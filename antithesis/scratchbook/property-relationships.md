---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# Property Relationships

Lightweight map of clusters, shared mechanisms, and suspected dominance across
the 22 cataloged properties. Flagged during discovery; not a deep analysis.

## Cluster 1 — The delivery window

**Members:** `charged-iff-spooled`, `spool-record-window-observed`,
`sweeper-never-preempts-live-send`, `retry-never-double-spools`,
`shutdown-leaves-jobs-recoverable`

All five live in `internal/app/deliver.go:64-106` and turn on the same two
statement boundaries: `MarkSent` before the send, and the guarded state write
after it.

- **Shared instrumentation:** one SUT-side flag bracketing
  `deliver.go:86-88` serves `charged-iff-spooled`,
  `spool-record-window-observed`, and `sweeper-never-preempts-live-send`. Build
  it once.
- **Shared environment:** all five need the fake SMB sink's per-job receipts.
  Without it none of them are checkable.
- **Dominance:** `charged-iff-spooled` is the strongest statement in the
  catalog — a violation of `sweeper-never-preempts-live-send`,
  `retry-never-double-spools`, or the crash half of
  `shutdown-leaves-jobs-recoverable` should each *also* trip it. The narrower
  properties earn their place by localising the cause: a bare
  `charged-iff-spooled` failure does not say whether the sweeper, a retry, or a
  deploy caused it.
- **`spool-record-window-observed` is not a safety property at all** — it exists
  to make a green `charged-iff-spooled` meaningful. Read them as a pair; a green
  safety result with a never-satisfied `Sometimes` is a non-result.

## Cluster 2 — Quota accounting

**Members:** `quota-never-negative`, `no-overspend-under-concurrent-submit`,
`stored-cost-matches-rate-formula`, `quota-correct-under-pool-exhaustion`,
`provisioning-is-idempotent`

All depend on the derived-balance formula in
`internal/postgres/quota_store.go:13-16` and the state set in
`job.QuotaDeductingStateNames`.

- **Layering:** `stored-cost-matches-rate-formula` guards the *inputs*;
  `quota-never-negative` guards the *result*; `no-overspend-under-concurrent
  -submit` guards the *decisions*. A failure in the first will usually surface as
  a failure in the second, far from its cause — which is exactly why the cheap
  input tripwire is worth having.
- **`provisioning-is-idempotent` is the other side of the ledger.** Everything
  else in this cluster constrains spending; this one constrains granting. A
  double grant makes `quota-never-negative` *more* likely to hold while being
  just as wrong.
- **`quota-correct-under-pool-exhaustion` shares the fault (a starved pool) with
  `readyz-reflects-db-reachability`** in Cluster 4 — one injected condition
  exercises both, from opposite sides.

## Cluster 3 — State machine mechanics

**Members:** `single-winner-guarded-transition`, `only-legal-state-transitions`,
`timestamps-agree-with-state`, `refunded-state-unreachable`

The compare-and-swap `UPDATE` is the shared mechanism; the schema enforces
nothing.

- **Dominance:** `single-winner-guarded-transition` is the mechanism that makes
  `sweeper-never-preempts-live-send` (Cluster 1) safe rather than merely
  unlikely. If the CAS can produce two winners, several Cluster 1 properties fail
  for a reason that has nothing to do with delivery.
- **`refunded-state-unreachable` is coupled to Cluster 2 by a hidden edge:**
  refunding works by *leaving the deducting state set*, not by explicit
  accounting. If the state ever becomes reachable, `quota-never-negative` and
  `only-legal-state-transitions` both acquire a new failure mode simultaneously.
- **`timestamps-agree-with-state` is the weakest member** — no current code reads
  the columns, so a violation harms only future auditing.

## Cluster 4 — Availability and resource limits

**Members:** `analysis-terminates-within-bound`,
`quota-correct-under-pool-exhaustion`, `readyz-reflects-db-reachability`

Connected by a feedback loop worth stating explicitly: `pool.Ping` in the
readiness probe draws from the same pool as request handlers, so pool exhaustion
makes the probe fail, which makes Kubernetes withdraw a *busy but healthy* pod
and shift its load onto peers. `analysis-terminates-within-bound` is the most
likely trigger, since an unbounded ghostscript run is what fills the pool in the
first place.

None of these three implies another, but a single timeline that slows the
database and feeds adversarial PDFs exercises all three at once.

## Cluster 5 — Request-boundary correctness

**Members:** `response-matches-recorded-state`,
`every-job-has-authenticated-owner`, `admin-requires-staff-role`

The web layer's contract with the user: the right person acts, and they are told
the truth about what happened.

- **`response-matches-recorded-state` bridges to Cluster 2** — it is the only
  property that compares a *rendered* value against stored state, so it catches
  the class of defect (a decision not propagating across a layer boundary) that
  `sut-analysis.md` identifies as this codebase's demonstrated weak spot.
- `admin-requires-staff-role` is nearly standalone and the lowest-value member,
  since the page it guards is a stub.

## Standalone

- **`check-constraint-violation-unreachable`** connects to
  `stored-cost-matches-rate-formula` through `quota.Rates.Cost` but is triggered
  by *configuration* rather than input or timing, which makes it the only
  property in the catalog whose fault is a config value. It belongs to no
  cluster and should not be dropped for that reason.
- **`non-terminal-jobs-eventually-resolved`** is the catalog's only liveness
  property. It is the safety net that every Cluster 1 crash scenario relies on:
  if it fails, several Cluster 1 properties become unfalsifiable, because quota
  is never returned and the "eventually correct" half of charged-iff-spooled
  never resolves.

## Cluster 6 — Added by evaluation (Category G)

The five gap-fill properties do not form their own mechanism cluster; each
attaches to an existing one, which is itself evidence they were genuine gaps
rather than a new subsystem.

| New property | Attaches to | Relationship |
|---|---|---|
| `page-count-matches-document` | Cluster 2 | Sits **upstream** of `stored-cost-matches-rate-formula`. That property checks cost is consistent with the page counts; this one checks the page counts are consistent with the document. Together they cover the billing chain end to end; either alone leaves half of it unverified. |
| `cross-user-quota-isolation` | Cluster 2 | The only property requiring two users at once. It is *orthogonal* to the rest of the cluster rather than dominated by it — a failure here is invisible to every single-user property, which is exactly why it was missing. |
| `service-recovers-after-fault` | Cluster 4 | The liveness counterpart to `readyz-reflects-db-reachability`. That property says the probe never lies while down; this one says the system comes back up. Neither implies the other, and a system satisfying only the first is permanently broken. |
| `disabled-destination-never-receives-job` | Cluster 1 (routing) | Shares the destination-selection path with `destination-frozen-across-retries`. Both constrain where a payload may go; one across time (retries), one across configuration change. |
| `dashboard-matches-stored-state` | Cluster 5 | The read-path twin of `response-matches-recorded-state`. Both compare a rendered value against stored state; together they cover the "values crossing a layer boundary" class that `sut-analysis.md` identifies as this codebase's demonstrated weak spot. |

## Suspected dominance summary

| Broader property | Likely also trips when these fail |
|---|---|
| `charged-iff-spooled` | `sweeper-never-preempts-live-send`, `retry-never-double-spools`, `shutdown-leaves-jobs-recoverable` |
| `quota-never-negative` | `stored-cost-matches-rate-formula`, `no-overspend-under-concurrent-submit` |
| `single-winner-guarded-transition` | underpins `sweeper-never-preempts-live-send` |

Dominance is a reason to keep the narrow properties for localisation, not a
reason to drop them.
