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
	err := json.Unmarshal(b, &s)
	if err != nil {
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
	adminGroup = "520-Infopoint Admins"
	userGroup  = "520-Infopoint Users"
)

var staffGroups = []string{
	"520-CTF Members",
	"520-CTF Contributors",
	"520-CTF Probationary Members",
}

// 000-All Current Term Students,OU=Campus Groups,OU=Network & Communications Services,OU=University Administration,DC=campus,DC=MCGILL,DC=CA
// 000-All Students,OU=Campus Groups,OU=Network & Communications Services,OU=University Administration,DC=campus,DC=MCGILL,DC=CA
// 000-All Undergrad Students,OU=Campus Groups,OU=Network & Communications Services,OU=University Administration,DC=campus,DC=MCGILL,DC=CA
// 000-Science-Undergrads,OU=Student Groups,OU=STUDENTS,DC=campus,DC=CGILL,DC=CA
// 000-All-Returning-Students,OU=Student Groups,OU=STUDENTS,DC=campus,DC=MCGILL,DC=CA
const (
	groupAllStudents          = "000-All Students"
	groupScienceUndergrads    = "000-Science-Undergrads"
	groupArtSciUndergrads     = "000-Arts_Sci-Undergrads"
	groupAllUndergrads        = "000-All Undergrad Students"
	groupAllReturningStudents = "000-All-Returning-Students"
	groupCurrentTermStudents  = "000-All Current Term Students"
)

const (
	facultyOfScience   = "Faculty of Science"
	interfacultyArtSci = "Interfaculty, B.A. & Sc."
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
	if strings.EqualFold(faculty, interfacultyArtSci) && containsFold(groups, groupArtSciUndergrads) {
		return true
	}
	if RoleFromGroups(groups) != RoleNone {
		return true
	}
	return false
}

func CTFGroups(groups []string) []string {
	var out []string
	for _, g := range groups {
		if strings.HasPrefix(g, "520-") {
			out = append(out, g)
		}
	}
	return out
}
