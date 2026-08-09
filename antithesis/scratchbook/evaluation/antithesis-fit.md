---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# Lens 1 — Antithesis Fit

Does each property require a state space deterministic tests cannot reach?

## Properties that are unit- or integration-test territory

### `admin-requires-staff-role` — strongest removal candidate

`internal/web/handlers_test.go` already contains `TestAdminRequiresStaff`, a
table over all four roles asserting 403/200, driving the real mux with real
sealed cookies. It passes today and was mutation-verified (neutering the rank
comparison fails it).

The property has **no fault sensitivity whatsoever**: no timing, no concurrency,
no partial failure. Antithesis would re-run an existing Go test thousands of
times at the cost of search budget. The one genuinely Antithesis-adjacent angle —
role revocation lag over `SESSION_TTL` — is a *time* property, and clock faults
are off by default.

**Action:** keep only if free; state plainly in the catalog that its value is
regression pinning, not exploration.

### `stored-cost-matches-rate-formula` — a tripwire, not a property

`internal/quota/quota_test.go` covers `Cost` exhaustively including invariants
(never below page count, monotonic in colour pages). The remaining surface is a
single INSERT of an already-computed integer. There is no interleaving in which
the formula and the stored value diverge that is not simply a code defect a unit
test would catch.

**Action:** keep — it is nearly free and localises Cluster 2 failures — but label
it honestly so it is not mistaken for an exploration target.

### `every-job-has-authenticated-owner` — largely pre-covered

`TestApplicationRoutesRequireASession`, `TestSubmitWithoutASessionIsNotProcessed`,
and `TestSubmitRequiresExpectedFetchSite` cover this in-process. Antithesis adds
only the possibility that a fault causes the middleware to be bypassed, which is
not a mechanism the code has.

**Action:** downgrade in expectation; do not remove, since the FK protection is
accidental rather than explicit and the `Unreachable` on an empty `user_id`
insert is worth having.

## Assertion-type problems

### `non-terminal-jobs-eventually-resolved` — the condition is vacuously satisfiable

Stated as `Sometimes(no job is older than 2 × SWEEP_AGE_BOUND while
non-terminal)`. **At the start of every timeline there are no jobs at all**, so
the condition is trivially true before the workload does anything. Antithesis
would report the property satisfied without the Sweeper ever running.

This is the same failure mode the skill warns about with `Sometimes(true, ...)`:
the condition must describe an interesting situation, not an absence.

**Action (refinement):** restate as an observed *recovery event* — `Sometimes(a
job that was observed non-terminal at time T is terminal at time T+n, having been
resolved by the Sweeper rather than the Deliverer)`. That cannot be satisfied
vacuously and requires the recovery path to have actually executed.

### `spool-record-window-observed` — correctly typed

A `Sometimes` on a genuinely rare semantic state, targetable by Antithesis
precisely because it is instrumented. This is the model the previous property
should follow.

## Properties whose Antithesis value is underestimated

### `provisioning-is-idempotent`

The catalog notes that `TestGrantedEqualsEntitled` covers the entitlement logic
against a fake store. That framing undersells the property: the untested surface
is **concurrent logins racing two `ON CONFLICT` clauses against a real database
under faults**, on a path that executes on every single login. `EnsureSemester`'s
`DO UPDATE SET id = EXCLUDED.id` exists solely to make `RETURNING` fire on
conflict — a subtle construction whose behaviour under concurrent inserts and a
partitioned database nobody has tested.

**Action:** raise expectation; this is a stronger fit than the catalog implies.

### `check-constraint-violation-unreachable`

Fit is moderate rather than weak. It is triggered by *configuration* rather than
timing, but Antithesis varies configuration across timelines, and an `Unreachable`
costs almost nothing to evaluate. Its real value is that it covers a whole class
(any CHECK violation) with one assertion.

## Passes

- `charged-iff-spooled`, `sweeper-never-preempts-live-send`,
  `retry-never-double-spools`, `single-winner-guarded-transition`,
  `no-overspend-under-concurrent-submit`, `quota-correct-under-pool-exhaustion`,
  `readyz-reflects-db-reachability`, `shutdown-leaves-jobs-recoverable` — all
  squarely in the sweet spot: timing, concurrency, or partial failure, none
  reachable by a deterministic test.
- `refunded-state-unreachable` — cheap, and its value is exactly that Antithesis
  explores paths a developer did not imagine.

## Uncertainties

- Whether thread-pausing faults meaningfully help `single-winner-guarded
  -transition`: the contention is resolved *inside PostgreSQL*, not in Go, so
  pausing Go threads may not widen the window. The database's own scheduling is
  outside Antithesis's instrumentation.
