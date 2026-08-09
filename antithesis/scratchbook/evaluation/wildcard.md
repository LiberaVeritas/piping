---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# Lens 4 — Wildcard

Covered territory: Lens 1 asks whether properties are Antithesis-shaped; Lens 2
asks whether the set is balanced against the risk map; Lens 3 asks whether each
can be checked in the planned topology. This lens starts where those end.

## 1. The catalog tests the system against itself

**Twenty of twenty-two properties use the database as ground truth.** They read
job rows and grant rows and check relationships among them. But PostgreSQL is
inside the blast radius: Antithesis will partition it, throttle it, and (if
enabled) kill it.

If a write is lost or misapplied, the property and the system observe the *same
wrong state* and agree. The catalog would report green through a class of failure
it was built to find.

The only property with a genuinely independent oracle is `charged-iff-spooled`
and its cluster, because the SMB receipt is produced by a different process in a
different container that the app cannot write to.

This is not a gap in the sense of a missing property — it is a structural
property of the whole catalog, and it argues for the workload maintaining a
**shadow ledger**: its own record of what it submitted and what it was told,
compared against the database rather than derived from it. That is a design
decision with real cost, so it belongs in front of a human.

## 2. Nobody is checking that users are separated

Lens 2 found this as a coverage gap; the wildcard framing is sharper. Every
property in the catalog is written from the perspective of *one user's* state
being self-consistent. A defect that mixed two users' data — a dropped `WHERE
user_id = $1`, a lock taken on the wrong row — produces a state that is
**perfectly self-consistent for each user individually** and wrong only in the
relationship between them.

The `FOR UPDATE` on `app_user` is per-user by design. If it ever locked the wrong
row, `no-overspend-under-concurrent-submit` would still pass for each user
examined alone.

This is the missing *perspective*: the catalog has no property that requires
looking at two users at once.

## 3. The measurement is upstream of everything and untested

The billing chain is:

```
PDF → gs subprocess → regex over stdout → (pages, colorPages) → cost → quota
```

The catalog begins its scrutiny at `cost`. Everything to the left is taken on
faith, and the left-most link is a regular expression against the stdout of a
subprocess, carrying a comment about a known Ghostscript output bug
(`analyzer.go:40-41`).

An under-counting regex is a systematic, silent discount applied to whichever
documents trigger it. Every downstream property confirms the wrong number is
handled consistently.

## 4. What the system says versus what it did — only checked once

`response-matches-recorded-state` is the only property comparing something the
*user was told* against something the system *recorded*. That one property exists
because a defect of exactly that shape was found in this codebase.

The same class exists elsewhere and is unchecked:

- The dashboard's remaining-quota number versus the derived balance.
- The history page's per-job state versus the job row.
- The result fragment's cost versus the job's stored cost.

`sut-analysis.md` names this class explicitly ("the codebase's risk is
concentrated in values crossing layer boundaries"), and the catalog covers one
instance of it.

## 5. Odd, without a clean category

**The `finally_`/`eventually_` distinction is unused.** The topology sketches an
`eventually_` command, but the catalog's single liveness property is written as a
mid-run `Sometimes`. For a system whose correctness story is "the sweeper fixes
it eventually", the absence of terminal liveness checks feels like an oversight,
though I cannot point to a specific property that is wrong because of it.

**The workload cannot represent the real usage pattern.** Real users submit one
document and wait, watching a spinner. The workload will submit in parallel from
one container. The single-user-serial pattern — where `SESSION_TTL` expiry,
browser disconnects mid-print, and resubmission after a confusing error all live
— is the *common* case and the hardest for a parallel driver to represent. The
`context.WithoutCancel` behaviour (the print continues after the browser leaves)
is specifically a single-user-behaviour property and no property covers it.

## Cross-cutting the lenses

Lens 1 wants to drop `admin-requires-staff-role` for poor fit; Lens 2 notes the
`idp` container is justified by a single property. Together these suggest a
cheaper alternative worth putting to the human: **drop the `idp` container, mint
session cookies in the workload using `ENCRYPTION_KEY`, and drop both
access-control properties.** That removes one container, one network link, and
two low-fit properties — at the cost of `provisioning-is-idempotent`, which Lens
1 argues is *underrated*. The trade is genuinely balanced, which is why it should
be a human's call rather than mine.
