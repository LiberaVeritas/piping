package user

import (
	"encoding/json/v2"
	"fmt"
	"strings"
)

type User struct {
	Username      string
	FullName      string
	Email         string
	Role          Role
	QuotaEligible bool
	Enrolments    []int
}

type Role struct{ name string }

func (r Role) String() string { return r.name }

func (r Role) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.name)
}

func (r *Role) UnmarshalJSON(b []byte) error {
	var s string

	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	role, ok := roleFromName(s)
	if !ok {
		return fmt.Errorf("unknown role %q", s)
	}
	*r = role
	return nil
}

func roleFromName(name string) (Role, bool) {
	switch name {
	case "user":
		return RoleUser, true
	case "staff":
		return RoleStaff, true
	case "admin":
		return RoleAdmin, true
	case "":
		return RoleNone, true
	}
	return Role{}, false
}

var (
	RoleNone  = Role{""}
	RoleUser  = Role{"user"}
	RoleStaff = Role{"staff"}
	RoleAdmin = Role{"admin"}
)

const (
	adminGroup = "Org-Admins"
	userGroup  = "Org-Users"
)

var staffGroups = []string{
	"Org-Managers",
	"Org-Staff",
}

const (
	groupAllStudents = "All-Students"
	facultyOfScience = "Faculty of Science"
)

func RoleFromGroups(groups []string) Role {
	if containsFold(groups, adminGroup) {
		return RoleAdmin
	}
	for _, g := range staffGroups {
		if containsFold(groups, g) {
			return RoleStaff
		}
	}
	if containsFold(groups, userGroup) {
		return RoleUser
	}
	return RoleNone
}

func RoleRank(role Role) int {
	switch role {
	case RoleNone:
		return 0
	case RoleUser:
		return 1
	case RoleStaff:
		return 2
	case RoleAdmin:
		return 3
	}
	return 0
}

func containsFold(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}

func EligibleForQuota(faculty string, groups []string) bool {
	if strings.EqualFold(faculty, facultyOfScience) && containsFold(groups, groupAllStudents) {
		return true
	}
	if RoleFromGroups(groups) != RoleNone {
		return true
	}
	return false
}
