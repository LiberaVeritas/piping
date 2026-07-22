package postgres

import (
	"context"
)

func (s *Store) EnsureUser(ctx context.Context, username string) error {

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO app_user (id) VALUES ($1)
		ON CONFLICT (id) DO NOTHING`,
		username); err != nil {
		return s.translateWriteErr(err, "ensuring user "+username)
	}
	return nil
}
