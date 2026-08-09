# quota-correct-under-pool-exhaustion

## What led here

A submission holds a pooled connection for the whole transaction *including* the
`FOR UPDATE` wait, and `pgxpool`'s default maximum is `max(4, NumCPU)`. The
per-user serialisation therefore consumes a global resource, and the interaction
between the two is untested.

## Code paths

- `cmd/piping/main.go:147` — `pgxpool.New(ctx, databaseURL)` with no
  `MaxConns` configured, so the default applies
- `internal/postgres/job_store.go:14` — `s.pool.Begin(ctx)` acquires a connection
- `internal/postgres/job_store.go:20-31` — `SET LOCAL lock_timeout = '5s'` then
  `FOR UPDATE`: the connection is held for up to 5s while blocked
- `internal/postgres/job_store.go:57` — commit releases it

Every other store method takes a connection per query and returns it promptly;
only `CheckQuotaAndStore` holds one across a lock wait.

## What goes wrong

With N users' submissions in flight and a small pool, connection acquisition
itself starts to queue behind lock waits. The failure modes to distinguish:

- Submissions time out and fail closed — acceptable.
- A submission's transaction commits but the response is lost — the user is
  charged with no feedback, and may resubmit.
- Health checks fail because `/readyz` cannot get a connection, causing
  Kubernetes to restart a pod that is merely busy — a feedback loop that makes
  the outage worse. `handleReady` calls `pool.Ping` with its own 2s timeout, and
  `Ping` needs a connection from the same exhausted pool.

That last one is the interesting cross-component effect and the reason this
property is separate from `no-overspend-under-concurrent-submit`.

## Expensive to rediscover

- `pool.Ping` competing with request handlers for the same pool means the
  readiness probe degrades exactly when the system is under load. Nothing
  reserves a connection for health checks.
- `lock_timeout` is set per-transaction with `SET LOCAL`, so it applies only
  inside `CheckQuotaAndStore`. Other queries have no statement timeout at all.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` that every submission reporting success has a
  corresponding job row, evaluated while the pool is saturated.
- Missing (useful): SUT-side `Sometimes` on "connection acquisition waited longer
  than X", to confirm the timeline actually reached saturation.

## Open Questions

- **Is the default pool size safe against PostgreSQL's `max_connections`?**
  Investigation (below) established the size is `max(4, runtime.NumCPU())` and
  that `NumCPU` reports the *node's* CPUs rather than the container's CPU limit.
  On a large node this is a large pool per replica, which inverts the concern
  from starvation to database connection exhaustion. Which risk is live depends
  on the node size and PostgreSQL's configured `max_connections`, neither of
  which is in this repo. `(needs human input)`

### Investigation Log

#### Is `pgxpool`'s default max-conns appropriate given each submission holds a connection across a lock wait of up to 5s?

- **Examined:** `cmd/piping/main.go:147` (`pgxpool.New` with no config),
  `pgxpool/pool.go:19,374-385` in `github.com/jackc/pgx/v5@v5.10.0`,
  `k8s/app.yaml` resource limits, `k8s/piping.env.example` for a
  `pool_max_conns` parameter in `DATABASE_URL`.
- **Found:** the default is computed as
  ```go
  config.MaxConns = defaultMaxConns            // 4
  if numCPU := int32(runtime.NumCPU()); numCPU > config.MaxConns {
      config.MaxConns = numCPU
  }
  ```
  so `max(4, NumCPU)`, overridable only via a `pool_max_conns` parameter in the
  connection string. `DATABASE_URL` is supplied from the secret and the example
  config sets no such parameter.
- **Correction to the original premise:** `runtime.NumCPU` reflects the process's
  CPU affinity, not the cgroup CPU *quota*. A container with `limits: cpu: 1` on
  a 64-core node still sees 64, so the pool is sized off the host. The original
  question assumed the opposite (that a 1-core limit implied 4 connections) and
  was wrong in the dangerous direction.
- **Not found:** PostgreSQL's `max_connections` for the deployment — the database
  is a stock `postgres:18-alpine` container with no configuration in this repo,
  so it uses the upstream default of 100. Also not found: any intended replica
  count, which multiplies the per-replica pool.
- **Conclusion:** the mechanism is fully resolved; the risk assessment is not,
  because it depends on node size, replica count, and the database's
  `max_connections` — none of which are determinable from this directory. Tagged
  `(needs human input)`. Regardless of the answer, setting `pool_max_conns`
  explicitly in `DATABASE_URL` would make the behaviour independent of node
  size, which is worth doing on its own merits.
