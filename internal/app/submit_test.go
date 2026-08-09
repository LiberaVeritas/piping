package app_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"piping/internal/app"
	"piping/internal/job"
	"piping/internal/pdf"
	"piping/internal/queue"
	"piping/internal/quota"
)

const (
	testMaxBytes  = 1 << 20
	testMaxPages  = 100
	testMaxCopies = 10
	testColorRate = 5
)

var minimalPDF = []byte("%PDF-1.4\n1 0 obj\n")

type fakeAnalyzer struct {
	pages, colorPages int
	err               error
}

func (f *fakeAnalyzer) CountPages(context.Context, []byte) (int, int, error) {
	return f.pages, f.colorPages, f.err
}

type fakeQueueReader struct {
	q   queue.Queue
	err error
}

func (f *fakeQueueReader) GetQueue(context.Context, int64) (queue.Queue, error) {
	return f.q, f.err
}

// mirrors postgres.Store.CheckQuotaAndStore: the row is always written and the
// returned job carries the id and the state the database decided on, even when
// the quota check rejects it.
type fakeQuotaStore struct {
	id           int64
	insufficient bool
	err          error
	got          job.Job
	calls        int
}

func (f *fakeQuotaStore) CheckQuotaAndStore(_ context.Context, j job.Job) (job.Job, error) {
	f.calls++
	f.got = j
	if f.err != nil {
		return job.Job{}, f.err
	}
	j.ID = f.id
	j.State = job.QuotaDeducted
	if f.insufficient {
		j.State = job.QuotaInsufficient
		return j, fmt.Errorf("cost %d, remaining 0: %w", j.Cost, quota.ErrInsufficient)
	}
	return j, nil
}

type fakeDeliverer struct {
	outcome app.DeliveryOutcome
	err     error
	got     job.Job
	doc     []byte
	calls   int
}

func (f *fakeDeliverer) Deliver(_ context.Context, j job.Job, doc []byte) (app.DeliveryOutcome, error) {
	f.calls++
	f.got = j
	f.doc = doc
	return f.outcome, f.err
}

type submitFakes struct {
	analyzer *fakeAnalyzer
	queues   *fakeQueueReader
	jobs     *fakeQuotaStore
	deliver  *fakeDeliverer
}

func newSubmitFakes() *submitFakes {
	return &submitFakes{
		analyzer: &fakeAnalyzer{pages: 10},
		queues:   &fakeQueueReader{q: queue.Queue{ID: 1, Name: "test", Enabled: true, Policy: queue.UniformPolicy}},
		jobs:     &fakeQuotaStore{id: 42},
		deliver:  &fakeDeliverer{outcome: app.DeliverySucceeded},
	}
}

func (f *submitFakes) submitter() *app.Submitter {
	return app.NewSubmitter(f.analyzer, f.queues, f.jobs, f.deliver,
		quota.Rates{ColorRate: testColorRate},
		testMaxBytes, testMaxPages, testMaxCopies, slog.New(slog.DiscardHandler))
}

func validInput() app.SubmitInput {
	return app.SubmitInput{
		Username: "user", QueueID: 1, Document: minimalPDF,
		Copies: 1, Filename: "doc.pdf",
	}
}

// the job the quota check rejected must never reach the printer: it is the
// difference between "you are out of quota" and free printing.
func TestSubmitOverQuotaIsNotDelivered(t *testing.T) {
	f := newSubmitFakes()
	f.jobs.insufficient = true

	res, err := f.submitter().Submit(context.Background(), validInput())

	if !errors.Is(err, quota.ErrInsufficient) {
		t.Fatalf("Submit err = %v, want %v", err, quota.ErrInsufficient)
	}
	if f.deliver.calls != 0 {
		t.Error("BILLING: a job the store recorded as quota_insufficient was sent for delivery")
	}
	// the caller still reports the attempt to the user, so id and cost must survive
	if res.JobID != 42 {
		t.Errorf("JobID = %d, want the id of the rejected row (42)", res.JobID)
	}
	if res.Pages != 10 || res.Cost != 10 {
		t.Errorf("res = %+v, want pages 10 cost 10", res)
	}
	if res.Outcome.String() != "" {
		t.Errorf("Outcome = %q, want no outcome for an undelivered job", res.Outcome)
	}
}

