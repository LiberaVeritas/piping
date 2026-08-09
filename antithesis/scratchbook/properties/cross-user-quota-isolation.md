# cross-user-quota-isolation

## What led here

Added during property evaluation (Coverage Balance and Wildcard lenses both
found it independently). Every one of the original 22 properties evaluates **one
user's state in isolation** and asks whether it is internally consistent. A
defect that mixed two users' data would produce a state that is perfectly
self-consistent for each user examined alone, and would therefore pass all of
them.

## Code paths — everything here keys on `user_id`

- `internal/postgres/quota_store.go:13-16` — `RemainingQuota` filters both the
  grant sum and the job-cost sum on `g.user_id = $1` / `j.user_id = $1`
- `internal/postgres/job_store.go:26-28` — `SELECT id FROM app_user WHERE id = $1
  FOR UPDATE`: the lock is taken on the *submitting user's* row, and correctness
  of the whole quota mechanism depends on it being that row and no other
- `internal/postgres/job_store.go:34-37` — the in-transaction balance derivation,
  same predicate
- `internal/postgres/user_store.go:22-25`, `:61-65` — history queries
- `internal/postgres/quota_store.go:62-72` — `EnsureGrant`, unique on
  `(user_id, semester_id)`

Six independent query sites, each carrying the predicate by hand. There is no
shared helper and no row-level security in the schema.

## What goes wrong

- A dropped or mistyped `WHERE user_id = $1` in the balance derivation makes
  every user share one pooled balance — the first submitter drains it.
- A lock taken on the wrong row removes serialisation between two users while
  appearing to serialise, which is worse than no lock: it looks correct under
  single-user testing.
- A history query missing the predicate discloses other students' document names.

## Expensive to rediscover

- `TestConcurrentSubmitDifferentUsersDoNotBlockEachOther` exists and looks like
  it covers this, but it asserts **liveness** (both users' submissions succeed),
  not isolation. It would pass if both users shared a balance, as long as neither
  blocked.
- The `FOR UPDATE` is on `app_user`, a table with exactly one column (`id`). It
  exists *solely* as a lock target — a detail easy to lose in a refactor that
  "cleans up" an apparently useless table.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` driving ≥2 identities and asserting each user's
  balance changes only in response to that user's own operations. The workload
  must track expected per-user balances itself — this is one of the few
  properties where a shadow ledger in the workload is required rather than
  optional.
- Missing (useful): SUT-side `Sometimes` confirming two different users'
  submissions were genuinely concurrent, so a green result is not just serialised
  execution.

## Open Questions

- None.
