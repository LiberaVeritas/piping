package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"piping/internal/queue"
)

func (s *Store) GetQueue(ctx context.Context, id int64) (queue.Queue, error) {
	var q queue.Queue
	var policy string
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, enabled, policy FROM queue WHERE id = $1`, id).
		Scan(&q.ID, &q.Name, &q.Enabled, &policy)
	if errors.Is(err, pgx.ErrNoRows) {
		return queue.Queue{}, fmt.Errorf("queue %d does not exist: %w", id, queue.ErrUnavailable)
	}
	if err != nil {
		return queue.Queue{}, fmt.Errorf("getting queue %d: %w", id, err)
	}
	q.Policy, err = queue.PolicyFromString(policy)
	if err != nil {
		return queue.Queue{}, fmt.Errorf("queue %d: %w", id, err)
	}
	return q, nil
}

func (s *Store) EnabledQueues(ctx context.Context) ([]queue.Queue, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, enabled, policy FROM queue WHERE enabled ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing queues: %w", err)
	}
	defer rows.Close()
	var out []queue.Queue
	for rows.Next() {
		var q queue.Queue
		var policy string
		err = rows.Scan(&q.ID, &q.Name, &q.Enabled, &policy)
		if err != nil {
			return nil, fmt.Errorf("scanning queue: %w", err)
		}
		q.Policy, err = queue.PolicyFromString(policy)
		if err != nil {
			return nil, fmt.Errorf("queue %d: %w", q.ID, err)
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
		return nil, fmt.Errorf("listing destinations for queue %d: %w", queueID, err)
	}
	defer rows.Close()
	var out []queue.Destination
	for rows.Next() {
		var d queue.Destination
		err := rows.Scan(&d.ID, &d.QueueID, &d.Address, &d.Name, &d.Enabled)
		if err != nil {
			return nil, fmt.Errorf("scanning destination: %w", err)
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
		return queue.UnknownPolicy, fmt.Errorf("getting policy for queue %d: %w", queueID, err)
	}
	return queue.PolicyFromString(policy)
}
