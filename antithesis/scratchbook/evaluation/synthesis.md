---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# Evaluation Synthesis

Four lenses run in single-agent mode over the 22-property discovery catalog:
`antithesis-fit.md`, `coverage-balance.md`, `implementability.md`, and
`wildcard.md` (run last, with awareness of the first three).

**Outcome:** 8 refinements applied, 5 gaps filled (catalog now 27 properties),
3 biases escalated to the human.

## Refinements — applied

| # | Property | Finding | Action taken |
|---|---|---|---|
| R1 | `non-terminal-jobs-eventually-resolved` | The `Sometimes` condition ("no job older than 2 × `SWEEP_AGE_BOUND`") is **vacuously true at the start of every timeline**, before any job exists. The property would report satisfied without the Sweeper running. | Restated as an observed recovery event: a job seen non-terminal at T is terminal at T+n, resolved by the Sweeper. Cannot be satisfied vacuously. |
| R2 | `response-matches-recorded-state` | Not implementable as written — the result fragment carries no job id, so responses cannot be correlated to rows under concurrent submission. | Promoted the required SUT change (a `data-job-id` attribute) from the evidence file into the catalog's Invariant field. |
| R3 | `only-legal-state-transitions` | Workload polling has a **false-positive mode**: two legal transitions between polls read as one illegal one. | Moved the assertion SUT-side into `UpdateJobState` and `MarkSent`, both chokepoints. Now exact rather than approximate. |
| R4 | `retry-never-double-spools` | The fault window is milliseconds wide; a green result would be indistinguishable from a retry path that never executed. | Added a companion `Sometimes(a send was retried)`. |
| R5 | `stored-cost-matches-rate-formula` | Unit-test territory — `quota_test.go` covers `Cost` exhaustively and no interleaving makes storage diverge. | Kept (near-free, localises failures) but the Antithesis Angle now says plainly it is a tripwire, not an exploration target. |
| R6 | `admin-requires-staff-role` | Duplicates `TestAdminRequiresStaff`, which already drives the real mux with real cookies. Zero fault sensitivity. | Kept as regression pinning with the Antithesis Angle rewritten to say so. Removal is a live option — see Bias B3. |
| R7 | `provisioning-is-idempotent` | **Value underestimated.** The catalog credited existing coverage that is against a *fake store*; the real surface is concurrent logins racing two `ON CONFLICT` clauses against a partitioned database, on a path that runs on every login. | Antithesis Angle rewritten upward. |
| R8 | `quota-correct-under-pool-exhaustion` | The pool is sized from the **node's** CPU count, so on a large host it may be too big to saturate and the property would never be exercised. | Catalog now directs setting `pool_max_conns` explicitly in the environment's `DATABASE_URL`. |

## Gaps — filled (Category G, 5 new properties)

| # | Gap | Property added |
|---|---|---|
| G1 | **No property looks at two users at once.** All 22 evaluated one user's state in isolation; a dropped `WHERE user_id = $1` or a lock on the wrong row produces state that is self-consistent per user and wrong only in the relationship between them. Found independently by Coverage Balance and Wildcard. | `cross-user-quota-isolation` |
| G2 | **The measurement upstream of all billing is unverified.** Cost derives from a regex over ghostscript stdout (with a comment citing a known Ghostscript output bug), and every billing property begins downstream of it. `internal/ghostscript` has no tests at all. | `page-count-matches-document` |
| G3 | **No recovery liveness.** Every safety property is satisfied by a system that fails safe and stays failed. 18 `Always` to 1 usable `Sometimes` was a portfolio imbalance. | `service-recovers-after-fault` |
| G4 | **The topology's custom fault had no consumer.** Toggling `queue.enabled` / `destination.enabled` mid-run is the system's only configuration-change-under-load surface, and no property used it. | `disabled-destination-never-receives-job` |
| G5 | **The read path was absent.** Students read the dashboard far more than they print, and `JobsWithDestinationForUser` scans seven columns positionally after a LEFT JOIN — a hazard the codebase acknowledges by testing column order with a failure message that asks "column order?". | `dashboard-matches-stored-state` |

