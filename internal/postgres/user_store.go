package postgres

import (
	"context"
)

func (s *Store) EnsureUser(ctx context.Context, username string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_user (id) VALUES ($1)
		ON CONFLICT (id) DO NOTHING`,
		username)
	if err != nil {
		return s.translateWriteErr(err, "ensuring user "+username)
	}
	return nil
}
