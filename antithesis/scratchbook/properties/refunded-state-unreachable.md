# refunded-state-unreachable

## What led here

A grep for `Refunded` across all non-test Go sources returns only *declarations
and plumbing*, never a call:

- `internal/job/job.go:38` — the state is declared
- `internal/job/job.go:42` — it is in `allStates`
- `internal/job/job.go:61` — `IsTerminal` includes it
- `internal/job/job.go:82` — `print_succeeded → refunded` is a legal edge
- `internal/postgres/job_store.go:78` — `to == job.Refunded` is passed as the
  `refunded_at` write flag
- `internal/job/job.go:24` — the `RefundedAt` field exists on the struct

**No code anywhere calls `UpdateJobState(..., job.Refunded)`.** The state is
declared, plumbed, indexed, and unreachable.

## Code paths that might have been expected to use it

- `internal/app/sweep.go:51` — the Sweeper resolves stale jobs to `print_failed`,
  not `refunded`. Quota is returned because `print_failed` is outside the
  deducting set, so a "refund" happens *implicitly* by state change, never by
  entering the `refunded` state.
- `internal/web/handlers.go` — no admin or operator endpoint performs one.
  `handleAdmin` renders a static page.

## What goes wrong

Two ways, depending on the intent:

1. **If a refund tool is planned**: whoever adds it must know that quota is
   already returned by leaving the deducting set, so transitioning
   `print_succeeded → refunded` returns quota *as a side effect of the state
   set*, not through any explicit accounting. Adding the transition without
   understanding that is how a double refund gets written.
2. **If it is vestigial**: the state machine advertises a capability the system
   does not have, and `refunded_at` is a column that can never be non-null.

## Expensive to rediscover

The refund semantics are implicit in `QuotaDeductingStateNames`
(`internal/job/job.go:75-77`). Nothing is named "refund" in the quota code at
all. A reader looking for the refund logic will not find it, because it is the
absence of a state from a list.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: `Unreachable` placed where a transition to `job.Refunded` would occur.
  Since no such call site exists, the natural home is inside `UpdateJobState`,
  guarded on `to == job.Refunded` — which also makes the assertion fire the
  moment someone adds the feature without revisiting the accounting.

## Open Questions

- **Is an operator refund path planned, or should the state and `refunded_at`
  column be removed?** If planned, this property flips from `Unreachable` to a
  `Sometimes` plus a new safety property about not double-refunding. If
  vestigial, removing them shrinks the state machine and deletes a column.
  Cannot be determined from the code — the state is fully plumbed but never
  invoked, which is equally consistent with both. `(needs human input)`

### Investigation Log

#### Is an operator refund path planned, or should the state and `refunded_at` column be removed?

- **Examined:** `grep -rn "Refunded"` across all Go sources excluding tests;
  `internal/web/handlers.go` and `server.go` for any operator or admin endpoint;
  `internal/app/*.go` for any refund entry point; `schema.sql` for the column and
  its constraints; `migration/*.sql` for a historical refund workflow; the git
  log (`init`, `oidc`, a typo fix, CI, an SQL fix) for a commit that added or
  removed one.
- **Found:** every occurrence is a declaration or plumbing —
  `job.go:24,38,42,61,82` and `job_store.go:78`. The only route that could host
  an operator action is `GET /admin`, which is registered with a `// TODO` above
  it (`server.go:59-60`) and renders a static template with no data. So there is
  an obvious *intended* home for such a tool, and it is empty.
- **Not found:** any design note, README section, or commit message mentioning
  refunds. The repo has no docs directory, and the user's scope answer was "just
  this directory", so no external tracker was available to check.
- **Conclusion:** the code is genuinely ambiguous. The `// TODO` on `/admin` is
  weak circumstantial evidence that operator tooling is planned, but it names
  nothing. Exhausted what the directory can tell me; tagged `(needs human
  input)`.
