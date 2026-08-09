# sweeper-never-preempts-live-send

## What led here

`cmd/piping/main.go:140-145` is a startup guard:

```go
if sweepAge <= sendTimeout {
    return fmt.Errorf("SWEEP_AGE_BOUND %q must exceed SEND_TIMEOUT %q", ...)
}
if sweepAge < 2*sendTimeout {
    log.Warn("leave more margin", ...)
}
```

The existence of this check is an explicit statement that the author knew the
Sweeper could race a live delivery. The guard compares two durations. The actual
window contains more than two.

## Code paths

- `internal/app/sweep.go:44` — `cutoff := time.Now().Add(-s.ageBound)`
- `internal/app/sweep.go:45` — selects jobs in `quota_deducted` / `print_sent`
  with `submitted_at < cutoff`
- `internal/postgres/job_store.go:47-52` — `submitted_at` is set by the database
  at INSERT, i.e. **before** destination selection, `MarkSent`, ticket
  construction, and the whole send
- `internal/app/deliver.go:79` — `sendCtx` bounded by `SEND_TIMEOUT`, created
  once for all attempts

So the elapsed time from `submitted_at` to the end of the last send attempt is:

```
commit + pickDestination (2 DB queries) + MarkSent + XCPT + Σ(send attempts, ≤ SEND_TIMEOUT)
```

Only the last term is in the guard's budget.

## What goes wrong

The Sweeper writes `print_failed`. Quota is returned. The document prints
anyway. The Deliverer's `print_succeeded` write then loses the CAS and
`resolve` maps that to `return DeliveryFailed, nil` (`deliver.go:120-123`), so
the user is shown "Printing failed … your quota was refunded" and collects the
printout.

**No crash is required.** Slowness alone is sufficient.

## Expensive to rediscover

- The failure is *silent by design*: `deliver.go:121` logs it at Warn as "job
  found already resolved", which reads like a benign race, and the handler
  reports a refund. Nothing anywhere marks the combination "we refunded a job
  whose send succeeded" as anomalous.
- The clock comparison spans two machines: `cutoff` is computed with Go's
  `time.Now()` on the app, `submitted_at` with PostgreSQL's `now()`. Skew shifts
  the window in either direction, and nothing checks it.
- With the k8s defaults in `k8s/piping.env.example` (`SEND_TIMEOUT=5s`,
  `SWEEP_AGE_BOUND=2m`) the margin is large. The guard permits `SWEEP_AGE_BOUND`
  as low as just above `SEND_TIMEOUT`, where it is not.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing (required): a SUT-side flag set for the duration of `Sender.Send` per
  job id, plus an `Always` at the Sweeper's transition point asserting the flag
  is not set for the job being resolved. A workload cannot observe this — it
  cannot see that a subprocess is mid-execution.
- Missing (useful): `Sometimes` on "the Sweeper resolved a job that had already
  been marked sent", to confirm the timeline explores the contended path.

## Open Questions

- **What margin above `SEND_TIMEOUT` actually makes this safe?** The answer
  determines whether the startup guard should be strengthened (e.g. to
  `SWEEP_AGE_BOUND > SEND_TIMEOUT + ANALYSIS_BUDGET + slack`) or whether the
  Sweeper should key off a `sent_at` timestamp rather than `submitted_at`. The
  latter would remove the analysis and queueing time from the window entirely and
  is probably the real fix.