Each new property attaches to an existing cluster rather than forming its own
(see `property-relationships.md`, Cluster 6), which is itself evidence these were
genuine gaps rather than a new subsystem.

**Second evaluation pass:** not warranted. Five properties across four existing
clusters, with no new mechanism and no new container required. Per the skill's
guidance, a gap producing a *new category* of properties warrants re-evaluation;
these integrate into existing categories.

## Biases — escalated to the human

These need a judgment call I should not make unilaterally.

### B1 — The database is the oracle for almost the whole catalog

**Evidence** (`wildcard.md` §1): 20 of the original 22 properties read job and
grant rows and check relationships among them. PostgreSQL is inside the blast
radius — Antithesis will partition it, throttle it, and, if node termination is
enabled, kill it. **If a write is lost or misapplied, the property and the system
observe the same wrong state and agree.** The only independent oracle is the SMB
receipt, because it is produced by a different process in a container the app
cannot write to.

**The judgment call:** should the workload maintain a **shadow ledger** — its own
record of every submission and every response, compared against the database
rather than derived from it? That is real implementation cost and makes the
workload stateful, but without it a whole class of storage failure is invisible.
`cross-user-quota-isolation` already requires a partial shadow ledger, so the
marginal cost may be smaller than it first appears.

### B2 — The catalog is oriented toward the write path

**Evidence** (`coverage-balance.md`, `wildcard.md` §4): before gap-filling,
26 of 22 property-mechanisms concerned submission, delivery, and state
transitions; one concerned what a user is shown. G5 adds one read-path property,
which improves but does not correct the balance.

**The judgment call:** is that the right allocation? A wrong charge is theft; a
wrong dashboard is confusion. The current weighting says theft dominates, which
is defensible — but students interact with the dashboard far more often, and the
one defect class this codebase has actually demonstrated (a value not propagating
across a layer boundary) lives in exactly the layer the catalog under-covers.

### B3 — Four properties are vacuous without a fault that is off by default

**Evidence** (`deployment-topology.md` fault table; `faults.md`): node
termination is **disabled by default**. `charged-iff-spooled`'s crash half,
`spool-record-window-observed`, `shutdown-leaves-jobs-recoverable`, and
`non-terminal-jobs-eventually-resolved` all need it. Without it they pass while
proving nothing — which is worse than failing.

**The judgment call:** confirm node termination is enabled for the tenant before
the first run, or accept that the catalog's highest-value cluster is inert.
Clock jitter (also commonly disabled) affects two further properties more mildly.

**A related trade** (`wildcard.md`, cross-cutting): the `idp` container is
justified by essentially one property. Dropping it and minting session cookies in
the workload with `ENCRYPTION_KEY` would remove a container, a network link, and
two low-fit access-control properties — at the cost of `provisioning-is-idempotent`,
which R7 argues is *underrated*. The trade is genuinely balanced, which is why it
belongs here rather than in a refinement.

## Passes

- Category A's weighting is proportionate: the billing path is the system's
  purpose and carries the most properties and the only independent oracle.
- The Deliverer's crash window — the highest-risk area in `sut-analysis.md` — has
  three safety properties plus a dedicated reachability check to prove it was
  explored.
- The three SUT-side instrumentation points are all chokepoints, so instrumentation
  is small, localised, and shared across properties.
- Importing `piping/internal/job` into the workload prevents the quota-deducting
  state set from being duplicated and drifting — a correctness benefit for the
  assertions themselves.

## Open Questions

- Whether environment variables can vary **per timeline** or only per run. If
  only per run, `check-constraint-violation-unreachable` and the
  `SWEEP_AGE_BOUND`-near-`SEND_TIMEOUT` band need separate runs, which changes
  scheduling rather than feasibility. `(needs human input)`
