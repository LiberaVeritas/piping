# non-terminal-jobs-eventually-resolved

## What led here

The Sweeper is the only mechanism that reclaims quota held by a job whose
delivery never concluded. `internal/app/sweep.go:53` comments the intent:
`// resolved by Deliverer`, and `deliver.go:73` says `// resolved by sweep; quota
returned`. Each component's comment defers to the other; the Sweeper is the
backstop.

## Code paths

- `cmd/piping/main.go:211` — `go sweeper.Run(ctx)`, the only background goroutine
- `internal/app/sweep.go:29-41` — `Run`: one immediate pass, then every
  `SWEEP_INTERVAL`
- `internal/app/sweep.go:43-62` — `pass`: fetch up to `SWEEP_BATCH` stale jobs,
  transition each from **its own state** to `print_failed`
- `internal/postgres/job_store.go:116-138` — `StaleJobs`, ordered by
  `submitted_at`, limited

## What goes wrong

Quota held by a crashed or abandoned delivery is never returned. The user sees a
reduced balance with no corresponding printout and no error — the job simply sits
in `print_sent` forever. There is no alert, no metric, and no UI surface that
distinguishes "in flight" from "stuck".

## Expensive to rediscover

- The Sweeper transitions from `j.State`, not a hardcoded state
  (`sweep.go:51`). This is essential and easy to get wrong: hardcoding
  `quota_deducted` would make the guarded UPDATE match nothing for `print_sent`
  jobs, and they would be stranded forever with no error — the failure would be
  perfectly silent.
- A `pass` that errors on `StaleJobs` logs and returns; the next tick retries.
  There is no backoff and no failure counter.
- `Run` performs its first pass **before** the ticker, so a restart immediately
  attempts recovery. This matters for the liveness property under Antithesis's
  restart faults: recovery does not wait a full interval.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Sometimes` evaluated late in the run — no job remains
  non-terminal beyond a generous multiple of `SWEEP_AGE_BOUND`.
- Missing: `Reachable` at `sweep.go:59` (the "resolved stale job" branch), to
  confirm the recovery path actually executed rather than the run simply having
  no stale jobs.

## Open Questions

- **If stale jobs accumulate faster than `SWEEP_BATCH` per `SWEEP_INTERVAL`, does
  the Sweeper keep up?** With the defaults (100 per minute) a burst beyond that
  drains slowly but does drain, since `StaleJobs` orders by `submitted_at`. Under
  sustained overload the property as stated would fail, which may be correct
  behaviour rather than a defect — that distinction determines whether the
  assertion needs a load precondition.
