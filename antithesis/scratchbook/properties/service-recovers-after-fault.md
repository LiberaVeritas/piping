# service-recovers-after-fault

## What led here

Added during property evaluation (Coverage Balance). The catalog had one liveness
property against eighteen safety properties. Every safety property in it is
satisfied by a system that fails safe and then **stays failed** — including
`readyz-reflects-db-reachability`, which asserts only that the probe does not lie
during an outage.

## Code paths

- `cmd/piping/main.go:147-155` — `pgxpool.New` then `pool.Ping`; a failure here
  is fatal and the process exits. This is startup only.
- **There is no reconnect logic anywhere in the application.** Recovery from a
  transient database outage is delegated entirely to `pgxpool`'s internal
  connection management.
- `internal/web/handlers.go:175-183` — `handleReady` calls `s.ready` (a
  `pool.Ping` with a 2s timeout) per probe, so readiness does track the pool's
  live state rather than a cached flag.
- `internal/app/sweep.go:46-49` — a Sweeper pass that fails logs and returns; the
  next tick retries. No backoff, no failure counter, no give-up.

## What goes wrong

A partition heals and the service does not resume: the pool holds poisoned
connections, `/readyz` stays 503, and Kubernetes restarts the pod. That last part
means the *system* recovers even if the *process* does not, which masks the
defect in production and makes it invisible to anyone reading logs — but under
Antithesis, where the container is not necessarily restarted, the difference is
observable.

## Expensive to rediscover

- The Sweeper's retry loop is the one component that recovers by construction (it
  simply tries again next tick), so a run where the Sweeper resumes but
  submissions do not points at the pool rather than at the app's structure.
- Startup is fail-fast but runtime is fail-soft: the same database outage is
  fatal before `ListenAndServe` and merely a 500 after it. A property that only
  observed startup would conclude the system handles database loss decisively.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Sometimes` — after observing `/readyz` return 503, a
  later full submission succeeds end to end.
- **Must be checked during an `ANTITHESIS_STOP_FAULTS` quiet period**, not with
  an `eventually_` command: `eventually_` is a terminal branch, and recovery
  should be verified mid-run so the workload can continue afterwards and
  potentially recover more than once.

## Open Questions

- **Does `pgxpool` recover from a partition without a process restart in all
  cases, or are there error classes that poison the pool?** The app has no
  reconnect path of its own, so whatever the pool does is the whole recovery
  story. If some error classes are unrecoverable, the honest property is weaker —
  "recovers, or reports unready until restarted" — and the fix would be an
  application-level health-driven pool reset.
