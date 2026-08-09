# single-winner-guarded-transition

## What led here

Every state change in the system is a compare-and-swap. There is no other
mechanism — the schema has an enum but no transition constraint, no trigger, no
row versioning.

## Code paths

- `internal/postgres/job_store.go:68-85` — `UpdateJobState`: `UPDATE job SET
  state = $2 ... WHERE id = $1 AND state = $5`, with `RowsAffected() == 0`
  becoming `job.ErrUnexpectedState`
- `internal/postgres/job_store.go:87-98` — `MarkSent`: the same pattern, with the
  guard hardcoded to `state = 'quota_deducted'`
- Contending actors: `internal/app/deliver.go:70` and `:119` (Deliverer),
  `internal/app/sweep.go:51` (Sweeper)

Existing coverage: `TestConcurrentStateTransitionExactlyOneWins` races 8
goroutines against a real database and asserts exactly one wins. That is the
single-process, fault-free case.

## What goes wrong

Two winners on a transition into or out of the quota-deducting set means a job's
cost is counted or refunded twice. Because quota is derived by summing over
states, a duplicated transition does not "double-count" directly — but a job that
could move `print_failed → print_succeeded` would silently re-charge a refunded
job.

## Expensive to rediscover

- The CAS is the *only* guard, and it is expressed twice in two different ways
  (`UpdateJobState` consults `job.ValidTransition` first; `MarkSent` does not
  consult it at all). See `only-legal-state-transitions`.
- `RowsAffected() == 0` conflates "someone else won" with "the row does not
  exist". A job id that never existed produces `ErrUnexpectedState`, which both
  callers treat as a benign lost race.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` racing transitions and asserting one winner.
- Missing (useful): SUT-side `Sometimes` at the `RowsAffected() == 0` branch, to
  confirm the workload actually produced contention rather than serialising.

## Open Questions

- None.
