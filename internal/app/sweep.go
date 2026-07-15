package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"piping/internal/job"
)

type sweepJobStore interface {
	StaleJobs(ctx context.Context, states []job.State, olderThan time.Time, limit int) ([]job.Job, error)
	UpdateJobState(ctx context.Context, id int64, from, to job.State) error
}

type Sweeper struct {
	jobs       sweepJobStore
	interval   time.Duration
	ageBound   time.Duration
	batchLimit int
	log        *slog.Logger
}

func NewSweeper(jobs sweepJobStore, interval, ageBound time.Duration, batchLimit int, log *slog.Logger) *Sweeper {
	return &Sweeper{jobs: jobs, interval: interval, ageBound: ageBound, batchLimit: batchLimit, log: log}
}

func (s *Sweeper) Run(ctx context.Context) {
	s.pass(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pass(ctx)
		}
	}
}

func (s *Sweeper) pass(ctx context.Context) {
	cutoff := time.Now().Add(-s.ageBound)
	stale, err := s.jobs.StaleJobs(ctx, []job.State{job.QuotaDeducted, job.PrintSent}, cutoff, s.batchLimit)
	if err != nil {
		s.log.Error("sweep: listing stale jobs", "err", err)
		return
	}
	for _, j := range stale {
		err := s.jobs.UpdateJobState(ctx, j.ID, j.State, job.PrintFailed)
		switch {
		case errors.Is(err, job.ErrUnexpectedState):
			// resolved by Deliverer
			continue
		case err != nil:
			s.log.Error("sweep: resolving job", "job", j.ID, "err", err)
			continue
		default:
			s.log.Warn("sweep: refunded stale job", "job", j.ID, "was", j.State)
		}
	}
}
