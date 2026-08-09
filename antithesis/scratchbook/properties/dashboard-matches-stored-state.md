# dashboard-matches-stored-state

## What led here

Added during property evaluation (Coverage Balance and Wildcard). The catalog had
exactly one property comparing something the user is *told* against something the
system *recorded* — `response-matches-recorded-state` — and that property exists
only because a defect of that shape was demonstrated in this codebase. The same
class exists on the read path and was uncovered.

## Code paths

- `internal/web/handlers.go:21-57` — `handleHome`: `RemainingQuota`,
  `GrantedQuota`, `EnabledQueues`, and the last 24h of jobs
- `internal/web/handlers.go:59-71` — `handleJobs`: up to 50 jobs, all time
- `internal/postgres/user_store.go:57-79` — `JobsWithDestinationForUser`:
  ```sql
  SELECT submitted_at, document_name, num_pages, copies, cost, state::text, COALESCE(d.name, '')
  FROM job LEFT JOIN destination d ON destination_id = d.id
  WHERE user_id = $1 AND submitted_at > $2 ORDER BY submitted_at DESC LIMIT $3
  ```
- `internal/postgres/user_store.go:41-55` — `scanJobView` scans those seven
  columns **positionally** into struct fields
- `internal/web/views.go:48-62` — `toJobView` maps to the render model, with
  `""` destination becoming `"None"`

## What goes wrong

A column reordering in the SQL or the scan silently swaps values of compatible
types. `num_pages`, `copies`, and `cost` are all `integer` — swapping any two
produces a page count where the cost should be, on every row, for every user,
with no error anywhere.

The codebase knows this hazard: `internal/postgres/store_test.go` contains
`TestJobWithDestinationForUserScans`, whose failure message literally asks
`"succeeded row: pages=%d cost=%d, want 12/12 (column order?)"`. That test covers
the scan; nothing covers the path from scan to rendered page.

## Expensive to rediscover

- `JobsWithDestinationForUser` substitutes `time.Unix(0, 0)` for a zero
  `newerThan` (`user_store.go:58-60`), so `/jobs` passing `time.Time{}` means "all
  time". A change to that substitution silently truncates history.
- `formatTimeSince` (`views.go:64-73`) computes against `time.Now()` on the app
  container and produces `"0m"` for anything under a minute — and negative values
  if the app's clock runs behind the database's `submitted_at`.
- The home page's "recent" list is the last 24 hours capped at 5
  (`handlers.go:41`), while `/jobs` is 50 with no time bound. Two different
  windows over the same data, so a property must compare each against its own
  query parameters.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` — parse the rendered dashboard and history and
  compare against direct SQL for the same user with the same window parameters.
- Parsing HTML is brittle; adding machine-readable attributes to the templates
  (as `response-matches-recorded-state` already requires for the job id) would
  make this and that property share one mechanism.

## Open Questions

- **`formatTimeSince` can render a negative age under clock skew or clock
  faults.** Is that worth asserting as a defect, or is it cosmetic noise that
  would generate failures without value? It is user-visible ("-3m ago") and
  trivially fixable by clamping, but it harms nobody.
