package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"piping/internal/job"
	"piping/internal/printticket"
	"piping/internal/queue"
)

type sender interface {
	Send(ctx context.Context, dest queue.Destination, payload []byte) error
}

type deliveryJobStore interface {
	UpdateJobState(ctx context.Context, id int64, from, to job.State) error
	MarkSent(ctx context.Context, jobID, destID int64) error
}

type deliveryQueueStore interface {
	DestinationsForQueue(ctx context.Context, queueID int64) ([]queue.Destination, error)
	LoadBalancerPolicyForQueue(ctx context.Context, queueID int64) (queue.LoadBalancerPolicy, error)
}

type DeliveryOutcome struct{ name string }

func (o DeliveryOutcome) String() string { return o.name }

var (
	DeliveryFailed    = DeliveryOutcome{"delivery_failed"}
	DeliverySucceeded = DeliveryOutcome{"delivery_succeeded"}
)

type Deliverer struct {
	sender      sender
	jobs        deliveryJobStore
	queues      deliveryQueueStore
	timeout     time.Duration
	maxAttempts int
	retryWait   time.Duration
	log         *slog.Logger
}

func NewDeliverer(s sender, j deliveryJobStore, q deliveryQueueStore,
	sendTimeout time.Duration, maxAttempts int, log *slog.Logger) *Deliverer {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &Deliverer{
		sender:      s,
		jobs:        j,
		queues:      q,
		timeout:     sendTimeout,
		maxAttempts: maxAttempts,
		retryWait:   500 * time.Millisecond,
		log:         log,
	}
}

func (d *Deliverer) Deliver(ctx context.Context, j job.Job, doc []byte) (DeliveryOutcome, error) {
	dest, err := d.pickDestination(ctx, j.QueueID)
	if err != nil {
		return d.resolve(ctx, j.ID, job.QuotaDeducted, DeliveryFailed, err)
	}

	err = d.jobs.MarkSent(ctx, j.ID, dest.ID)
	if errors.Is(err, job.ErrUnexpectedState) {
		d.log.Warn("delivery intercepted before send", "job", j.ID)
		return DeliveryFailed, nil // resolved by sweep; quota returned
	}
	if err != nil {
		return DeliveryFailed, fmt.Errorf("marking job %d sent: %w", j.ID, err)
	}

	sendCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	attrs := printticket.FromJob(j)
	ticketedDoc := printticket.XCPT(attrs, doc)

	for attempt := 1; ; attempt++ {
		err = d.sender.Send(sendCtx, dest, ticketedDoc)
		if err == nil {
			return d.resolve(ctx, j.ID, job.PrintSent, DeliverySucceeded, nil)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return d.resolve(ctx, j.ID, job.PrintSent, DeliveryFailed, err)
		}
		if attempt >= d.maxAttempts {
			d.log.Warn("send failed too many times", "job", j.ID, "dest", dest.ID)
			return d.resolve(ctx, j.ID, job.PrintSent, DeliveryFailed, err)
		}
		d.log.Warn("send failed; retrying", "job", j.ID, "dest", dest.ID,
			"attempt", attempt, "of", d.maxAttempts, "cause", err)
		select {
		case <-sendCtx.Done():
			return d.resolve(ctx, j.ID, job.PrintSent, DeliveryFailed, err)
		case <-time.After(d.retryWait):
		}
	}
}

func (d *Deliverer) resolve(ctx context.Context, id int64, from job.State,
	out DeliveryOutcome, cause error) (DeliveryOutcome, error) {
	if cause != nil {
		d.log.Error("delivery failed", "job", id, "outcome", out, "cause", cause)
	}

	to := job.PrintFailed
	if out == DeliverySucceeded {
		to = job.PrintSucceeded
	}

	err := d.jobs.UpdateJobState(ctx, id, from, to)
	if errors.Is(err, job.ErrUnexpectedState) {
		d.log.Warn("job found already resolved", "job", id, "intended", to)
		return DeliveryFailed, nil
	}
	if err != nil {
		return out, fmt.Errorf("recording outcome for job %d: %w", id, err)
	}
	return out, nil
}

func (d *Deliverer) pickDestination(ctx context.Context, queueID int64) (queue.Destination, error) {
	dests, err := d.queues.DestinationsForQueue(ctx, queueID)
	if err != nil {
		return queue.Destination{}, fmt.Errorf("getting destinations for queue %d: %w", queueID, err)
	}
	dests = queue.EnabledDestinations(dests)
	if len(dests) == 0 {
		return queue.Destination{}, fmt.Errorf("no enabled destinations for queue %d", queueID)
	}
	policy, err := d.queues.LoadBalancerPolicyForQueue(ctx, queueID)
	if err != nil {
		return queue.Destination{}, fmt.Errorf("getting lb policy for queue %d: %w", queueID, err)
	}
	lb, err := queue.LoadBalancerFromPolicy(policy)
	if err != nil {
		return queue.Destination{}, fmt.Errorf("getting load balancer %s for queue %d: %w", policy, queueID, err)
	}
	dest, err := lb.Choose(dests)
	if err != nil {
		return queue.Destination{}, fmt.Errorf("choosing destination for queue %d: %w", queueID, err)
	}
	return dest, nil
}
