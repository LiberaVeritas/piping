# readyz-reflects-db-reachability

## What led here

"Health reporting accuracy" is a named attack surface in the skill's SUT analysis
methodology: the system says healthy but cannot serve. piping's readiness probe
is a single `pool.Ping`, and Kubernetes routes traffic on it.

## Code paths

- `cmd/piping/main.go:213-215` — `ready := func(c context.Context) error { return
  pool.Ping(c) }`
- `internal/web/handlers.go:175-183` — `handleReady`: 2s timeout; `s.ready == nil
  || s.ready(ctx) != nil` yields 503, otherwise `"ready\n"` with 200
- `internal/web/server.go:67` — registered on the root mux, outside
  `requireSession`, so probes need no credentials
- `k8s/app.yaml` — the readiness probe targets `/readyz`, liveness targets
  `/healthz` (static, no database), with a 3s probe timeout against the handler's
  2s internal budget

## What goes wrong

A probe reporting ready during a database outage causes Kubernetes to send users
to a pod that will 500 every request. During a rolling update it is worse: the
new pod reports ready, the old one is terminated, and the service is down with no
healthy backend.

The inverse — reporting not-ready while healthy — costs availability but is safe.

## Expensive to rediscover

- `pool.Ping` acquires a connection from the same pool the request handlers use.
  Under pool exhaustion the probe fails *because the app is busy*, not because the
  database is down, and Kubernetes will pull a loaded pod out of service, shifting
  load to its peers. See `quota-correct-under-pool-exhaustion`.
- The liveness/readiness split is correct and deliberate: `/healthz` is static so
  a database outage does not restart pods. Worth preserving — a naive "make
  liveness check the database too" change would turn an outage into a crash loop.
- `gs` and `smbclient` are verified only at startup (`main.go:161-166`), so a
  toolchain broken after start still reports ready.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` — while the app is partitioned from PostgreSQL,
  `/readyz` does not return 200.
- Missing (useful): `Sometimes` on "readyz returned 503", confirming the timeline
  actually produced a database outage rather than never testing the path.

## Open Questions

- **Should `/readyz` also verify `gs` and `smbclient` availability?** They are the
  other two hard dependencies of the submit path and are checked only at startup.
  Adding them makes the probe a truer statement about serving capability but
  widens the blast radius of a transient subprocess failure into a traffic
  withdrawal.
