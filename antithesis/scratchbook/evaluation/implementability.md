---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# Lens 3 — Implementability

Can each property actually be checked given the planned topology and the
codebase as it stands?

## Blockers requiring a SUT change the catalog underweights

### `response-matches-recorded-state` — the response carries no job identifier

The property compares rendered result text against the job row it describes. To
do that, the workload must know **which job** a response refers to.

`handleSubmit` renders `resultView{Title, Message}` (`handlers.go:120-126`), and
the message is prose: `"%d page(s), %d quota deducted."` — page count and cost,
no id. `SubmitResult.JobID` exists (`submit.go:150`) and is discarded by the
handler.

Correlating by "the most recent job for this user" is unreliable under a workload
that submits concurrently, which is exactly the condition the property needs.

**Required change:** emit the job id in the result fragment (a `data-job-id`
attribute is sufficient and invisible to users). Small, but it is a SUT change
the catalog mentions only in the evidence file. Promote it to the Invariant
field.

### `only-legal-state-transitions` — polling cannot see every transition

The workload polls job state. Two transitions between polls collapse into one
observed change, so a genuinely illegal intermediate is missable. Worse, the
legal sequence `quota_deducted → print_sent → print_succeeded` observed as
`quota_deducted → print_succeeded` looks *illegal* and produces a false positive.

**Required change:** a SUT-side assertion inside `UpdateJobState` and `MarkSent`
checking the transition against the graph. Cheap (both are chokepoints) and it
makes the property exact instead of approximate — and removes the false-positive
mode, which matters more.

### `analysis-terminates-within-bound` — no threshold exists

The assertion needs a latency ceiling that is not derivable from the repo. The
open question already records this; the implementability consequence is that this
property **cannot be implemented at all** until someone measures a legitimate
100-page render on the target CPU allocation. Until then it is a placeholder.

## Feasible but delicate

### `retry-never-double-spools` — narrow fault window

Requires a fault that lands after `smbd` has accepted the payload but before
`smbclient` reports success. A partition on the `piping ↔ smb-sink` link can do
it, but the window is milliseconds and the outcome depends on TCP buffering. The
receipt-writing design helps: a receipt exists iff smbd's print pipeline ran, so
the oracle is precise even if the window is hard to hit.

Pair it with a `Sometimes` on "a send was retried" so a green result is
distinguishable from a never-exercised path. Without that, this property is at
high risk of passing vacuously.

### `quota-correct-under-pool-exhaustion` — saturation is hard to guarantee

The pool is `max(4, NumCPU)` and sized off the *node*, so on a large host it may
be too big to saturate with a reasonable workload. Node throttling helps by
slowing query completion. **Recommendation:** set `pool_max_conns` explicitly in
`DATABASE_URL` for the Antithesis environment so the property targets a known,
small pool. This makes the test deterministic in the dimension that matters and
is independently good practice.

### `sweeper-never-preempts-live-send` — needs config in a narrow band

Only interesting when `SWEEP_AGE_BOUND` is close to `SEND_TIMEOUT`. The startup
guard rejects `<=`, so the band is `(SEND_TIMEOUT, SEND_TIMEOUT + ε]`. The
topology's config-variance plan must deliberately include that band; the
production-like defaults (5s vs 2m) will never exercise it.

## Topology adequacy

| Property | Needs | Supported? |
|---|---|---|
| `charged-iff-spooled` and cluster | independent spool oracle | Yes — `smb-sink` receipts |
| `readyz-reflects-db-reachability` | partition app ↔ database | Yes — separate containers |
| `retry-never-double-spools` | partition app ↔ spool | Yes — separate containers |
| `provisioning-is-idempotent` | real login path | Yes — `idp` container |
| crash-window properties | node termination | **Off by default — must be enabled** |
| `check-constraint-violation-unreachable` | `COLOR_RATE=0` in some timelines | Depends on whether env vars vary per timeline or per run — **unresolved** |

That last row is a real constraint: if environment variables are fixed per run
rather than per timeline, config-dependent properties need separate runs, which
changes how the catalog is scheduled rather than whether it is implementable.

## Passes

- Every stored-state property is observable via direct SQL from the workload.
- Importing `piping/internal/job` into the workload keeps the quota-deducting
  state set from being duplicated — a real correctness benefit for the assertions
  themselves.
- The three SUT-side instrumentation points are all at chokepoints (`deliver.go`
  send window, Sweeper transition, `mapPostgresError`), so instrumentation is
  small and localised.

## Uncertainties

- Whether `smbd` can be configured with a print command that produces a durable,
  readable receipt without a second process in the container. If not, the sink
  needs a purpose-built SMB responder, which is a significant scope increase and
  should be settled before committing to the topology.
