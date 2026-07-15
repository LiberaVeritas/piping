package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"piping/internal/app"
	"piping/internal/directory"
	"piping/internal/ghostscript"
	"piping/internal/postgres"
	"piping/internal/quota"
	"piping/internal/smb"
	"piping/internal/web"
)

func main() {
	err := run(context.Background(), os.Getenv, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, logw io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewTextHandler(logw, nil))

	listenAddr := envStr(getenv, "LISTEN_ADDR", ":8080")
	databaseURL, err := envRequired(getenv, "DATABASE_URL")
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
	maxPages, err := envInt(getenv, "MAX_PAGES", 200)
	if err != nil {
		return err
	}
	maxCopies, err := envInt(getenv, "MAX_COPIES", 100)
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
	baseURL := envStr(getenv, "CTF_API_BASE_URL", "api.ctf.mcgill.ca")
	ninshouPath := envStr(getenv, "NINSHOU_PATH", "ninshou/api/v1/authenticate/simple")
	attrispoolPath := envStr(getenv, "ATTRISPOOL_PATH", "/api/v1/users")
	username, err := envRequired(getenv, "NINSHOU_USERNAME")
	if err != nil {
		return err
	}
	password, err := envRequired(getenv, "NINSHOU_PASSWORD")
	if err != nil {
		return err
	}
	defaultQuota, err := envInt(getenv, "DEFAULT_QUOTA", 250)
	if err != nil {
		return err
	}

	if sweepAge <= sendTimeout {
		return fmt.Errorf("SWEEP_AGE_BOUND %s must exceed SEND_TIMEOUT %s", sweepAge, sendTimeout)
	}
	if sweepAge < 2*sendTimeout {
		log.Warn("leave more margin", "sweep_age_bound", sweepAge, "send_timeout", sendTimeout)
	}

	pool, err := pgxpool.New(ctx, databaseURL)
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
	sender := smb.New(smbAuthFile, log)

	vctx, vcancel := context.WithTimeout(ctx, 5*time.Second)
	err = analyzer.VerifyDevice(vctx)
	vcancel()
	if err != nil {
		return fmt.Errorf("ghostscript unusable: %w", err)
	}

	deliverer := app.NewDeliverer(sender, store, store, sendTimeout, maxSendAttempts, log)
	submitter := app.NewSubmitter(analyzer, store, store, deliverer,
		quota.Rates{ColorRate: colorRate}, maxBytes, maxPages, maxCopies)
	sweeper := app.NewSweeper(store, sweepEvery, sweepAge, sweepBatch, log)

	dc := &directory.Client{
		BaseURL:        baseURL,
		NinshouPath:    ninshouPath,
		AttrispoolPath: attrispoolPath,
		User:           username,
		Password:       password,
		Log:            log,
	}

	prov := app.NewProvisioner(dc, store, defaultQuota, log)

	go sweeper.Run(ctx)

	ready := func(c context.Context) error {
		return pool.Ping(c)
	}
	srv := &http.Server{
		Addr:    listenAddr,
		Handler: web.NewServer(submitter, prov, ready, int64(maxBytes), log).Routes(),
	}

	log.Info("piping listening", "addr", listenAddr)
	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := srv.Shutdown(shutCtx)
		if err != nil {
			log.Error("shutting down http server", "err", err)
		}
	}()

	wg.Wait()
	log.Info("piping stopped")
	return nil
}

func envStr(getenv func(string) string, key, def string) string {
	v := getenv(key)
	if v != "" {
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
