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
		return 0, fmt.Errorf("deriving remaining quota for %q: %w", username, err)
	}
	return int(remaining), nil
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
		return 0, fmt.Errorf("ensuring semester %d: %w", id, err)
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
		return fmt.Errorf("creating grant for %s semester %d: %w", username, semesterID, err)
	}
	return nil
}

func (s *Store) ListGrantSemesters(ctx context.Context, username string) ([]int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT semester_id FROM semester_grant WHERE user_id = $1`, username)
	if err != nil {
		return nil, fmt.Errorf("listing grant semesters for %s: %w", username, err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var t int
		err := rows.Scan(&t)
		if err != nil {
			return nil, fmt.Errorf("scanning grant semester: %w", err)
		}
		out = append(out, t)
	}
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("iterating grant semesters: %w", err)
	}
	return out, nil
}
