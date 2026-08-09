package queue

import "testing"

func TestPolicyStringInverse(t *testing.T) {
	for _, p := range allPolicies {
		back, err := PolicyFromString(p.String())
		if err != nil || back != p {
			t.Errorf("PolicyFromString(String(%v)) = %v, %v", p, back, err)
		}
	}
}

// a queue row carrying a policy this build does not know must fail loudly:
// silently falling back to a balancer would send jobs somewhere unintended.
func TestPolicyFromStringRejectsUnknown(t *testing.T) {
	for _, s := range []string{"", "Uniform", "uniform ", "roundrobin", "unknown"} {
		got, err := PolicyFromString(s)
		if err == nil {
			t.Errorf("PolicyFromString(%q) = %v, want an error", s, got)
		}
		if got != UnknownPolicy {
			t.Errorf("PolicyFromString(%q) = %v, want UnknownPolicy on failure", s, got)
		}
	}
}

// every policy the parser accepts needs a balancer behind it, or a queue can
// be configured into a state that fails only at delivery time.
func TestEveryPolicyHasALoadBalancer(t *testing.T) {
	for _, p := range allPolicies {
		lb, err := LoadBalancerFromPolicy(p)
		if err != nil || lb == nil {
			t.Errorf("LoadBalancerFromPolicy(%v) = %v, %v", p, lb, err)
		}
	}
	if _, err := LoadBalancerFromPolicy(UnknownPolicy); err == nil {
		t.Error("LoadBalancerFromPolicy(UnknownPolicy) succeeded, want an error")
	}
}

func TestEnabledDestinations(t *testing.T) {
	a := Destination{ID: 1, Name: "a", Enabled: true}
	b := Destination{ID: 2, Name: "b"}
	c := Destination{ID: 3, Name: "c", Enabled: true}

	cases := []struct {
		name string
		in   []Destination
		want []int64
	}{
		{"filters disabled and keeps order", []Destination{a, b, c}, []int64{1, 3}},
		{"all enabled", []Destination{a, c}, []int64{1, 3}},
		{"all disabled", []Destination{b}, nil},
		{"empty", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EnabledDestinations(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want ids %v", got, tc.want)
			}
			for i, d := range got {
				if d.ID != tc.want[i] {
					t.Errorf("position %d is id %d, want %d", i, d.ID, tc.want[i])
				}
				if !d.Enabled {
					t.Errorf("id %d is disabled but was returned", d.ID)
				}
			}
		})
	}
}

func TestUniformChooseReturnsOneOfTheDestinations(t *testing.T) {
	dests := []Destination{{ID: 1}, {ID: 2}, {ID: 3}}
	lb := uniformBalancer{}
	for range 100 {
		got, err := lb.Choose(dests)
		if err != nil {
			t.Fatalf("Choose: %v", err)
		}
		if got.ID < 1 || got.ID > 3 {
			t.Fatalf("Choose returned %+v, which is not one of the inputs", got)
		}
	}
}

func TestUniformChooseRejectsEmpty(t *testing.T) {
	if got, err := (uniformBalancer{}).Choose(nil); err == nil {
		t.Errorf("Choose(nil) = %+v, want an error rather than a zero destination", got)
	}
}
