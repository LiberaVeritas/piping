package postgres

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Store {
	return &Store{pool: pool, log: log}
}

func (s *Store) translateWriteErr(err error, op string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23514": // check_violation
			s.log.Error("database CHECK violation — upstream validation bypassed or broken", "op", op, "constraint", pgErr.ConstraintName)
			return fmt.Errorf("%s: check constraint %q violated: %w", op, pgErr.ConstraintName, err)
		case "23505": // unique_violation (e.g. one grant per user per semester)
			return fmt.Errorf("%s: already exists (%s): %w", op, pgErr.ConstraintName, err)
		case "23503": // foreign_key_violation
			return fmt.Errorf("%s: referenced row missing (%s): %w", op, pgErr.ConstraintName, err)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
