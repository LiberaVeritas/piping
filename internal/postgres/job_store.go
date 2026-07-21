package postgres

import (
	"context"
	"fmt"
	"time"

	"piping/internal/job"
	"piping/internal/quota"
)

// read verify and update atomically
func (s *Store) CheckQuotaAndStore(ctx context.Context, j job.Job) (job.Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return job.Job{}, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) // no-op if Commit succeeds

	var remaining int64
	err = tx.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(g.amount) FROM semester_grant g WHERE g.user_id = $1), 0)
		     - COALESCE((SELECT SUM(j.cost)  FROM job j WHERE j.user_id = $1 AND j.state::text = ANY($2)), 0)`,
		j.Username, job.QuotaDeductingStateNames()).Scan(&remaining)
	if err != nil {
		return job.Job{}, fmt.Errorf("deriving remaining quota for %q: %w", j.Username, err)
	}

	state := job.QuotaDeducted
	if remaining < int64(j.Cost) {
		state = job.QuotaInsufficient
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO job (user_id, queue_id, state, num_pages, num_color_pages, copies, cost, color, duplex, document_name, completed_at)
		VALUES ($1, $2, $3::job_state, $4, $5, $6, $7, $8, $9, $10, CASE WHEN $3::job_state = 'quota_insufficient' THEN now() END)
		RETURNING id, submitted_at`,
		j.Username, j.QueueID, state.String(), j.NumPages, j.NumColorPages,
		j.Copies, j.Cost, j.Color, j.Duplex, j.DocumentName).Scan(&j.ID, &j.SubmittedAt)
	if err != nil {
		return job.Job{}, s.translateWriteErr(err, "storing job")
	}

	err = tx.Commit(ctx)
	if err != nil {
		return job.Job{}, fmt.Errorf("committing job to db: %w", err)
	}

	j.State = state
	if state == job.QuotaInsufficient {
		return j, fmt.Errorf("cost %d, remaining %d: %w", j.Cost, remaining, quota.ErrInsufficient)
	}
	return j, nil
}

func (s *Store) UpdateJobState(ctx context.Context, id int64, from, to job.State) error {
	if !job.ValidTransition(from, to) {
		return fmt.Errorf("transition %s -> %s for job %d: %w", from, to, id, job.ErrInvalidTransition)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE job SET state = $2::job_state,
		       completed_at = CASE WHEN $3 THEN now() ELSE completed_at END,
		       refunded_at  = CASE WHEN $4 THEN now() ELSE refunded_at  END
		WHERE id = $1 AND state = $5::job_state`,
		id, to.String(), job.IsTerminal(to), to == job.Refunded, from.String())
	if err != nil {
		return s.translateWriteErr(err, fmt.Sprintf("updating job %d state", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("job %d not in %s (wanted -> %s): %w", id, from, to, job.ErrUnexpectedState)
	}
	return nil
}

func (s *Store) MarkSent(ctx context.Context, jobID, destID int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE job SET state = 'print_sent', destination_id = $2
		WHERE id = $1 AND state = 'quota_deducted'`, jobID, destID)
	if err != nil {
		return s.translateWriteErr(err, fmt.Sprintf("marking job %d sent", jobID))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("job %d not in quota_deducted: %w", jobID, job.ErrUnexpectedState)
	}
	return nil
}

const jobColumns = `
  id, user_id, queue_id, destination_id, state::text,
  num_pages, num_color_pages, copies, cost, color, duplex,
	document_name, submitted_at, completed_at, refunded_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanJob(sc rowScanner) (job.Job, error) {
	var j job.Job
	var state string
	err := sc.Scan(
		&j.ID, &j.Username, &j.QueueID, &j.DestinationID, &state,
		&j.NumPages, &j.NumColorPages, &j.Copies, &j.Cost, &j.Color, &j.Duplex,
		&j.DocumentName, &j.SubmittedAt, &j.CompletedAt, &j.RefundedAt)
	if err != nil {
		return job.Job{}, err
	}
	j.State, err = job.StateFromString(state)
	return j, err
}

func (s *Store) StaleJobs(ctx context.Context, states []job.State, olderThan time.Time, limit int) ([]job.Job, error) {
	names := make([]string, len(states))
	for i, st := range states {
		names[i] = st.String()
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+jobColumns+` FROM job
		WHERE state::text = ANY($1) AND submitted_at < $2
		ORDER BY submitted_at LIMIT $3`, names, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("listing stale jobs: %w", err)
	}
	defer rows.Close()
	var out []job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning stale job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) JobsForUser(ctx context.Context, username string, limit int) ([]job.Job, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+jobColumns+` FROM job
		WHERE user_id = $1 ORDER BY submitted_at DESC LIMIT $2`, username, limit)
	if err != nil {
		return nil, fmt.Errorf("listing jobs for %q: %w", username, err)
	}
	defer rows.Close()
	var out []job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