func TestSubmitDeliversTheStoredJob(t *testing.T) {
	f := newSubmitFakes()

	res, err := f.submitter().Submit(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if f.deliver.calls != 1 {
		t.Fatalf("Deliver called %d times, want 1", f.deliver.calls)
	}
	// the job handed to the deliverer must be the one the store returned, not
	// the one Submit built: only the store knows the id and the state.
	if f.deliver.got.ID != 42 {
		t.Errorf("delivered job id = %d, want 42", f.deliver.got.ID)
	}
	if f.deliver.got.State != job.QuotaDeducted {
		t.Errorf("delivered job state = %q, want %q", f.deliver.got.State, job.QuotaDeducted)
	}
	if !bytes.Equal(f.deliver.doc, minimalPDF) {
		t.Error("delivered document differs from the submitted one")
	}
	if res.Outcome != app.DeliverySucceeded || res.JobID != 42 {
		t.Errorf("res = %+v, want the delivered outcome and id 42", res)
	}
}

func TestSubmitStoreFailureIsNotDelivered(t *testing.T) {
	f := newSubmitFakes()
	f.jobs.err = errors.New("db down")

	res, err := f.submitter().Submit(context.Background(), validInput())
	if err == nil {
		t.Fatal("store failure must fail Submit")
	}
	if errors.Is(err, quota.ErrInsufficient) {
		t.Errorf("a database failure must not be reported as insufficient quota: %v", err)
	}
	if f.deliver.calls != 0 {
		t.Error("job delivered despite the store failing")
	}
	if res != (app.SubmitResult{}) {
		t.Errorf("res = %+v, want zero after a store failure", res)
	}
}

// every gate must reject before a row is written, so a refused submission
// never costs quota and never reaches a printer.
func TestSubmitGatesRejectBeforeStoring(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*submitFakes, *app.SubmitInput)
		want  error
	}{
		{"document over size limit", func(_ *submitFakes, in *app.SubmitInput) {
			in.Document = append(minimalPDF, make([]byte, testMaxBytes)...)
		}, app.ErrTooLarge},
		{"not a pdf", func(_ *submitFakes, in *app.SubmitInput) {
			in.Document = []byte("just some bytes")
		}, pdf.ErrNotPDF},
		{"queue disabled", func(f *submitFakes, _ *app.SubmitInput) {
			f.queues.q.Enabled = false
		}, queue.ErrUnavailable},
		{"queue lookup failed", func(f *submitFakes, _ *app.SubmitInput) {
			f.queues.err = errors.New("no such queue")
		}, nil},
		{"analyzer failed", func(f *submitFakes, _ *app.SubmitInput) {
			f.analyzer.err = errors.New("ghostscript died")
		}, nil},
		{"analyzer reports no pages", func(f *submitFakes, _ *app.SubmitInput) {
			f.analyzer.pages = 0
		}, pdf.ErrUnreadable},
		{"analyzer reports more colour pages than pages", func(f *submitFakes, in *app.SubmitInput) {
			f.analyzer.pages, f.analyzer.colorPages = 5, 6
			in.Color = true
		}, pdf.ErrUnreadable},
		{"over page limit", func(f *submitFakes, _ *app.SubmitInput) {
			f.analyzer.pages = testMaxPages + 1
		}, app.ErrTooManyPages},
		{"zero copies", func(_ *submitFakes, in *app.SubmitInput) {
			in.Copies = 0
		}, app.ErrInvalidCopies},
		{"over copy limit", func(_ *submitFakes, in *app.SubmitInput) {
			in.Copies = testMaxCopies + 1
		}, app.ErrInvalidCopies},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSubmitFakes()
			in := validInput()
			tc.setup(f, &in)

			_, err := f.submitter().Submit(context.Background(), in)
			if err == nil {
				t.Fatal("Submit accepted the job, want rejection")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if f.jobs.calls != 0 {
				t.Error("a rejected submission reached the quota store")
			}
			if f.deliver.calls != 0 {
				t.Error("a rejected submission reached the deliverer")
			}
		})
	}
}

func TestSubmitBillsColourOnlyWhenAsked(t *testing.T) {
	const (
		pages  = 10
		colour = 4
		copies = 3
	)
	monoCost := pages * copies
	colourCost := ((pages - colour) + colour*testColorRate) * copies

	for _, tc := range []struct {
		name       string
		color      bool
		wantCost   int
		wantColour int
	}{
		{"colour requested", true, colourCost, colour},
		{"mono requested for a colour document", false, monoCost, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSubmitFakes()
			f.analyzer.pages, f.analyzer.colorPages = pages, colour
			in := validInput()
			in.Color, in.Copies, in.Duplex = tc.color, copies, true

			res, err := f.submitter().Submit(context.Background(), in)
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			if res.Cost != tc.wantCost {
				t.Errorf("cost = %d, want %d", res.Cost, tc.wantCost)
			}
			if f.jobs.got.Cost != tc.wantCost {
				t.Errorf("stored cost = %d, want %d — the user is billed what the store records",
					f.jobs.got.Cost, tc.wantCost)
			}
			if f.jobs.got.NumColorPages != tc.wantColour {
				t.Errorf("stored colour pages = %d, want %d", f.jobs.got.NumColorPages, tc.wantColour)
			}
			// the rest of the input must survive the trip to the store
			stored := f.jobs.got
			if stored.Username != in.Username || stored.QueueID != in.QueueID ||
				stored.DocumentName != in.Filename || stored.Copies != copies ||
				stored.NumPages != pages || stored.Color != tc.color || !stored.Duplex {
				t.Errorf("stored job = %+v, does not match input %+v", stored, in)
			}
		})
	}
}
