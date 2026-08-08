package postgres

import (
	"context"
	"fmt"
	"piping/internal/job"
	"time"
)

func (s *Store) EnsureUser(ctx context.Context, username string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_user (id) VALUES ($1)
		ON CONFLICT (id) DO NOTHING`,
		username)
	if err != nil {
		return s.mapPostgresError(err, "db op user")
	}
	return nil
}

func (s *Store) JobsForUser(ctx context.Context, username string, newerThan time.Time, limit int) ([]job.Job, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+jobColumns+` FROM job
		WHERE user_id = $1 AND submitted_at > $2 ORDER BY submitted_at DESC LIMIT $3`,
		username, newerThan, limit)
	if err != nil {
		return nil, fmt.Errorf("db query job: %w", err)
	}
	defer rows.Close()
	var out []job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("db scan job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func scanJobView(sc rowScanner) (job.WithDestinationName, error) {
	var jv job.WithDestinationName
	var state string
	err := sc.Scan(
		&jv.SubmittedAt, &jv.DocumentName, &jv.NumPages, &jv.Copies, &jv.Cost, &state, &jv.DestinationName)
	if err != nil {
		return job.WithDestinationName{}, err
	}
	s, err := job.StateFromString(state)
	if err != nil {
		return job.WithDestinationName{}, err
	}
	jv.State = s
	return jv, err
}

func (s *Store) JobsWithDestinationForUser(ctx context.Context, username string, newerThan time.Time, limit int) ([]job.WithDestinationName, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT submitted_at, document_name, num_pages, copies, cost, state::text, COALESCE(d.name, 'None') FROM job
		LEFT JOIN destination d ON destination_id = d.id
		WHERE user_id = $1 AND submitted_at > $2 ORDER BY submitted_at DESC LIMIT $3`,
		username, newerThan, limit)
	if err != nil {
		return nil, fmt.Errorf("db query job view: %w", err)
	}
	defer rows.Close()
	var out []job.WithDestinationName
	for rows.Next() {
		jv, err := scanJobView(rows)
		if err != nil {
			return nil, fmt.Errorf("db scan job view: %w", err)
		}
		out = append(out, jv)
	}
	return out, rows.Err()
}
