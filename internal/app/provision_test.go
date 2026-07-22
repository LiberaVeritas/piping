package app_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"pgregory.net/rapid"

	"piping/internal/app"
	"piping/internal/user"
)

type fakeProvStore struct {
	users     []string
	semesters map[int]int
	grants    map[int]int
	userErr   error
	grantErr  map[int]error
}

func newFakeProvStore() *fakeProvStore {
	return &fakeProvStore{semesters: map[int]int{}, grants: map[int]int{}, grantErr: map[int]error{}}
}

func (f *fakeProvStore) EnsureUser(_ context.Context, u string) error {
	if f.userErr != nil {
		return f.userErr
	}
	f.users = append(f.users, u)
	return nil
}
func (f *fakeProvStore) EnsureSemester(_ context.Context, id, dq int) (int, error) {
	f.semesters[id] = dq
	return dq, nil
}
func (f *fakeProvStore) EnsureGrant(_ context.Context, u string, sem, amt int) error {
	if err := f.grantErr[sem]; err != nil {
		return err
	}
	f.grants[sem] = amt
	return nil
}
func (f *fakeProvStore) ListGrantSemesters(_ context.Context, u string) ([]int, error) {
	return nil, nil
}

func prov(f *fakeProvStore) *app.Provisioner {
	return app.NewProvisioner(f, 250, slog.New(slog.DiscardHandler))
}

func semesterCodeGen() *rapid.Generator[int] {
	return rapid.Custom(func(rt *rapid.T) int {
		year := rapid.IntRange(2000, 2100).Draw(rt, "year")
		month := rapid.SampledFrom([]int{1, 5, 9}).Draw(rt, "month")
		return year*100 + month
	})
}

func TestGrantedEqualsEntitled(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		eligible := rapid.Bool().Draw(rt, "eligible")
		enrolments := rapid.SliceOfN(semesterCodeGen(), 0, 8).Draw(rt, "enrolments")
		current := semesterCodeGen().Draw(rt, "current")

		f := newFakeProvStore()
		p := app.NewProvisioner(f, 250, slog.New(slog.DiscardHandler))
		u := user.User{Username: "testuser", QuotaEligible: eligible, Enrolments: enrolments}
		if err := p.Provision(context.Background(), u, current); err != nil {
			rt.Fatal(err)
		}

		want := map[int]bool{}
		if eligible {
			want[current] = true
		}
		for _, c := range enrolments {
			want[c] = true
		}

		if len(f.grants) != len(want) {
			rt.Fatalf("granted %v, want keys %v", f.grants, want)
		}
		for code := range want {
			amt, ok := f.grants[code]
			if !ok {
				rt.Fatalf("missing grant for %d: %v", code, f.grants)
			}
			wantAmt := 250
			if code%100 == 5 {
				wantAmt = 0
			}
			if amt != wantAmt {
				rt.Fatalf("grant %d amount %d, want %d", code, amt, wantAmt)
			}
		}
		if len(f.users) != 1 || f.users[0] != "testuser" {
			rt.Fatalf("EnsureUser calls: %v", f.users)
		}
	})
}

func TestEnrolmentEqualToCurrentNotDoubled(t *testing.T) {
	f := newFakeProvStore()
	u := user.User{Username: "x", QuotaEligible: true, Enrolments: []int{202509}}
	if err := prov(f).Provision(context.Background(), u, 202509); err != nil {
		t.Fatal(err)
	}
	if len(f.grants) != 1 {
		t.Errorf("current-also-enrolled must yield one grant, got %v", f.grants)
	}
}

func TestEnsureUserFailureIsHard(t *testing.T) {
	f := newFakeProvStore()
	f.userErr = errors.New("db down")
	u := user.User{Username: "x", QuotaEligible: true}
	if err := prov(f).Provision(context.Background(), u, 202509); err == nil {
		t.Fatal("EnsureUser failure must fail Provision")
	}
	if len(f.grants) != 0 {
		t.Error("no grants should be attempted after EnsureUser failure")
	}
}

func TestGrantFailureIsSoftOthersProceed(t *testing.T) {
	f := newFakeProvStore()
	f.grantErr[202409] = errors.New("boom")
	u := user.User{Username: "x", QuotaEligible: true, Enrolments: []int{202409}}
	if err := prov(f).Provision(context.Background(), u, 202509); err != nil {
		t.Fatalf("grant failure must not fail Provision: %v", err)
	}
	if _, ok := f.grants[202509]; !ok {
		t.Error("other grants must proceed past a failed one")
	}
}
