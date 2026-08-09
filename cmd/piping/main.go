package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"piping/internal/app"
	"piping/internal/ghostscript"
	"piping/internal/oidc"
	"piping/internal/postgres"
	"piping/internal/quota"
	"piping/internal/seal"
	"piping/internal/session"
	"piping/internal/smb"
	"piping/internal/web"
)

func main() {

	if err := run(context.Background(), os.Getenv, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, logw io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	logLevel := slog.LevelVar{}
	if err := logLevel.UnmarshalText([]byte(envStr(getenv, "LOG_LEVEL", slog.LevelInfo.String()))); err != nil {
		fmt.Fprint(logw, fmt.Errorf("failed to parse LOG_LEVEL env var %q: %w", getenv("LOG_LEVEL"), err))
		logLevel.Set(slog.LevelInfo)
	}
	log := slog.New(slog.NewTextHandler(logw, &slog.HandlerOptions{Level: &logLevel}))

	listenAddr := envStr(getenv, "LISTEN_ADDR", ":8080")
	dbUser, err := envRequired(getenv, "DB_USER")
	if err != nil {
		return err
	}
	dbPassword, err := envRequired(getenv, "DB_PASSWORD")
	if err != nil {
		return err
	}
	dbHost, err := envRequired(getenv, "DB_HOST")
	if err != nil {
		return err
	}
	dbName, err := envRequired(getenv, "DB_NAME")
	if err != nil {
		return err
	}
	smbAuthFile, err := envRequired(getenv, "SMB_AUTH_FILE")
	if err != nil {
		return err
	}
	sendTimeout, err := envDuration(getenv, "SEND_TIMEOUT", 5*time.Second)
	if err != nil {
		return err
	}
	sweepEvery, err := envDuration(getenv, "SWEEP_INTERVAL", time.Minute)
	if err != nil {
		return err
	}
	sweepAge, err := envDuration(getenv, "SWEEP_AGE_BOUND", time.Minute)
	if err != nil {
		return err
	}
	sweepBatch, err := envInt(getenv, "SWEEP_BATCH", 100)
	if err != nil {
		return err
	}
	maxBytes, err := envInt(getenv, "MAX_BYTES", 50<<20)
	if err != nil {
		return err
	}
	maxPages, err := envInt(getenv, "MAX_PAGES", 100)
	if err != nil {
		return err
	}
	maxCopies, err := envInt(getenv, "MAX_COPIES", 50)
	if err != nil {
		return err
	}
	maxSendAttempts, err := envInt(getenv, "MAX_SEND_ATTEMPTS", 5)
	if err != nil {
		return err
	}
	colorRate, err := envInt(getenv, "COLOR_RATE", 3)
	if err != nil {
		return err
	}
	colorThreshold, err := envFloat(getenv, "COLOR_THRESHOLD", 0.0005)
	if err != nil {
		return err
	}
	defaultQuota, err := envInt(getenv, "DEFAULT_QUOTA", 250)
	if err != nil {
		return err
	}

	oidcClientID, err := envRequired(getenv, "OIDC_CLIENT_ID")
	if err != nil {
		return err
	}
	oidcClientSecret, err := envRequired(getenv, "OIDC_CLIENT_SECRET")
	if err != nil {
		return err
	}
	oidcRedirectURI, err := envRequired(getenv, "OIDC_REDIRECT_URI")
	if err != nil {
		return err
	}
	oidcScopes, err := envRequired(getenv, "OIDC_SCOPES")
	if err != nil {
		return err
	}
	oidcDiscoveryURL, err := envRequired(getenv, "OIDC_DISCOVERY_URL")
	if err != nil {
		return err
	}

	sealKey64, err := envRequired(getenv, "ENCRYPTION_KEY")
	if err != nil {
		return err
	}
	sealKey, err := base64.StdEncoding.DecodeString(sealKey64)
	if err != nil {
		return fmt.Errorf("seal key must be in base64: %w", err)
	}

	sessionTTL, err := envDuration(getenv, "SESSION_TTL", 60*time.Minute)
	if err != nil {
		return err
	}

	if sweepAge <= sendTimeout {
		return fmt.Errorf("SWEEP_AGE_BOUND %q must exceed SEND_TIMEOUT %q", sweepAge, sendTimeout)
	}
	if sweepAge < 2*sendTimeout {
		log.Warn("leave more margin", "sweep_age_bound", sweepAge, "send_timeout", sendTimeout)
	}

	pool, err := pgxpool.New(ctx, fmt.Sprintf("user=%s password=%s host=%s dbname=%s port=5432",
		dbUser, dbPassword, dbHost, dbName))
	if err != nil {
		return fmt.Errorf("creating db pool: %w", err)
	}
	defer pool.Close()
	err = pool.Ping(ctx)
	if err != nil {
		return fmt.Errorf("pinging database: %w", err)
	}

	store := postgres.New(pool, log)
	analyzer := ghostscript.New(colorThreshold, log)
	sender := smb.New(smbAuthFile)

	vctx, vcancel := context.WithTimeout(ctx, 5*time.Second)
	err = analyzer.VerifyDevice(vctx)
	vcancel()
	if err != nil {
		return fmt.Errorf("ghostscript unusable: %w", err)
	}

	deliverer := app.NewDeliverer(sender, store, store, 500*time.Millisecond, sendTimeout, maxSendAttempts, log)
	submitter := app.NewSubmitter(analyzer, store, store, deliverer,
		quota.Rates{ColorRate: colorRate}, maxBytes, maxPages, maxCopies, log)
	sweeper := app.NewSweeper(store, sweepEvery, sweepAge, sweepBatch, log)

	sealer, err := seal.NewSealer(sealKey)
	if err != nil {
		return err
	}
	cfg := oidc.ClientConfig{
		RedirectURI:  oidcRedirectURI,
		ClientID:     oidcClientID,
		ClientSecret: oidcClientSecret,
		Scopes:       oidcScopes,
		Sealer:       sealer,
		Log:          log,
	}

	oidcClient, err := oidc.NewFromDiscovery(ctx, cfg, oidcDiscoveryURL)
	if err != nil {
		log.Warn("oidc discovery", "err", err)
		authEndpoint, err := envRequired(getenv, "OIDC_AUTHORIZATION_ENDPOINT")
		if err != nil {
			return err
		}
		tokenEndpoint, err := envRequired(getenv, "OIDC_TOKEN_ENDPOINT")
		if err != nil {
			return err
		}
		userEndpoint, err := envRequired(getenv, "OIDC_USER_INFO_ENDPOINT")
		if err != nil {
			return err
		}
		oidcClient, err = oidc.New(ctx, cfg, authEndpoint, tokenEndpoint, userEndpoint)
		if err != nil {
			return err
		}
	}
	session := session.NewManager(sealer, sessionTTL, log)
	prov := app.NewProvisioner(store, defaultQuota, log)

	build := envStr(getenv, "PIPING_BUILD", "")

	go sweeper.Run(ctx)

	ready := func(c context.Context) error {
		return pool.Ping(c)
	}
	u, err := url.Parse(oidcRedirectURI)
	if err != nil {
		return fmt.Errorf("parsing oidc redirect uri %q: %w", oidcRedirectURI, err)
	}
	origin := u.Scheme + "://" + u.Host
	srv, err := web.NewServer(submitter, prov, store, oidcClient, session, ready, int64(maxBytes), maxCopies, origin, build, log)
	if err != nil {
		return fmt.Errorf("creating web server: %w", err)
	}
	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 2 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		stop()
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		if err := httpSrv.Shutdown(shutCtx); err != nil {
			log.Error("shutting down http server", "err", err)
		}
	}()

	log.Info("piping listening", "addr", listenAddr)
	err = httpSrv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	wg.Wait()
	log.Info("piping stopped")
	return nil
}

func envStr(getenv func(string) string, key, def string) string {

	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func envRequired(getenv func(string) string, key string) (string, error) {
	v := getenv(key)
	if v == "" {
		return "", fmt.Errorf("missing required environment variable %s", key)
	}
	return v, nil
}

func envInt(getenv func(string) string, key string, def int) (int, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid integer in %s: %q", key, v)
	}
	return n, nil
}

func envFloat(getenv func(string) string, key string, def float64) (float64, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float in %s: %q", key, v)
	}
	return n, nil
}

func envDuration(getenv func(string) string, key string, def time.Duration) (time.Duration, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid duration in %s: %q", key, v)
	}
	return d, nil
}
