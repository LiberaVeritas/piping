package job

import "testing"

func outgoing(s State) []State { return validTransitions[s] }

func TestStateStringInverse(t *testing.T) {
	for _, s := range allStates {
		back, err := StateFromString(s.String())
		if err != nil || back != s {
			t.Errorf("StateFromString(String(%v)) = %v, %v", s, back, err)
		}
	}
}

func TestDeductingStateNamesMatchMethod(t *testing.T) {
	names := map[string]bool{}
	for _, n := range QuotaDeductingStateNames() {
		names[n] = true
	}
	for _, s := range allStates {
		if s.DeductsQuota() != names[s.String()] {
			t.Errorf("%v: DeductsQuota()=%v but names list says %v",
				s, s.DeductsQuota(), names[s.String()])
		}
	}
}

func TestTerminalityMatchesGraph(t *testing.T) {
	for _, s := range allStates {
		out := outgoing(s)
		if !IsTerminal(s) && len(out) == 0 {
			t.Errorf("non-terminal %v has no outgoing transitions (stuck state)", s)
		}
		if IsTerminal(s) {
			for _, to := range out {
				if to != Refunded {
					t.Errorf("terminal %v may only exit to Refunded, has edge to %v", s, to)
				}
			}
		}
	}
}

func TestOnlyDeductedReachesSend(t *testing.T) {
	for _, from := range allStates {
		for _, to := range outgoing(from) {
			if to == PrintSent && from != QuotaDeducted {
				t.Errorf("edge %v -> PrintSent violates deduct-before-send", from)
			}
		}
	}
}

func TestQuotaNeverRedeductsViaTransition(t *testing.T) {
	for _, from := range allStates {
		for _, to := range outgoing(from) {
			if !from.DeductsQuota() && to.DeductsQuota() {
				t.Errorf("edge %v -> %v re-deducts quota via transition", from, to)
			}
		}
	}
}

func TestAllStatesReachableFromInitial(t *testing.T) {
	seen := map[State]bool{QuotaDeducted: true}
	frontier := []State{QuotaDeducted}
	for len(frontier) > 0 {
		s := frontier[0]
		frontier = frontier[1:]
		for _, to := range outgoing(s) {
			if !seen[to] {
				seen[to] = true
				frontier = append(frontier, to)
			}
		}
	}
	for _, s := range allStates {
		if s == QuotaInsufficient {
			continue
		}
		if !seen[s] {
			t.Errorf("state %v unreachable from QuotaDeducted (dead state)", s)
		}
	}
}

func TestNoSelfLoops(t *testing.T) {
	for _, from := range allStates {
		for _, to := range outgoing(from) {
			if from == to {
				t.Errorf("self-loop on %v", from)
			}
		}
	}
}
