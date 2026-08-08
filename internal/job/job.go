package job

import (
	"errors"
	"fmt"
	"time"
)

type Job struct {
	ID            int64
	Username      string
	QueueID       int64
	DestinationID *int64
	State         State
	NumPages      int
	NumColorPages int
	Copies        int
	Cost          int
	Color         bool
	Duplex        bool
	DocumentName  string
	SubmittedAt   time.Time
	CompletedAt   *time.Time
	RefundedAt    *time.Time
}

type State struct{ name string }

func (s State) String() string { return s.name }

var (
	Unknown           = State{""}
	QuotaInsufficient = State{"quota_insufficient"}
	QuotaDeducted     = State{"quota_deducted"}
	PrintSent         = State{"print_sent"}
	PrintSucceeded    = State{"print_succeeded"}
	PrintFailed       = State{"print_failed"}
	Refunded          = State{"refunded"}
)

var allStates = []State{
	QuotaInsufficient, QuotaDeducted, PrintSent, PrintSucceeded, PrintFailed, Refunded,
}

func StateFromString(s string) (State, error) {
	for _, st := range allStates {
		if st.name == s {
			return st, nil
		}
	}
	return Unknown, fmt.Errorf("unknown job state %q", s)
}

var (
	ErrUnexpectedState   = errors.New("job not in expected state")
	ErrInvalidTransition = errors.New("invalid job state transition")
)

func IsTerminal(s State) bool {
	switch s {
	case QuotaInsufficient, PrintSucceeded, PrintFailed, Refunded:
		return true
	}
	return false
}

func (s State) DeductsQuota() bool {
	switch s {
	case QuotaDeducted, PrintSent, PrintSucceeded:
		return true
	}
	return false
}

func QuotaDeductingStateNames() []string {
	return []string{QuotaDeducted.name, PrintSent.name, PrintSucceeded.name}
}

var validTransitions = map[State][]State{
	QuotaDeducted:  {PrintSent, PrintFailed},
	PrintSent:      {PrintSucceeded, PrintFailed},
	PrintSucceeded: {Refunded},
}

func ValidTransition(from, to State) bool {
	for _, t := range validTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

type WithDestinationName struct {
	Job
	DestinationName string
}
