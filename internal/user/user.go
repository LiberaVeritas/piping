package user

import (
	"strings"
)

type Role struct{ name string }

func (r Role) String() string { return r.name }

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

func EligibleForQuota(department string, groups []string) bool {
	if strings.EqualFold(department, facultyOfScience) && containsFold(groups, groupAllStudents) {
		return true
	}
	if strings.EqualFold(department, interfacultyArtSci) && containsFold(groups, groupArtSciUndergrads) {
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

type User struct {
	Username  string
	FullName  string
	Email     string
	Faculty   string
	Groups    []string
	Semesters []int
}
