---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# Lens 2 — Coverage Balance

Is this the right *set* of properties, judged against `sut-analysis.md`?

## Assertion-type distribution

| Type | Count | Properties |
|---|---|---|
| `Always` | 18 | most of the catalog |
| `Sometimes` | 2 | `non-terminal-jobs-eventually-resolved`, `spool-record-window-observed` |
| `Unreachable` | 2 | `refunded-state-unreachable`, `check-constraint-violation-unreachable` |
| `Reachable` | 0 standalone | one is embedded inside a `Sometimes` entry |
| `AlwaysOrUnreachable` | 0 | — |

**The portfolio is 82% safety.** One genuine liveness property for a system whose
entire recovery story rests on a background sweeper is disproportionate. The
skill's own guidance flags exactly this shape: "a catalog with 15 `Always`
assertions and no `Sometimes` assertions is probably missing liveness
properties."

## Risk areas from the SUT analysis with no property

Walking `sut-analysis.md` section by section:

### Gap — the analyzer's measurement is never validated (F11, Wildcard)

The analysis flags that cost derives from a **regex over ghostscript's stdout**
(`analyzer.go:41`, with a comment citing a Ghostscript bug about missing
newlines). Every billing property in the catalog takes `num_pages` and
`num_color_pages` as *given* and checks arithmetic downstream of them.

Nothing checks the measurement itself. A regex that under-counts pages on certain
PDFs undercharges every user who submits one, and `stored-cost-matches-rate
-formula` would happily confirm the wrong cost is internally consistent.

The workload controls the PDF corpus, so ground truth is available — this is
cheaply checkable and currently absent.

### Gap — cross-user isolation is assumed, never asserted

Quota is derived per `user_id`; every query filters on it. No property states
that **one user's activity cannot affect another's balance or history**. A
missing `WHERE user_id = $1` — the single most common SQL defect class — would
be invisible to all 22 properties, because each one evaluates a single user's
state in isolation and would find it self-consistent.

`internal/postgres/store_test.go` has `TestConcurrentSubmitDifferentUsersDoNot
BlockEachOther`, which tests *liveness* across users, not isolation.

### Gap — recovery liveness

`readyz-reflects-db-reachability` asserts the probe never lies while the database
is down. Nothing asserts the system **comes back**: that after a partition heals,
`/readyz` returns 200 again and submissions succeed. A system that fails safe and
stays failed satisfies every current property.

This is the natural home for `ANTITHESIS_STOP_FAULTS` mid-run checks, which the
topology mentions but no property consumes.

### Gap — the read path

Students look at their dashboard far more often than they print.
`JobsWithDestinationForUser` (`user_store.go:57-79`) does a LEFT JOIN and scans
seven columns positionally into a struct — a hazard the codebase itself
acknowledges by testing column order explicitly
(`TestJobWithDestinationForUserScans`, whose failure message asks "column
order?"). No property asserts that what the user is shown matches the stored
rows.

### Gap — disabled queues and destinations

`deployment-topology.md` proposes a custom fault toggling `queue.enabled` and
`destination.enabled` mid-run, and nothing consumes it. `GetQueue` and
`EnabledDestinations` are read per request with no caching, so this is a genuine
configuration-change-under-load surface. The invariant — a job is never sent to a
disabled destination — has no property.

## Over-investment

Category B (state machine) has four properties for a mechanism that is one SQL
pattern repeated twice. `timestamps-agree-with-state` guards columns **no code
reads**, and `refunded-state-unreachable` guards a state **no code enters**. Both
are cheap, so this is a mild observation rather than a problem — but two of four
properties in the category concern dead surface.

## Component distribution

| Container | Properties targeting it |
|---|---|
| `piping` | 22 (all) |
| `postgres` | ~8 (via faults) |
| `smb-sink` | 4 |
| `idp` | 1 |

Concentrated in the SUT, which is appropriate — but `idp` earns its container on
the strength of a single property (`provisioning-is-idempotent`) plus access.
That is a topology cost worth noting to the human.

## Passes

- Category A is proportionate to its risk: the billing path is the system's
  purpose and carries six properties.
- The Deliverer's crash window (the highest-risk area in the analysis) has three
  properties plus a dedicated reachability check.
- Both directions of the charged-iff-printed equivalence are covered.

## Uncertainties

- Whether the read path deserves properties depends on relative harm: a wrong
  dashboard is confusing, a wrong charge is theft. The allocation is a judgment
  call, not a defect.
