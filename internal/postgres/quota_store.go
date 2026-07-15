package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"

	"piping/internal/job"
	"piping/internal/quota"
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

func (s *Store) GrantExists(ctx context.Context, username string, semesterID int) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM semester_grant
			WHERE user_id = $1 AND semester_id = $2
		)`,
		username, semesterID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking grant for %s semester %d: %w", username, semesterID, err)
	}
	return exists, nil
}

// upsert semester or return default quota of existing semester if it already exists
func (s *Store) EnsureSemester(ctx context.Context, id int, defaultQuota int) (int, error) {
	var effective int
	name := SemesterNameFromCode(id)
	err := s.pool.QueryRow(ctx, `
		INSERT INTO semester (id, name, default_quota)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET id = excluded.id
		RETURNING default_quota`,
		id, name, defaultQuota).Scan(&effective)
	if err != nil {
		return 0, fmt.Errorf("ensuring semester %d: %w", id, err)
	}
	return effective, nil
}

func (s *Store) CreateGrant(ctx context.Context, username string, semesterID, amount int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO semester_grant (user_id, semester_id, amount)
		VALUES ($1, $2, $3)`,
		username, semesterID, amount)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return fmt.Errorf("grant for %s semester %d: %w", username, semesterID, quota.ErrGrantExists)
	}
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

func (s *Store) ListRolloverCandidates(ctx context.Context, sinceSemester int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT user_id FROM semester_grant
		WHERE semester_id >= $1 ORDER BY user_id`, sinceSemester)
	if err != nil {
		return nil, fmt.Errorf("listing rollover candidates: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		err := rows.Scan(&u)
		if err != nil {
			return nil, fmt.Errorf("scanning username: %w", err)
		}
		out = append(out, u)
	}
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("iterating candidates: %w", err)
	}
	return out, nil
}

func SemesterNameFromCode(code int) string {
	year := code / 100
	switch code % 100 {
	case 1:
		return "Winter " + strconv.Itoa(year)
	case 5:
		return "Summer " + strconv.Itoa(year)
	case 9:
		return "Fall " + strconv.Itoa(year)
	default:
		return "Semester " + strconv.Itoa(code)
	}
}
