# no-overspend-under-concurrent-submit

## What led here

`internal/app/submit.go:29` carries the comment `// must be atomic to avoid
double spend` on the `jobCheckQuotaStore` interface, and
`internal/postgres/job_store.go:12` says `// read verify and update atomically`.
Two claimed guarantees on the same path, both untested under faults.

This is distinct from `quota-never-negative`: that property checks stored state,
this one checks the **accept/reject decisions the API returned**. A system could
keep stored quota non-negative while wrongly rejecting valid submissions, or
wrongly accept and then fail to record — both are caught here and not there.

## Code paths

- `internal/postgres/job_store.go:20-22` — `SET LOCAL lock_timeout = '5s'`
- `:26-31` — `SELECT id FROM app_user WHERE id = $1 FOR UPDATE`, the serialiser
- `:34-40` — the balance derivation, inside the same transaction
- `:42-45` — the decision
- `:47-56` — the INSERT, same transaction
- `:57-60` — commit

Isolation is pgx's default (Read Committed). Correctness rests entirely on the
row lock, not on the isolation level.

## What goes wrong

Two submissions both read the same remaining balance and both insert. The user
prints twice what they had. The row lock prevents it — unless the lock is not
actually held for the whole read-decide-write span, or acquisition times out and
the error path does something other than fail closed.

## Expensive to rediscover

- A submission holds a pooled connection for the entire transaction *including*
  the `FOR UPDATE` wait. With `pgxpool`'s default `max(4, NumCPU)` connections, a
  handful of concurrent submissions by one user can occupy the pool, which turns
  a per-user serialisation into a global one.
- `lock_timeout = 5s` means the guard can *expire* rather than block forever. The
  resulting error is not a quota error, so `Submit` returns it as a generic
  failure (`submit.go:144`) and nothing is delivered — fail-closed, which is
  right, but the user sees the "we could not confirm your job's status" message.
- A demonstrated defect on this exact path: `CheckQuotaAndStore` computed the
  correct state but never assigned it to the returned job, so
  `quota.ErrInsufficient` was unreachable and every over-quota submission
  reported success and was delivered. Confirmed by a failing test, not by a
  report. Fixed in the working tree at the time of writing.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` comparing the sum of accepted costs against the
  provisioned grant.
- Missing (useful): SUT-side `Sometimes` at the point where the lock is acquired
  after a wait, to confirm the workload actually creates contention rather than
  serialising by accident.

## Open Questions

- **Under `lock_timeout` expiry, does the submission fail closed, or can a client
  retry turn it into a double-spend?** The server side fails closed — no job row,
  no delivery. But the browser sees a 500 and a message inviting them to check
  their history; a user who retries creates a second independent submission,
  which is correct behaviour only if the first truly did not commit.
  `(partial: the error propagates as a non-quota error and Submit returns without delivering, but retry behaviour is client-driven)`

### Investigation Log

#### Under `lock_timeout` expiry, does the submission fail closed, or can a client retry turn it into a double-spend?

- **Examined:** `internal/postgres/job_store.go:20-31` (the `SET LOCAL
  lock_timeout` and `FOR UPDATE`), `:18` (`defer tx.Rollback`),
  `internal/app/submit.go:138-145`, `internal/web/handlers.go:114-118` and
  `mapSubmitError` at `:194-216`.
- **Found:** the server side fails closed and cleanly. A lock timeout surfaces
  from the `QueryRow(...).Scan` at `:28` as a pgx error, `CheckQuotaAndStore`
  returns before the INSERT, and `defer tx.Rollback(ctx)` discards the
  transaction — no job row, no quota effect, no delivery. `Submit` wraps it as
  `storing job for user %q`, which is not `quota.ErrInsufficient`, so
  `mapSubmitError` falls to the default branch: HTTP 500 with "we could not
  confirm your job's status. Check your job history before submitting again."
- **Not found:** any server-side idempotency key, request deduplication, or
  submission token. A user who retries creates a genuinely independent
  submission, which is correct *given* the first did not commit — and it did not.
- **Conclusion:** the mechanism is fail-closed and the double-spend concern does
  not survive contact with the code. Remaining partial: the user-facing message
  actively invites a retry for a case where nothing happened, which is a
  usability defect rather than a correctness one. Kept as `(partial: ...)`
  because the correctness half is resolved and only the message question — a
  product decision — remains.
