package queue

import (
	"errors"
	"fmt"
	"math/rand/v2"
)

var ErrUnavailable = errors.New("queue unavailable")

type Queue struct {
	ID      int64
	Name    string
	Enabled bool
	Policy  LoadBalancerPolicy
}

type Destination struct {
	ID      int64
	QueueID int64
	Address string
	Name    string
	Enabled bool
}

func EnabledDestinations(dests []Destination) []Destination {
	var out []Destination
	for _, d := range dests {
		if d.Enabled {
			out = append(out, d)
		}
	}
	return out
}

type LoadBalancerPolicy struct{ name string }

func (p LoadBalancerPolicy) String() string { return p.name }

var (
	UnknownPolicy = LoadBalancerPolicy{""}
	UniformPolicy = LoadBalancerPolicy{"uniform"}
)

var allPolicies = []LoadBalancerPolicy{UniformPolicy}

func PolicyFromString(s string) (LoadBalancerPolicy, error) {
	for _, p := range allPolicies {
		if p.name == s {
			return p, nil
		}
	}
	return UnknownPolicy, fmt.Errorf("unknown load balancer policy %q", s)
}

type LoadBalancer interface {
	Choose(dests []Destination) (Destination, error)
}

func LoadBalancerFromPolicy(p LoadBalancerPolicy) (LoadBalancer, error) {
	switch p {
	case UniformPolicy:
		return uniformBalancer{}, nil
	}
	return nil, fmt.Errorf("no load balancer for policy %q", p)
}

type uniformBalancer struct{}

func (uniformBalancer) Choose(dests []Destination) (Destination, error) {
	if len(dests) == 0 {
		return Destination{}, errors.New("no destinations to choose from")
	}
	return dests[rand.IntN(len(dests))], nil
}
