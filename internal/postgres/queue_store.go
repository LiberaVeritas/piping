package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"piping/internal/job"
	"piping/internal/queue"
)

func (s *Store) GetQueue(ctx context.Context, id int64) (queue.Queue, error) {
	var q queue.Queue
	var policy string
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, enabled, policy FROM queue WHERE id = $1`, id).
		Scan(&q.ID, &q.Name, &q.Enabled, &policy)
	if errors.Is(err, pgx.ErrNoRows) {
		return queue.Queue{}, fmt.Errorf("db no rows: %w", queue.ErrUnavailable)
	}
	if err != nil {
		return queue.Queue{}, fmt.Errorf("db query queue: %w", err)
	}
	q.Policy, err = queue.PolicyFromString(policy)
	if err != nil {
		return queue.Queue{}, fmt.Errorf("getting policy %q for queue: %w", policy, err)
	}
	return q, nil
}

func (s *Store) EnabledQueues(ctx context.Context) ([]queue.Queue, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, enabled, policy FROM queue WHERE enabled ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("db query queue: %w", err)
	}
	defer rows.Close()
	var out []queue.Queue
	for rows.Next() {
		var q queue.Queue
		var policy string
		if err = rows.Scan(&q.ID, &q.Name, &q.Enabled, &policy); err != nil {
			return nil, fmt.Errorf("db scan queue: %w", err)
		}
		q.Policy, err = queue.PolicyFromString(policy)
		if err != nil {
			return nil, fmt.Errorf("getting policy %q for queue: %w", policy, err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Store) DestinationsForQueue(ctx context.Context, queueID int64) ([]queue.Destination, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, queue_id, address, name, enabled
		FROM destination WHERE queue_id = $1`, queueID)
	if err != nil {
		return nil, fmt.Errorf("db query destination: %w", err)
	}
	defer rows.Close()
	var out []queue.Destination
	for rows.Next() {
		var d queue.Destination
		err := rows.Scan(&d.ID, &d.QueueID, &d.Address, &d.Name, &d.Enabled)
		if err != nil {
			return nil, fmt.Errorf("db scan destination: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) LoadBalancerPolicyForQueue(ctx context.Context, queueID int64) (queue.LoadBalancerPolicy, error) {
	var policy string
	err := s.pool.QueryRow(ctx, `
		SELECT policy FROM queue WHERE id = $1`, queueID).Scan(&policy)
	if err != nil {
		return queue.UnknownPolicy, fmt.Errorf("getting policy %q for queue: %w", policy, err)
	}
	return queue.PolicyFromString(policy)
}

func (s *Store) JobsForQueue(ctx context.Context, queueID int64, limit int) ([]job.Job, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+jobColumns+` FROM job
		WHERE queue_id = $1 ORDER BY submitted_at DESC LIMIT $2`, queueID, limit)
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

// maybe no
func (s *Store) JobsForQueueForUser(ctx context.Context,
	queueID int64, username string, limit int) ([]job.Job, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+jobColumns+` FROM job
		WHERE queue_id = $1 AND user_id = $2 ORDER BY submitted_at DESC LIMIT $3`,
		queueID, username, limit)
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
