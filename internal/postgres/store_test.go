package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"pgregory.net/rapid"

	"piping/internal/job"
	"piping/internal/quota"
)

const schemaPath = "../../schema.sql"

var (
	testDBURL string
	dbSkipMsg string
)

func seedJob(t *testing.T, s *Store, username string, queueID int64,
	destID *int64, state job.State, pages, cost int) int64 {
	t.Helper()
	var id int64
	err := s.pool.QueryRow(context.Background(), `
		INSERT INTO job (user_id, queue_id, destination_id, state, num_pages,
		                 num_color_pages, copies, cost, color, duplex, document_name)
		VALUES ($1, $2, $3, $4::job_state, $5, 0, 1, $6, false, true, 'seed.pdf')
		RETURNING id`,
		username, queueID, destID, state.String(), pages, cost).Scan(&id)
	if err != nil {
		t.Fatalf("seeding job: %v", err)
	}
	return id
}

func seedDestination(t *testing.T, s *Store, queueID int64, name string) int64 {
	t.Helper()
	var id int64
	err := s.pool.QueryRow(context.Background(), `
		INSERT INTO destination (queue_id, address, name, enabled)
		VALUES ($1, 'smb://x/y', $2, true) RETURNING id`, queueID, name).Scan(&id)
	if err != nil {
		t.Fatalf("seeding destination: %v", err)
	}
	return id
}

func TestJobWithDestinationForUserScans(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testLogger())
	ctx := context.Background()

	const username = "scanuser"
	queueID := seedUser(t, s, username, 1000)
	destID := seedDestination(t, s, queueID, "Trottier 3rd")

	seedJob(t, s, username, queueID, &destID, job.PrintSucceeded, 12, 12)
	seedJob(t, s, username, queueID, nil, job.QuotaInsufficient, 5, 5)

	jobs, err := s.JobsWithDestinationForUser(ctx, username, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("JobWithDestinationForUser: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d rows, want 2", len(jobs))
	}

	var withDest, withoutDest int
	for _, v := range jobs {
		switch v.State {
		case job.PrintSucceeded:
			withDest++
			if v.NumPages != 12 || v.Cost != 12 {
				t.Errorf("succeeded row: pages=%d cost=%d, want 12/12 (column order?)", v.NumPages, v.Cost)
			}
			if v.DestinationName != "Trottier 3rd" {
				t.Errorf("destination = %q, want %q", v.DestinationName, "Trottier 3rd")
			}
		case job.QuotaInsufficient:
			withoutDest++
			if v.DestinationName != "" {
				t.Errorf("NULL destination scanned as %q, want empty string", v.DestinationName)
			}
		default:
			t.Errorf("unexpected state %q", v.State)
		}
		if v.DocumentName != "seed.pdf" {
			t.Errorf("document_name = %q", v.DocumentName)
		}
		if v.SubmittedAt.IsZero() {
			t.Error("submitted_at scanned as zero")
		}
	}
	if withDest != 1 || withoutDest != 1 {
		t.Errorf("got %d with / %d without destination, want 1/1", withDest, withoutDest)
	}
}

func TestJobsForUserScans(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testLogger())
	ctx := context.Background()

	const username = "scanuser2"
	queueID := seedUser(t, s, username, 1000)
	id := seedJob(t, s, username, queueID, nil, job.QuotaDeducted, 7, 7)

	jobs, err := s.JobsForUser(ctx, username, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	j := jobs[0]
	if j.ID != id || j.NumPages != 7 || j.Cost != 7 || j.State != job.QuotaDeducted {
		t.Errorf("scanned %+v", j)
	}
	if j.DestinationID != nil {
		t.Errorf("NULL destination_id scanned as %v", *j.DestinationID)
	}
}

