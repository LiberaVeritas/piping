package app

import (
	"context"
	"errors"
	"fmt"

	"piping/internal/job"
	"piping/internal/pdf"
	"piping/internal/queue"
	"piping/internal/quota"
)

var (
	ErrTooLarge      = errors.New("document exceeds size limit")
	ErrTooManyPages  = errors.New("document exceeds page limit")
	ErrInvalidCopies = errors.New("copy count out of range")
)

type pdfAnalyzer interface {
	CountPages(ctx context.Context, doc []byte) (pages, colorPages int, err error)
}

type queueReader interface {
	GetQueue(ctx context.Context, id int64) (queue.Queue, error)
}

// must be atomic to avoid double spend
type jobCheckQuotaStore interface {
	CheckQuotaAndStore(ctx context.Context, j job.Job) (job.Job, error)
}

type deliverer interface {
	Deliver(ctx context.Context, j job.Job, doc []byte) (DeliveryOutcome, error)
}

type SubmitInput struct {
	Username string
	QueueID  int64
	Document []byte
	Color    bool
	Duplex   bool
	Copies   int
	Filename string
}

type SubmitResult struct {
	JobID   int64
	Pages   int
	Cost    int
	Outcome DeliveryOutcome
}

type Submitter struct {
	analyzer  pdfAnalyzer
	queues    queueReader
	jobs      jobCheckQuotaStore
	deliver   deliverer
	rates     quota.Rates
	maxBytes  int
	maxPages  int
	maxCopies int
}

func NewSubmitter(a pdfAnalyzer, q queueReader, j jobCheckQuotaStore, d deliverer, r quota.Rates, maxBytes, maxPages, maxCopies int) *Submitter {
	return &Submitter{
		analyzer:  a,
		queues:    q,
		jobs:      j,
		deliver:   d,
		rates:     r,
		maxBytes:  maxBytes,
		maxPages:  maxPages,
		maxCopies: maxCopies,
	}
}

func (s *Submitter) Submit(ctx context.Context, in SubmitInput) (SubmitResult, error) {
	if len(in.Document) > s.maxBytes {
		return SubmitResult{}, ErrTooLarge
	}
	if !pdf.IsPDF(in.Document) {
		return SubmitResult{}, pdf.ErrNotPDF
	}

	q, err := s.queues.GetQueue(ctx, in.QueueID)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("getting queue %d: %w", in.QueueID, err)
	}
	if !q.Enabled {
		return SubmitResult{}, fmt.Errorf("queue %d disabled: %w", in.QueueID, queue.ErrUnavailable)
	}

	pages, colorPages, err := s.analyzer.CountPages(ctx, in.Document)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("analyzing pdf: %w", err)
	}
	if !in.Color {
		colorPages = 0
	}
	if pages <= 0 || colorPages < 0 || colorPages > pages {
		return SubmitResult{}, fmt.Errorf("analyzer returned pages=%d colorPages=%d: %w", pages, colorPages, pdf.ErrUnreadable)
	}
	if pages > s.maxPages {
		return SubmitResult{}, fmt.Errorf("page count %d over limit %d: %w", pages, s.maxPages, ErrTooManyPages)
	}
	if in.Copies < 1 || in.Copies > s.maxCopies {
		return SubmitResult{}, fmt.Errorf("copy count %d outside [1,%d]: %w", in.Copies, s.maxCopies, ErrInvalidCopies)
	}

	cost := s.rates.Cost(pages, colorPages) * in.Copies

	// db assigns id, sets state, and sets submitted_at
	j := job.Job{
		Username:      in.Username,
		QueueID:       in.QueueID,
		NumPages:      pages,
		NumColorPages: colorPages,
		Copies:        in.Copies,
		Cost:          cost,
		Color:         in.Color,
		Duplex:        in.Duplex,
		DocumentName:  in.Filename,
	}

	// disconnect context from browser, and own the job lifetime
	ctx = context.WithoutCancel(ctx)

	created, err := s.jobs.CheckQuotaAndStore(ctx, j)
	if err != nil {
		if errors.Is(err, quota.ErrInsufficient) {
			return SubmitResult{JobID: created.ID, Pages: pages, Cost: cost}, fmt.Errorf("cost %d over remaining quota: %w", cost, err)
		}
		return SubmitResult{}, fmt.Errorf("storing job: %w", err)
	}

	outcome, err := s.deliver.Deliver(ctx, created, in.Document)
	res := SubmitResult{JobID: created.ID, Pages: pages, Cost: cost, Outcome: outcome}
	if err != nil {
		return res, fmt.Errorf("delivering job %d: %w", created.ID, err)
	}
	return res, nil
}
