package app

import (
	"context"
	"log/slog"

	"piping/internal/user"
)

type provisionStore interface {
	ListGrantSemesters(ctx context.Context, username string) ([]int, error)
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
func (p *Provisioner) Provision(ctx context.Context, user user.User, currentSemester int) error {
	err := p.store.EnsureUser(ctx, user.Username)
	if err != nil {
		return err
	}
	entitled := map[int]bool{}
	if user.QuotaEligible {
		entitled[currentSemester] = true
	}
	for _, code := range user.Enrolments {
		entitled[code] = true
	}
	for code := range entitled {
		amount, err := p.store.EnsureSemester(ctx, code, p.quotaFor(code))
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

// TODO
// summers get zero quota?
func (p *Provisioner) quotaFor(semesterCode int) int {
	if semesterCode%100 == 5 {
		return 0
	}
	return p.defaultQuota
}
