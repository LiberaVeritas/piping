# only-legal-state-transitions

## What led here

The transition graph is declared in Go (`internal/job/job.go:79-83`) but enforced
in SQL, and the two are not derived from each other. `MarkSent` bypasses the
graph entirely.

## Code paths

- `internal/job/job.go:79-92` — `validTransitions` and `ValidTransition`:
  - `quota_deducted → {print_sent, print_failed}`
  - `print_sent → {print_succeeded, print_failed}`
  - `print_succeeded → {refunded}`
- `internal/postgres/job_store.go:69-71` — `UpdateJobState` consults
  `ValidTransition` before touching the database
- `internal/postgres/job_store.go:88-90` — **`MarkSent` does not**. It hardcodes
  `quota_deducted → print_sent` in SQL.
- Entry points that are not edges: the INSERT at `job_store.go:47`, which creates
  a job directly in `quota_deducted` or `quota_insufficient`

Existing coverage: `internal/job/job_test.go` has `TestNoSelfLoops`,
`TestAllStatesReachableFromInitial`, `TestTerminalityMatchesGraph`, and
`TestQuotaNeverRedeductsViaTransition` — all over the *graph*, none over observed
database behaviour.

## What goes wrong

An edge outside the graph that moves a job into the quota-deducting set
re-charges the user. `print_failed → print_succeeded` is the dangerous one: the
Sweeper refunds a stuck job, then a late Deliverer write re-charges it. Today the
CAS prevents this because the Deliverer's guard names `print_sent`, not
`print_failed` — but that is a property of the guard, not of the graph.

## Expensive to rediscover

- `quota_insufficient` has **no outgoing edges** and is created only at insert.
  It is terminal by construction, not by declaration.
- Because quota is derived by summing over the current state of all jobs, a
  single illegal edge retroactively changes a balance rather than producing an
  incremental error. There is no ledger to reconcile against.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` that polls job state and validates each
  observed change against the declared graph plus the two entry points.
- Note: polling can miss a transition pair if two changes happen between reads.
  A SUT-side assertion at each write site would be exact; the workload version is
  a cheaper approximation that still catches a persistently illegal state.

## Open Questions

- **Should `MarkSent` be expressed through `ValidTransition` so the graph is the
  single source of truth?** If yes, the property becomes enforceable at one
  chokepoint and the SUT-side assertion is trivial. If the duplication is
  deliberate (for the extra `destination_id` write in the same statement), the
  property must continue to special-case it, and a graph edit will silently
  diverge from the SQL.
  `(partial: MarkSent's SQL guard is equivalent to the graph today, but is not derived from it)`

### Investigation Log

#### Should `MarkSent` be expressed through `ValidTransition` so the graph is the single source of truth?

- **Examined:** `internal/postgres/job_store.go:87-98` (`MarkSent`) against
  `internal/job/job.go:79-92` (`validTransitions`, `ValidTransition`), plus
  `UpdateJobState` at `:68-85` for contrast.
- **Found:** `MarkSent`'s SQL guard is `WHERE id = $1 AND state =
  'quota_deducted'` with `SET state = 'print_sent'`, which is exactly the edge
  `quota_deducted → print_sent` declared at `job.go:80`. They agree today. The
  reason for the duplication is visible: `MarkSent` writes `destination_id` in
  the same statement, which `UpdateJobState`'s signature has no room for.
- **Not found:** any comment or test asserting the two stay in step. Nothing
  fails if a future edit removes the edge from the graph — `MarkSent` would keep
  performing it.
- **Conclusion:** structurally answered (the duplication is deliberate and
  currently consistent), but whether to refactor is a design call for the owner.
  The property must continue to special-case `MarkSent` either way, so this does
  not change the invariant — only how cheaply it could be enforced at a
  chokepoint. Kept as `(partial: ...)`.
