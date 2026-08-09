package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"pgregory.net/rapid"

	"piping/internal/job"
	"piping/internal/queue"
)

type fakeJobStore struct {
	state      job.State
	destID     *int64
	violations []string
}

func (f *fakeJobStore) MarkSent(_ context.Context, id, destID int64) error {
	if f.state != job.QuotaDeducted {
		return fmt.Errorf("job %d in %v: %w", id, f.state, job.ErrUnexpectedState)
	}
	if !job.ValidTransition(job.QuotaDeducted, job.PrintSent) {
		f.violations = append(f.violations, "MarkSent: quota_deducted->print_sent invalid")
	}
	f.state = job.PrintSent
	f.destID = &destID
	return nil
}

func (f *fakeJobStore) UpdateJobState(_ context.Context, id int64, from, to job.State) error {
	if f.state != from {
		return fmt.Errorf("job %d in %v not %v: %w", id, f.state, from, job.ErrUnexpectedState)
	}
	if !job.ValidTransition(from, to) {
		f.violations = append(f.violations,
			fmt.Sprintf("attempted invalid %v -> %v", from, to))
	}
	f.state = to
	return nil
}

type staticQueueStore struct{}

func (staticQueueStore) DestinationsForQueue(context.Context, int64) ([]queue.Destination, error) {
	return []queue.Destination{{ID: 7, QueueID: 1, Address: "smb://x", Name: "d", Enabled: true}}, nil
}
func (staticQueueStore) LoadBalancerPolicyForQueue(context.Context, int64) (queue.LoadBalancerPolicy, error) {
	return queue.UniformPolicy, nil
}

type scriptedSender struct{ errs []error }

func (s *scriptedSender) Send(context.Context, queue.Destination, []byte) error {
	if len(s.errs) == 0 {
		return nil
	}
	e := s.errs[0]
	s.errs = s.errs[1:]
	return e
}

func TestDelivererRespectsStateMachine(t *testing.T) {
	boom := errors.New("smb exploded")
	rapid.Check(t, func(rt *rapid.T) {
		// retryWait is 0 here, so the retry path costs nothing and the
		// scripts can span every attempt count without sleeping.
		script := rapid.SampledFrom([][]error{
			{},                               // first-try success
			{context.DeadlineExceeded},       // timeout -> fail, never retried
			{boom},                           // one failure, then success if attempts allow
			{boom, boom},                     // two failures, then success if attempts allow
			{boom, boom, boom, boom},         // fails on every allowed attempt
			{boom, context.DeadlineExceeded}, // retried, then timed out
		}).Draw(rt, "script")
		maxAttempts := rapid.IntRange(1, 4).Draw(rt, "maxAttempts")

		store := &fakeJobStore{state: job.QuotaDeducted}
		d := NewDeliverer(&scriptedSender{errs: script}, store, staticQueueStore{},
			0, time.Second, maxAttempts, slog.New(slog.DiscardHandler))

		j := job.Job{ID: 1, QueueID: 1, State: job.QuotaDeducted, Copies: 1}
		outcome, err := d.Deliver(context.Background(), j, []byte("%PDF-1.4"))
		if err != nil {
			rt.Fatalf("Deliver returned error for handled sender failure: %v", err)
		}

		if len(store.violations) > 0 {
			rt.Fatalf("deliverer attempted invalid transitions: %v", store.violations)
		}
		succeeded := outcome == DeliverySucceeded
		if succeeded != (store.state == job.PrintSucceeded) {
			rt.Fatalf("outcome %v inconsistent with final state %v", outcome, store.state)
		}
		if succeeded != store.state.DeductsQuota() {
			rt.Fatalf("BILLING: outcome %v but DeductsQuota(%v)=%v — user charged iff printed must hold",
				outcome, store.state, store.state.DeductsQuota())
		}
		if !job.IsTerminal(store.state) {
			rt.Fatalf("job left in non-terminal %v", store.state)
		}
		if store.destID == nil {
			rt.Fatal("destination never recorded before send")
		}
	})
}

func TestDelivererRetriesThenSucceeds(t *testing.T) {
	store := &fakeJobStore{state: job.QuotaDeducted}
	d := NewDeliverer(&scriptedSender{errs: []error{errors.New("transient")}},
		store, staticQueueStore{}, 0, 5*time.Second, 2, slog.New(slog.DiscardHandler))
	outcome, err := d.Deliver(context.Background(),
		job.Job{ID: 1, QueueID: 1, State: job.QuotaDeducted, Copies: 1}, []byte("%PDF-1.4"))
	if err != nil || outcome != DeliverySucceeded {
		t.Fatalf("retry-then-success: outcome=%v err=%v", outcome, err)
	}
	if len(store.violations) > 0 {
		t.Fatalf("violations: %v", store.violations)
	}
}
