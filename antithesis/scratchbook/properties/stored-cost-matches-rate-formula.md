# stored-cost-matches-rate-formula

## What led here

`cost` is the sole input to every quota computation, and it is written once at
insert time from a value computed three layers up. Nothing revalidates it.

## Code paths

- `internal/quota/quota.go:13-15` — `Cost(pages, colorPages) = (pages -
  colorPages) + colorPages * ColorRate`
- `internal/app/submit.go:120` — `cost := s.rates.Cost(pages, colorPages) *
  in.Copies`
- `internal/app/submit.go:104-106` — when `Color` is false, `colorPages` is
  zeroed *before* costing, so a colour document submitted as mono bills as mono
- `internal/postgres/job_store.go:47-52` — the insert; `cost` arrives as an
  opaque integer

## What goes wrong

If a stored cost can disagree with the job's own `num_pages` /
`num_color_pages` / `copies`, then every balance derived from it is wrong and no
other property localises the cause — `quota-never-negative` would fail somewhere
far from the write that caused it.

## Expensive to rediscover

- The rate is applied per *page*, then multiplied by copies. A colour page costs
  `ColorRate`, not `1 + ColorRate` — easy to get wrong when re-deriving.
- `COLOR_RATE` is process configuration, not a column. The formula is only
  checkable against the *current* configured rate, so historical rows are
  verifiable only while the rate is unchanged.
- `cost_positive CHECK (cost > 0)` means a computed cost of 0 is rejected by the
  database rather than stored — see `check-constraint-violation-unreachable`.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` recomputing the formula for every readable job
  row using the timeline's configured `COLOR_RATE`.

## Open Questions

- **Is `COLOR_RATE` guaranteed stable for the lifetime of stored jobs?** If it
  can change between deployments, historical rows stop satisfying the formula and
  the property must either scope to jobs created within the timeline or the
  schema must snapshot the rate per job. The latter would also make historical
  balances auditable, which they currently are not.
