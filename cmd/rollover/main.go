package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"piping/internal/app"
	"piping/internal/directory"
	"piping/internal/postgres"
)

func main() {
	err := run(context.Background(), os.Getenv, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, logw io.Writer) error {
	log := slog.New(slog.NewTextHandler(logw, nil))

	databaseURL, err := envRequired(getenv, "DATABASE_URL")
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
	dc := &directory.Client{
		BaseURL:        baseURL,
		NinshouPath:    ninshouPath,
		AttrispoolPath: attrispoolPath,
		User:           username,
		Password:       password,
		Log:            log,
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()
	err = pool.Ping(ctx)
	if err != nil {
		return fmt.Errorf("database unreachable: %w", err)
	}

	store := postgres.New(pool, log)
	prov := app.NewProvisioner(dc, store, defaultQuota, log)
	code := app.CurrentSemester(time.Now())

	sinceSemester := code - 100 // same semester last year
	users, err := store.ListRolloverCandidates(ctx, sinceSemester)
	if err != nil {
		return fmt.Errorf("listing rollover candidates: %w", err)
	}

	for _, u := range users {
		uctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		prov.ReconcileGrants(uctx, u, code)
		cancel()
	}
	log.Info("rollover complete", "semester", code, "users_processed", len(users))
	return nil
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

func envStr(getenv func(string) string, key, def string) string {
	v := getenv(key)
	if v != "" {
		return v
	}
	return def
}
