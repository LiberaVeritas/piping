# quota-never-negative

## What led here

Quota is derived, not stored (`internal/postgres/quota_store.go:11-21`), so there
is no balance column a constraint could protect. `schema.sql` has
`amount_positive CHECK (amount >= 0)` on grants and `cost_positive CHECK (cost >
0)` on jobs, but **nothing constrains their difference**. Non-negativity is
purely an emergent consequence of the application's check-then-insert logic.

The existing test `internal/postgres/store_test.go` (`TestConcurrentSubmitCannot
Overspend`) asserts exactly this in the single-process, fault-free case, and the
rapid state machine `TestStoreQuotaStateMachine` asserts `remaining == granted -
spent` and `remaining >= 0` after every action.

## Code paths

- `internal/postgres/quota_store.go:13-16` — the derivation:
  `COALESCE(SUM(grant.amount),0) - COALESCE(SUM(job.cost WHERE state = ANY(...)),0)`
- `internal/job/job.go:75-77` — `QuotaDeductingStateNames` defines the subtracted
  set: `quota_deducted`, `print_sent`, `print_succeeded`
- `internal/postgres/job_store.go:13-66` — `CheckQuotaAndStore`, the only writer
  that is supposed to preserve the invariant

## What goes wrong

Negative remaining quota means a user printed more pages than they were granted.
Because the value is derived, it is also self-healing in appearance: the number
on the dashboard just goes negative, and the template renders it without comment.

## Expensive to rediscover

The invariant depends on the *subtracted state set* matching the set of states in
which a document may have been printed. Adding a state, or moving a state in or
out of `QuotaDeductingStateNames`, silently changes every user's balance
retroactively — the derivation is over all history, not a running total. This
also means a bug fixed today changes yesterday's balances.

## Instrumentation

Per `existing-assertions.md` the codebase has no Antithesis SDK assertions.

- Missing: workload-side `Always` evaluating `RemainingQuota` (or the equivalent
  SQL) per user after every operation.
- Not needed SUT-side: the value is fully observable from the database.

## Open Questions

- None.
