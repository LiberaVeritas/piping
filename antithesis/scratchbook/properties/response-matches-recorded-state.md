# response-matches-recorded-state

## What led here

A defect of exactly this shape was found and confirmed in this codebase during
the session that produced this catalog. `CheckQuotaAndStore` computed the job's
state into a local variable, inserted it correctly, and then tested a *different*
value — the caller-supplied `j.State`, always the zero value — to decide whether
to return `quota.ErrInsufficient`. The error was therefore unreachable.

The user-visible consequence: an over-quota submission rendered "Sent to printer,
N page(s), M quota deducted" while the row said `quota_insufficient` and the
balance never moved. The database was right and the human was told the opposite.

This was confirmed by a failing test, not inferred from a report.

## Code paths

- `internal/postgres/job_store.go:42-64` — decision, insert, and the post-commit
  error return
- `internal/app/submit.go:138-145` — `Submit` branches on
  `errors.Is(err, quota.ErrInsufficient)` and returns the job id either way
- `internal/web/handlers.go:114-127` — `handleSubmit` maps the outcome to result
  text: "Sent to printer", "Printing failed", or `mapSubmitError`
- `internal/web/handlers.go:185-190` — `result` renders it, forcing 200 for htmx

## What goes wrong

The rendered text and the stored state diverge. Every direction is bad:

- "Sent to printer" over a `quota_insufficient` row — free printing, and the
  balance the user sees never matches the receipt they were given.
- An insufficient-quota message over a `quota_deducted` row — the user is charged
  and told they were not, so they resubmit and are charged twice.

## Expensive to rediscover

- The result fragment is the *only* feedback channel. There is no email, no job
  id shown for later reference beyond the response, and the history page shows
  state but not the message that was rendered at submit time. Divergence is
  therefore permanently unauditable after the response is gone.
- `result` rewrites the status to 200 whenever `HX-Request: true`
  (`handlers.go:186-188`), so an htmx client cannot distinguish success from
  rejection by status code — only by body text. Any property here must compare
  text, not status.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` correlating the rendered result body with the
  job row created by that submission. The workload must record the job id it
  submitted; the response body does not currently include one in a
  machine-readable form, which may require adding a data attribute to the result
  fragment.

## Open Questions

- None.
