package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"piping/internal/directory"
	"piping/internal/quota"
	"piping/internal/user"
)

type looker interface {
	Lookup(ctx context.Context, username string) (user.User, error)
}

type provisionStore interface {
	GrantExists(ctx context.Context, username string, semesterID int) (bool, error)
	ListGrantSemesters(ctx context.Context, username string) ([]int, error)
	EnsureSemester(ctx context.Context, id int, defaultQuota int) (int, error)
	CreateGrant(ctx context.Context, username string, semesterID, amount int) error
}

type Provisioner struct {
	look         looker
	store        provisionStore
	defaultQuota int
	log          *slog.Logger
}

func NewProvisioner(look looker, store provisionStore, defaultQuota int, log *slog.Logger) *Provisioner {
	return &Provisioner{look: look, store: store, defaultQuota: defaultQuota, log: log}
}

// eligibility for quota is based on group membership and faculty attr:
// faculty of science, or interfaculty ArtSci
// past semesters are migrated from tepid
// current semesters are granted if eligible
func (p *Provisioner) EnsureEntitledGrants(ctx context.Context, username string, currentSemesterCode int) error {
	exists, err := p.store.GrantExists(ctx, username, currentSemesterCode)
	if err != nil {
		return fmt.Errorf("checking grant for %s semester %d: %w", username, currentSemesterCode, err)
	}
	if exists {
		return nil
	}
	p.ReconcileGrants(ctx, username, currentSemesterCode)
	return nil
}

// grant all eligible semesters up to current
func (p *Provisioner) ReconcileGrants(ctx context.Context, username string, currentSemesterCode int) {
	u, err := p.look.Lookup(ctx, username)
	if err != nil {
		if errors.Is(err, directory.ErrUserNotFound) {
			p.log.Warn("provisioning: user not in directory", "user", username)
			return
		}
		p.log.Error("provisioning: directory lookup failed", "user", username, "err", err)
		return
	}
	if !user.EligibleForQuota(u.Faculty, u.Groups) {
		p.log.Info("provisioning: user not quota-eligible", "user", username, "department", u.Faculty)
		return
	}

	held := map[int]bool{}
	heldSemesters, err := p.store.ListGrantSemesters(ctx, username)
	if err != nil {
		p.log.Error("provisioning: failed to get user's grants", "user", username, "err", err)
		heldSemesters = []int{} // granting is idempotent anyway
	}
	for _, s := range heldSemesters {
		held[s] = true
	}

	entitled := map[int]bool{currentSemesterCode: true}
	for _, code := range u.Semesters {
		entitled[code] = true
	}
	for code := range entitled {
		if held[code] {
			continue
		}
		amount, err := p.store.EnsureSemester(ctx, code, p.quotaFor(code))
		if err != nil {
			p.log.Error("provisioning: ensuring semester", "semester", code, "err", err)
			continue
		}
		err = p.store.CreateGrant(ctx, username, code, amount)
		if err != nil {
			if errors.Is(err, quota.ErrGrantExists) {
				continue
			}
			p.log.Error("provisioning: creating grant", "user", username, "semester", code, "err", err)
			continue
		}
		p.log.Info("provisioning: granted semester quota",
			"user", username, "semester", code, "amount", amount)
	}
}

// TODO
// summers get zero quota?
func (p *Provisioner) quotaFor(semesterCode int) int {
	if semesterCode%100 == 5 {
		return 0
	}
	return p.defaultQuota
}

func SemesterCode(s string) (int, error) {
	season, year, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok {
		return 0, fmt.Errorf("parsing semester %s", s)
	}
	var month int
	switch season {
	case "Winter":
		month = 1
	case "Summer":
		month = 5
	case "Fall":
		month = 9
	default:
		return 0, fmt.Errorf("unknown season %q", season)
	}
	y, err := strconv.Atoi(year)
	if err != nil {
		return 0, fmt.Errorf("converting to string %s: %w", year, err)
	}
	return y*100 + month, nil
}
