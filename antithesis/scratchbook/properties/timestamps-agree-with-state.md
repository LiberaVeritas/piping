# timestamps-agree-with-state

## What led here

`completed_at` and `refunded_at` are written by conditional expressions inside
the same `UPDATE` as the state change, but they are also written by a *different*
mechanism at insert time. Two independent code paths maintain one invariant, and
no schema constraint ties them together.

## Code paths

- `internal/postgres/job_store.go:72-77` — `UpdateJobState`:
  ```sql
  completed_at = CASE WHEN $3 THEN now() ELSE completed_at END,
  refunded_at  = CASE WHEN $4 THEN now() ELSE refunded_at  END
  ```
  with `$3 = job.IsTerminal(to)` and `$4 = (to == job.Refunded)`
- `internal/postgres/job_store.go:47-52` — the INSERT sets `completed_at` via
  `CASE WHEN $3::job_state = 'quota_insufficient' THEN now() END`
- `internal/postgres/job_store.go:88-90` — `MarkSent` sets neither, correctly:
  `print_sent` is not terminal
- `internal/job/job.go:59-65` — `IsTerminal`: `quota_insufficient`,
  `print_succeeded`, `print_failed`, `refunded`
- `schema.sql:61-62` — both columns nullable, no constraint

## What goes wrong

The columns are the audit trail. If `completed_at` can be null on a terminal job
or set on a live one, reconstructing what happened during a billing dispute
becomes guesswork. Nothing in the application reads these columns today, which is
precisely why drift would go unnoticed.

## Expensive to rediscover

- The two writers use different mechanisms to express the same rule: one computes
  terminality in Go and passes a boolean, the other tests the state name in SQL.
  A new terminal state added to `IsTerminal` would be handled by `UpdateJobState`
  automatically and by the INSERT path not at all.
- `refunded_at` can never be set today, because nothing transitions to `refunded`
  — see `refunded-state-unreachable`. The `$4` parameter is permanently false.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` over all job rows checking both biconditionals.
- Alternative worth noting: this invariant is expressible as a schema CHECK
  constraint, which would make it enforced rather than merely asserted. The
  property would then become a test of the constraint's presence.

## Open Questions

- **Is `quota_insufficient` intended to be terminal-with-timestamp at insert
  time?** It is set there and `IsTerminal` agrees, so the two are consistent
  today. If the intent is instead that `completed_at` means "delivery concluded",
  then setting it on a job that was never delivered is wrong and the property's
  biconditional should exclude `quota_insufficient`. The answer changes which
  side of the invariant is the defect.
