package postgres

import (
	"context"
	"fmt"

	"piping/internal/job"
	"piping/internal/semester"
)

func (s *Store) RemainingQuota(ctx context.Context, username string) (int, error) {
	var remaining int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(g.amount) FROM semester_grant g WHERE g.user_id = $1), 0)
		     - COALESCE((SELECT SUM(j.cost) FROM job j WHERE j.user_id = $1 AND j.state::text = ANY($2)), 0)`,
		username, job.QuotaDeductingStateNames()).Scan(&remaining)
	if err != nil {
		return 0, fmt.Errorf("db query grant amount and job cost: %w", err)
	}
	return int(remaining), nil
}

func (s *Store) GrantedQuota(ctx context.Context, username string) (int, error) {
	var granted int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(g.amount) FROM semester_grant g WHERE g.user_id = $1), 0)`,
		username).Scan(&granted)
	if err != nil {
		return 0, fmt.Errorf("db query grant amount: %w", err)
	}
	return int(granted), nil
}

func (s *Store) SpentQuota(ctx context.Context, username string) (int, error) {
	var spent int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(j.cost) FROM job j WHERE j.user_id = $1 AND j.state::text = ANY($2)), 0)`,
		username, job.QuotaDeductingStateNames()).Scan(&spent)
	if err != nil {
		return 0, fmt.Errorf("db query job cost: %w", err)
	}
	return int(spent), nil
}

// upsert semester or return default quota of existing semester if it already exists
// DO UPDATE does nothing but ensures that quota is returned even on conflict
func (s *Store) EnsureSemester(ctx context.Context, id int, defaultQuota int) (int, error) {
	var effective int
	name := semester.Name(id)
	err := s.pool.QueryRow(ctx, `
		INSERT INTO semester (id, name, default_quota)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET id = EXCLUDED.id
		RETURNING default_quota`,
		id, name, defaultQuota).Scan(&effective)
	if err != nil {
		return 0, fmt.Errorf("db query semester: %w", err)
	}
	return effective, nil
}

func (s *Store) EnsureGrant(ctx context.Context, username string, semesterID, amount int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO semester_grant (user_id, semester_id, amount)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, semester_id) DO NOTHING`,
		username, semesterID, amount)
	if err != nil {
		return fmt.Errorf("db op grant: %w", err)
	}
	return nil
}
