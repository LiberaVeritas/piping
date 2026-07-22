package user

import (
	"encoding/json/v2"
	"testing"
)

func TestRoleFromGroups(t *testing.T) {
	cases := []struct {
		name   string
		groups []string
		want   Role
	}{
		{"admin", []string{"520-Infopoint Admins"}, RoleAdmin},
		{"staff", []string{"520-CTF Members"}, RoleStaff},
		{"staff probationary", []string{"520-CTF Probationary Members"}, RoleStaff},
		{"user", []string{"520-Infopoint Users"}, RoleUser},
		{"admin outranks staff", []string{"520-CTF Members", "520-Infopoint Admins"}, RoleAdmin},
		{"case insensitive", []string{"520-infopoint admins"}, RoleAdmin},
		{"ordinary student", []string{"000-All Students"}, RoleNone},
		{"empty", nil, RoleNone},
	}
	for _, c := range cases {
		if got := RoleFromGroups(c.groups); got != c.want {
			t.Errorf("%s: RoleFromGroups(%v) = %v, want %v", c.name, c.groups, got, c.want)
		}
	}
}

func TestEligibleForQuota(t *testing.T) {
	cases := []struct {
		name    string
		faculty string
		groups  []string
		want    bool
	}{
		{"science student", "Faculty of Science", []string{"000-All Students"}, true},
		{"interfaculty", "Interfaculty, B.A. & Sc.", []string{"000-Arts_Sci-Undergrads"}, true},
		{"arts student", "Faculty of Arts", []string{"000-All Students"}, false},
		{"science without membership", "Faculty of Science", nil, false},
		{"staff always eligible", "Faculty of Arts", []string{"520-CTF Members"}, true},
		{"admin always eligible", "", []string{"520-Infopoint Admins"}, true},
	}
	for _, c := range cases {
		if got := EligibleForQuota(c.faculty, c.groups); got != c.want {
			t.Errorf("%s: EligibleForQuota(%q, %v) = %v, want %v", c.name, c.faculty, c.groups, got, c.want)
		}
	}
}

// Role must survive JSON round trips — INCLUDING RoleNone, which is what every
// ordinary student carries. A RoleNone that cannot unmarshal is an infinite
// login-redirect loop (issue cookie -> open fails -> re-login -> repeat).
func TestRoleJSONRoundTrip(t *testing.T) {
	for _, r := range []Role{RoleNone, RoleUser, RoleStaff, RoleAdmin} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal %v: %v", r, err)
		}
		var back Role
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		if back != r {
			t.Errorf("round trip: %v -> %s -> %v", r, b, back)
		}
	}
}

func TestRoleUnknownRejected(t *testing.T) {
	var r Role
	if err := json.Unmarshal([]byte(`"superadmin"`), &r); err == nil {
		t.Fatal("unknown role name must fail to unmarshal (schema-drift self-heal via re-login)")
	}
}

// Nested marshaling: the encoder must invoke Role's methods inside a struct.
func TestRoleInsideStruct(t *testing.T) {
	type wrapper struct {
		Role Role `json:"role"`
	}
	b, err := json.Marshal(wrapper{Role: RoleStaff})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"role":"staff"}` {
		t.Errorf("nested marshal = %s", b)
	}
	var w wrapper
	if err := json.Unmarshal([]byte(`{"role":"admin"}`), &w); err != nil {
		t.Fatal(err)
	}
	if w.Role != RoleAdmin {
		t.Errorf("nested unmarshal = %v", w.Role)
	}
}

func TestCTFGroups(t *testing.T) {

	if got := CTFGroups([]string{"000-All Students", "520-CTF Members", "520-Infopoint Users"}); len(got) != 2 {
		t.Errorf("CTFGroups = %v", got)
	}
}
