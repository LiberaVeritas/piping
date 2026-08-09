package app_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"piping/internal/app"
	"piping/internal/job"
)

type sweepUpdate struct {
	id       int64
	from, to job.State
}

type fakeSweepStore struct {
	stale    []job.Job
	staleErr error
	failWith map[int64]error

	askedStates []job.State
	askedCutoff time.Time
	askedLimit  int
	updates     []sweepUpdate
}

func (f *fakeSweepStore) StaleJobs(_ context.Context, states []job.State,
	olderThan time.Time, limit int) ([]job.Job, error) {
	f.askedStates, f.askedCutoff, f.askedLimit = states, olderThan, limit
	return f.stale, f.staleErr
}

func (f *fakeSweepStore) UpdateJobState(_ context.Context, id int64, from, to job.State) error {
	f.updates = append(f.updates, sweepUpdate{id, from, to})
	return f.failWith[id]
}

// Run does one pass before it ever waits on the ticker, so an already-cancelled
// context gives exactly one deterministic pass.
func onePass(t *testing.T, f *fakeSweepStore, ageBound time.Duration, limit int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app.NewSweeper(f, time.Hour, ageBound, limit, slog.New(slog.DiscardHandler)).Run(ctx)
}

func TestSweepResolvesStaleJobsFromTheirOwnState(t *testing.T) {
	f := &fakeSweepStore{stale: []job.Job{
		{ID: 1, State: job.QuotaDeducted},
		{ID: 2, State: job.PrintSent},
	}}

	onePass(t, f, 30*time.Minute, 50)

	if len(f.updates) != 2 {
		t.Fatalf("updates = %+v, want one per stale job", f.updates)
	}
	for i, u := range f.updates {
		want := f.stale[i]
		// the guard must name the state the job is actually in; hardcoding one
		// would make the UPDATE match nothing and silently strand the job.
		if u.id != want.ID || u.from != want.State {
			t.Errorf("update %+v, want id %d from %q", u, want.ID, want.State)
		}
		if u.to != job.PrintFailed {
			t.Errorf("update %+v resolves to %q, want %q", u, u.to, job.PrintFailed)
		}
		if !job.ValidTransition(u.from, u.to) {
			t.Errorf("sweep attempted invalid transition %q -> %q", u.from, u.to)
		}
	}
}

func TestSweepAsksOnlyForUnresolvedStates(t *testing.T) {
	f := &fakeSweepStore{}
	const (
		ageBound = 30 * time.Minute
		limit    = 25
	)

	onePass(t, f, ageBound, limit)

	if len(f.askedStates) != 2 {
		t.Fatalf("asked for states %v, want the two non-terminal ones", f.askedStates)
	}
	for _, s := range f.askedStates {
		if job.IsTerminal(s) {
			t.Errorf("sweep asked for terminal state %q — already-resolved jobs must be left alone", s)
		}
	}
	if f.askedLimit != limit {
		t.Errorf("limit = %d, want %d", f.askedLimit, limit)
	}
	if age := time.Since(f.askedCutoff); age < ageBound {
		t.Errorf("cutoff is %v old, want at least %v — younger jobs may still be in flight", age, ageBound)
	}
}

// the Deliverer resolving a job first is the expected race, not an error, and
// it must not stop the rest of the batch.
func TestSweepContinuesPastAlreadyResolvedJobs(t *testing.T) {
	f := &fakeSweepStore{
		stale: []job.Job{
			{ID: 1, State: job.PrintSent},
			{ID: 2, State: job.PrintSent},
			{ID: 3, State: job.QuotaDeducted},
		},
		failWith: map[int64]error{
			1: job.ErrUnexpectedState,
			2: errors.New("db down"),
		},
	}

	onePass(t, f, time.Minute, 10)

	if len(f.updates) != 3 {
		t.Errorf("updates = %+v, want all three attempted", f.updates)
	}
}

func TestSweepUpdatesNothingWhenQueryFails(t *testing.T) {
	f := &fakeSweepStore{
		staleErr: errors.New("db down"),
		stale:    []job.Job{{ID: 1, State: job.PrintSent}},
	}

	onePass(t, f, time.Minute, 10)

	if len(f.updates) != 0 {
		t.Errorf("updates = %+v, want none after a failed query", f.updates)
	}
}