func TestStaleJobsScans(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testLogger())
	ctx := context.Background()

	const username = "scanuser3"
	queueID := seedUser(t, s, username, 1000)
	id := seedJob(t, s, username, queueID, nil, job.PrintSent, 3, 3)
	if _, err := s.pool.Exec(ctx,
		`UPDATE job SET submitted_at = now() - interval '1 hour' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	stale, err := s.StaleJobs(ctx, []job.State{job.QuotaDeducted, job.PrintSent}, time.Now().Add(-time.Minute), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].ID != id || stale[0].State != job.PrintSent {
		t.Fatalf("stale = %+v, want the backdated print_sent job", stale)
	}
}

func TestMain(m *testing.M) {
	code := func() int {
		ctx := context.Background()

		url, ok := os.LookupEnv("TEST_DATABASE_URL")
		if !ok || url == "" {
			fmt.Fprintf(os.Stderr, "TEST_DATABASE_URL env var not set")
			return 1
		}
		testDBURL = url
		if err := loadSchema(ctx, url); err != nil {
			fmt.Fprintf(os.Stderr, "loading schema into TEST_DATABASE_URL %q: %v\n", url, err)
			return 1
		}
		return m.Run()
	}()
	os.Exit(code)
}

func loadSchema(ctx context.Context, url string) error {
	ddl, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", schemaPath, err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		return fmt.Errorf("resetting schema: %w", err)
	}
	if _, err := pool.Exec(ctx, string(ddl)); err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}
	return nil
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBURL == "" {
		t.Skipf("SKIPPING DATABASE TEST: %s", dbSkipMsg)
	}
	pool, err := pgxpool.New(context.Background(), testDBURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedUser(t *testing.T, s *Store, username string, grant int) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `TRUNCATE job, semester_grant, app_user, semester, destination, queue CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureUser(ctx, username); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureSemester(ctx, 202609, grant); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureGrant(ctx, username, 202609, grant); err != nil {
		t.Fatal(err)
	}
	var queueID int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO queue (name, enabled, policy) VALUES ('test', true, 'uniform') RETURNING id`).
		Scan(&queueID)
	if err != nil {
		t.Fatal(err)
	}
	return queueID
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestRejectsJobsWithInsufficientQuota(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testLogger())
	ctx := context.Background()

	const (
		username = "insuffuser"
		grant    = 10
		cost     = 11
	)

	queueID := seedUser(t, s, username, grant)

	j, err := s.CheckQuotaAndStore(ctx, job.Job{
		Username: username, QueueID: queueID,
		NumPages: cost, NumColorPages: 0, Copies: 1, Cost: cost,
		DocumentName: "insufficient.pdf",
	})
	if !errors.Is(err, quota.ErrInsufficient) {
		t.Errorf("unexpected error; got: %v+ want: %v+", err, quota.ErrInsufficient)
	}
	if j.ID == 0 {
		t.Error("got job id of 0")
	}
	if j.State != job.QuotaInsufficient {
		t.Errorf("unexpected job state; got: %q want: %s", j.State, job.QuotaInsufficient)
	}
	remaining, err := s.RemainingQuota(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != grant {
		t.Errorf("job not printed but quota changed; got: %d want: %d", remaining, grant)
	}
}

func TestConcurrentSubmitCannotOverspend(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testLogger())
	ctx := context.Background()

	const (
		username   = "raceuser"
		grant      = 40
		cost       = 8
		attempts   = 20
		wantAccept = grant / cost
	)
	queueID := seedUser(t, s, username, grant)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
		rejected int
		other    []error
	)
	start := make(chan struct{})

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.CheckQuotaAndStore(ctx, job.Job{
				Username: username, QueueID: queueID,
				NumPages: cost, NumColorPages: 0, Copies: 1, Cost: cost,
				DocumentName: "race.pdf",
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, quota.ErrInsufficient):
				rejected++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	for _, err := range other {
		t.Errorf("unexpected error: %v", err)
	}
	if accepted != wantAccept {
		t.Errorf("accepted %d submissions, want exactly %d (grant %d / cost %d)",
			accepted, wantAccept, grant, cost)
	}
	if accepted+rejected != attempts {
		t.Errorf("accepted %d + rejected %d != %d attempts", accepted, rejected, attempts)
	}

	remaining, err := s.RemainingQuota(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	if remaining < 0 {
		t.Fatalf("OVER-SPEND: remaining quota is %d", remaining)
	}
	if want := grant - wantAccept*cost; remaining != want {
		t.Errorf("remaining %d, want %d", remaining, want)
	}
}

func TestConcurrentSubmitDifferentUsersDoNotBlockEachOther(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testLogger())
	ctx := context.Background()

	queueID := seedUser(t, s, "userA", 100)
	if err := s.EnsureUser(ctx, "userB"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureGrant(ctx, "userB", 202609, 100); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 10; i++ {
		for _, u := range []string{"userA", "userB"} {
			wg.Add(1)
			go func(username string) {
				defer wg.Done()
				_, err := s.CheckQuotaAndStore(ctx, job.Job{
					Username: username, QueueID: queueID,
					NumPages: 5, Copies: 1, Cost: 5, DocumentName: "x.pdf",
				})
				if err != nil {
					errs <- err
				}
			}(u)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("submission failed: %v", err)
	}
	for _, u := range []string{"userA", "userB"} {
		remaining, err := s.RemainingQuota(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		if remaining != 50 {
			t.Errorf("%s remaining %d, want 50", u, remaining)
		}
	}
}

func TestConcurrentStateTransitionExactlyOneWins(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testLogger())
	ctx := context.Background()

	queueID := seedUser(t, s, "stateuser", 100)
	created, err := s.CheckQuotaAndStore(ctx, job.Job{
		Username: "stateuser", QueueID: queueID,
		NumPages: 5, Copies: 1, Cost: 5, DocumentName: "x.pdf",
	})
	if err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var wg sync.WaitGroup
	results := make(chan error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.UpdateJobState(ctx, created.ID, job.QuotaDeducted, job.PrintFailed)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var won, lost int
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, job.ErrUnexpectedState):
			lost++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Errorf("%d racers won the transition, want exactly 1", won)
	}
	if lost != racers-1 {
		t.Errorf("%d racers lost, want %d", lost, racers-1)
	}
}

func TestStoreQuotaStateMachine(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		if _, err := pool.Exec(ctx, `TRUNCATE job, semester_grant, app_user, semester CASCADE`); err != nil {
			rt.Fatal(err)
		}
		store := New(pool, nil)
		const user = "propuser"
		if err := store.EnsureUser(ctx, user); err != nil {
			rt.Fatal(err)
		}

		granted := 0
		spent := 0
		jobs := map[int64]job.Job{}
		nextSem := 200001

		rt.Repeat(map[string]func(*rapid.T){
			"grant": func(rt *rapid.T) {
				amt := rapid.IntRange(0, 50).Draw(rt, "amt")
				if _, err := store.EnsureSemester(ctx, nextSem, amt); err != nil {
					rt.Fatal(err)
				}
				if err := store.EnsureGrant(ctx, user, nextSem, amt); err != nil {
					rt.Fatal(err)
				}
				granted += amt
				nextSem++
			},
			"submit": func(rt *rapid.T) {
				cost := rapid.IntRange(1, 30).Draw(rt, "cost")
				j, err := store.CheckQuotaAndStore(ctx, job.Job{
					Username: user, QueueID: mustQueue(rt, ctx, pool), Cost: cost,
					NumPages: cost, Copies: 1, DocumentName: "x.pdf",
				})
				switch {
				case err == nil:
					if granted-spent < cost {
						rt.Fatalf("over-spend: accepted cost %d with remaining %d", cost, granted-spent)
					}
					spent += cost
					jobs[j.ID] = j
				case errors.Is(err, quota.ErrInsufficient):
					if granted-spent >= cost {
						rt.Fatalf("wrongly rejected cost %d with remaining %d", cost, granted-spent)
					}
				default:
					rt.Fatal(err)
				}
			},
			"resolve": func(rt *rapid.T) {
				if len(jobs) == 0 {
					return
				}
				var ids []int64
				for id := range jobs {
					ids = append(ids, id)
				}
				id := rapid.SampledFrom(ids).Draw(rt, "job")
				j := jobs[id]
				to := rapid.SampledFrom([]job.State{job.PrintSent, job.PrintFailed}).Draw(rt, "to")
				err := store.UpdateJobState(ctx, id, job.QuotaDeducted, to)
				if j.State != job.QuotaDeducted {
					if !errors.Is(err, job.ErrUnexpectedState) {
						rt.Fatalf("guarded update let %v pass as quota_deducted", j.State)
					}
					return
				}
				if err != nil {
					rt.Fatal(err)
				}
				if to == job.PrintFailed {
					spent -= j.Cost
				}
				j.State = to
				jobs[id] = j
			},
			"": func(rt *rapid.T) { // invariant after every action
				remaining, err := store.RemainingQuota(ctx, user)
				if err != nil {
					rt.Fatal(err)
				}
				if want := granted - spent; remaining != want {
					rt.Fatalf("derived quota %d, want %d (granted %d spent %d)",
						remaining, want, granted, spent)
				}
				if remaining < 0 {
					rt.Fatalf("quota went negative: %d", remaining)
				}
			},
		})
	})
}

var queueID int64

func mustQueue(rt *rapid.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	if queueID != 0 {
		return queueID
	}
	err := pool.QueryRow(ctx,
		`INSERT INTO queue (name, enabled, policy) VALUES ('t', true, 'uniform') RETURNING id`).
		Scan(&queueID)
	if err != nil {
		rt.Fatal(err)
	}
	return queueID
}
