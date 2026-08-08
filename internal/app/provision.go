package app

import (
	"context"
	"fmt"
	"log/slog"

	"piping/internal/user"
)

type provisionStore interface {
	EnsureSemester(ctx context.Context, id int, defaultQuota int) (int, error)
	EnsureGrant(ctx context.Context, username string, semesterID, amount int) error
	EnsureUser(ctx context.Context, username string) error
}

type Provisioner struct {
	store        provisionStore
	defaultQuota int
	log          *slog.Logger
}

func NewProvisioner(store provisionStore, defaultQuota int, log *slog.Logger) *Provisioner {
	return &Provisioner{store: store, defaultQuota: defaultQuota, log: log}
}

// eligibility for quota is based on group membership and faculty attr:
// faculty of science, or interfaculty ArtSci
// past semesters are migrated from tepid
func (p *Provisioner) Provision(ctx context.Context, user user.User, currentSemesterCode int) error {

	if err := p.store.EnsureUser(ctx, user.Username); err != nil {
		return fmt.Errorf("provision: ensure user %q semester %d: %w", user.Username, currentSemesterCode, err)
	}
	entitled := map[int]bool{}
	if user.QuotaEligible {
		entitled[currentSemesterCode] = true
	}
	for _, code := range user.Enrolments {
		if code <= currentSemesterCode {
			entitled[code] = true
		}
	}
	for code := range entitled {
		amount, err := p.store.EnsureSemester(ctx, code, p.QuotaFor(code))
		if err != nil {
			p.log.Error("provision: ensure semester", "user", user.Username, "semester", code, "err", err)
			continue
		}
		if err := p.store.EnsureGrant(ctx, user.Username, code, amount); err != nil {
			p.log.Error("provision: create grant", "user", user.Username, "semester", code, "err", err)
		}
	}
	return nil
}

func (p *Provisioner) QuotaFor(semesterCode int) int {
	if semesterCode%100 == 5 {
		// summers give zero quota
		return 0
	} else if semesterCode < 201609 {
		// tepid didn't exist before fall 2016
		return 0
	} else if semesterCode == 201609 {
		// first semester was 500 pages
		return 500
	} else if 201609 < semesterCode && semesterCode < 201909 {
		// these gave 1000 pages
		return 1000
	}
	return p.defaultQuota
}
