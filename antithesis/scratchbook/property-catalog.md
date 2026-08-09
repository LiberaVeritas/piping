---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# Property Catalog — piping

Discovery run in single-agent mode: sequential passes over focuses 1–10, wildcard
pass (11) last. Focus provenance is noted per property as `[F<n>]`.

**27 properties across seven categories.** The organizing principle is the
system's central claim: **quota is spent if and only if the document printed**.
Category A attacks that claim directly; B and C attack the machinery that upholds
it.

Categories A–F came from discovery. **Category G was added after property
evaluation** (see `evaluation/synthesis.md`) to fill five gaps the discovery
focuses missed — most importantly that every original property examined a single
user in isolation, and that the catalog began its scrutiny downstream of the page
measurement that all billing depends on.

Focus 10 (Version Compatibility) yielded no properties: piping is a single binary
with no wire protocol, no client/server version skew, and no serialization
boundary between versions. Schema/code agreement is already covered by
`internal/postgres/sql_test.go`, which prepares every SQL statement in the
package against the live schema. Noted and moved on.

## Index and Priority

Priority reflects expected bug-finding value: **High** = a violation costs money
or correctness and the path is timing- or fault-sensitive; **Medium** = real
risk, narrower blast radius or narrower fault window; **Low** = worth pinning,
but largely covered by existing Go tests or guarding dead surface.

| Slug | Cat | Type | Priority |
|---|---|---|---|
| `charged-iff-spooled` | A | Safety | **High** |
| `quota-never-negative` | A | Safety | **High** |
| `no-overspend-under-concurrent-submit` | A | Safety | **High** |
| `response-matches-recorded-state` | A | Safety | **High** |
| `retry-never-double-spools` | A | Safety | **High** |
| `stored-cost-matches-rate-formula` | A | Safety | Low |
| `single-winner-guarded-transition` | B | Safety | **High** |
| `only-legal-state-transitions` | B | Safety | **High** |
| `timestamps-agree-with-state` | B | Safety | Low |
| `refunded-state-unreachable` | B | Reachability | Medium |
| `sweeper-never-preempts-live-send` | C | Safety | **High** |
| `non-terminal-jobs-eventually-resolved` | C | Liveness | **High** |
| `spool-record-window-observed` | C | Reachability | Medium |
| `shutdown-leaves-jobs-recoverable` | C | Safety | Medium |
| `analysis-terminates-within-bound` | D | Safety | Medium |
| `quota-correct-under-pool-exhaustion` | D | Safety | Medium |
| `destination-frozen-across-retries` | D | Safety | Medium |
| `every-job-has-authenticated-owner` | E | Safety | Medium |
| `admin-requires-staff-role` | E | Safety | Low |
| `readyz-reflects-db-reachability` | E | Safety | Medium |
| `provisioning-is-idempotent` | E | Safety | **High** |
| `check-constraint-violation-unreachable` | F | Reachability | Medium |
| `cross-user-quota-isolation` | G | Safety | **High** |
| `page-count-matches-document` | G | Safety | **High** |
| `service-recovers-after-fault` | G | Liveness | **High** |
| `disabled-destination-never-receives-job` | G | Safety | Medium |
| `dashboard-matches-stored-state` | G | Safety | Medium |

Priority is independent of a property's Open Questions: `charged-iff-spooled`
carries two unresolved questions and is still the highest-value property in the
catalog.

**Four High-priority properties are inert without node termination**, which is
disabled by default — see `deployment-topology.md` and Bias B3 in
`evaluation/synthesis.md`.

---

## A. Billing Integrity

The money path. A violation here either costs a student pages they never got or
gives away printing for free. Every property in this category is high-value
because the system's entire purpose is metered access.

### charged-iff-spooled — Quota is deducted exactly when the document reached the printer

| | |
|---|---|
| **Type** | Safety |
| **Property** | A job holds the user's quota if and only if its payload was accepted by the destination spool. |
| **Invariant** | `Always` in the workload: for every job it submitted, `state ∈ {quota_deducted, print_sent, print_succeeded}` (quota held) iff the fake SMB sink recorded a payload for that job id. Message: `"job holds quota iff its payload reached the spool"`. `Always` is correct because this must hold on every evaluation, for every job, with no exception — it is the system's defining guarantee. |
| **Antithesis Angle** | The window between `Sender.Send` returning nil (`deliver.go:86`) and the guarded `print_succeeded` write (`deliver.go:88`) is the target. Antithesis kills the app process, pauses it, or partitions it from PostgreSQL inside that window. It also explores the no-crash variant: delaying the send past `SWEEP_AGE_BOUND` so the Sweeper refunds a job whose document is printing. |
| **Why It Matters** | Both directions are real harm: refunding a printed job gives away pages, and charging an unprinted job takes a student's quota with nothing to show. `deliver_test.go:100` already asserts this in-process with a fake sender ("user charged iff printed must hold") — Antithesis extends it across process death and database faults, which the Go test cannot reach. |

**Open Questions:**

- Does `smbclient` exit 0 guarantee the spool durably accepted the job, or only that the connection succeeded? The property can only ever be as strong as this signal.
- Should a job whose send succeeded but whose record was lost be reconciled on restart, or is one-sided loss accepted?

### quota-never-negative — Derived remaining quota never goes below zero

| | |
|---|---|
| **Type** | Safety |
| **Property** | For every user, `SUM(grants) - SUM(cost of quota-deducting jobs)` is never negative. |
| **Invariant** | `Always`, evaluated by the workload after every operation, per user. Message: `"remaining quota is non-negative"`. `Always` because this is an invariant of stored state that must hold at every observation, not a state to reach. |
| **Antithesis Angle** | Concurrent submissions for one user under process and network faults; the row lock in `job_store.go:26` is the only thing serialising them, and `SET LOCAL lock_timeout = '5s'` means the guard can *expire* rather than block. Antithesis explores the interleaving where the lock times out under load. |
| **Why It Matters** | Negative quota means a user printed more than they were granted — the system failed at its one job. `TestConcurrentSubmitCannotOverspend` covers the single-process, no-fault case only. |

**Open Questions:**

- None.

### no-overspend-under-concurrent-submit — Concurrent submissions cannot exceed the grant

| | |
|---|---|
| **Type** | Safety |
| **Property** | When N submissions race for a user with remaining quota R, the total cost accepted never exceeds R. |
| **Invariant** | `Always`: the workload tracks the grant it provisioned and the costs it observed accepted, asserting `acceptedTotal <= granted`. Message: `"accepted cost never exceeds granted quota"`. Distinct from `quota-never-negative`: that one checks stored state, this one checks the *accept/reject decisions* the API returned, which is where the read-then-write race lives. |
| **Antithesis Angle** | This is the classic TOCTOU. The check and the insert are in one transaction under `FOR UPDATE`, but Read Committed plus a 5s `lock_timeout` leaves room: a lock acquisition that times out surfaces as an error, and Antithesis can starve the pool so acquisition is slow. |
| **Why It Matters** | The direct cause of revenue loss. A defect here was demonstrated in this codebase: `CheckQuotaAndStore` computed the correct decision but never returned it, so every over-quota submission reported success. |

**Open Questions:**

- Under `lock_timeout` expiry, does the submission fail closed (rejected) or is the error surfaced as a 500 that a client might retry into a double-spend? `(partial: the error propagates as a non-quota error and Submit returns without delivering, but retry behaviour is client-driven)`

### response-matches-recorded-state — What the user is told matches what was recorded

| | |
|---|---|
| **Type** | Safety |
| **Property** | A submission that renders "Sent to printer" corresponds to a job that was not recorded as `quota_insufficient`, and a submission that renders an insufficient-quota rejection corresponds to a job that was. |
| **Invariant** | `Always` in the workload: after each submit, compare the rendered result text against the job row's state. Message: `"submit result text agrees with recorded job state"`. **Requires a SUT change**: the result fragment carries no job identifier (`handlers.go:120-126` renders prose only, and `SubmitResult.JobID` is discarded), so the workload cannot correlate response to row under concurrent submission. Emitting a `data-job-id` attribute in the result template is sufficient. |
| **Antithesis Angle** | Less about fault injection than about a decision crossing a layer boundary — but faults widen it: a database error between the insert and the response, or a partial write, can desynchronise the two. |
| **Why It Matters** | This is the exact shape of the defect found in this codebase (the computed state was not propagated to the returned job, so the user saw "Sent to printer, N quota deducted" while the row said `quota_insufficient` and their balance never moved). A regression target with a demonstrated mechanism. |

**Open Questions:**

- None.

### stored-cost-matches-rate-formula — A job's recorded cost is derivable from its own page counts

| | |
|---|---|
| **Type** | Safety |
| **Property** | For every job row, `cost == ((num_pages - num_color_pages) + num_color_pages * COLOR_RATE) * copies`. |
| **Invariant** | `Always`, evaluated by the workload over every job it can read. Message: `"stored job cost matches the rate formula"`. |
| **Antithesis Angle** | **Not an exploration target.** `internal/quota/quota_test.go` already covers `Cost` exhaustively, and the remaining surface is one INSERT of an already-computed integer — there is no interleaving that makes them diverge. Included as a near-free tripwire that localises Cluster 2 failures to their input, not because fault injection helps. |
| **Why It Matters** | The cost column is the sole input to every quota computation. If it can disagree with the job's own page counts, every downstream balance is wrong and no other property will localise it. |

**Open Questions:**

- Is `COLOR_RATE` guaranteed stable for the lifetime of stored jobs? If it can change between deployments, historical rows will no longer satisfy the formula and the property needs a per-job rate snapshot.

### retry-never-double-spools — A retried send never prints twice

| | |
|---|---|
| **Type** | Safety |
| **Property** | For each job, the destination spool receives its payload at most once, even when `Sender.Send` is retried. |
| **Invariant** | `Always`: the fake SMB sink counts payloads per job id; the workload asserts every count is `<= 1`. Message: `"a job's payload is spooled at most once"`. Pair it with `Sometimes(a send was retried after a non-deadline error)` — the fault window is narrow, so without that companion a green result is indistinguishable from a retry path that never executed. |
| **Antithesis Angle** | Directly in Antithesis's wheelhouse. The retry loop (`deliver.go:85-105`) retries on any non-deadline error. If the printer accepted the payload and *then* the connection dropped, `smbclient` reports failure and the loop re-sends. Antithesis injects exactly that: a network fault after payload delivery but before acknowledgement. |
| **Why It Matters** | The user is charged once and the printer produces two copies — invisible to the system, visible to whoever picks up the paper. Nothing in the code deduplicates, and `print -` carries no idempotency key. |

**Open Questions:**

- Does `smbclient print -` offer any idempotency token or job identifier that would let a retry be deduplicated at the spool?
- Is a double print considered worse than a missed print by the operators? It determines whether the retry loop should exist at all.

---

## B. Job State Machine Integrity

The state machine is defined in Go and enforced only by compare-and-swap
`UPDATE`s. The database has an enum but no transition constraints, so every
guarantee in this category is application-level and unenforced at rest.

### single-winner-guarded-transition — At most one actor wins any given transition

| | |
|---|---|
| **Type** | Safety |
| **Property** | When multiple actors attempt the same guarded transition on one job, exactly one succeeds and the rest observe `ErrUnexpectedState`. |
| **Invariant** | `Always`: the workload races transitions on one job and asserts `winners == 1`. Message: `"exactly one actor wins a guarded job transition"`. |
| **Antithesis Angle** | The Deliverer and the Sweeper genuinely contend (`deliver.go:71`, `sweep.go:51`). Antithesis controls the interleaving and can suspend one mid-transaction. |
| **Why It Matters** | Two winners means a job's cost could be counted or refunded twice. The CAS is the only mechanism preventing it — there is no transition constraint in the schema. |

**Open Questions:**

- None.

### only-legal-state-transitions — No job ever takes a transition outside the declared graph

| | |
|---|---|
| **Type** | Safety |
| **Property** | Every observed state change for a job is an edge in `job.validTransitions`, plus the two entry points (`INSERT` as `quota_deducted` or `quota_insufficient`) and `MarkSent`'s `quota_deducted → print_sent`. |
| **Invariant** | `Always` placed **SUT-side** inside `UpdateJobState` and `MarkSent`, checking the attempted transition against the declared graph. Message: `"attempted job state change is a declared transition"`. A workload-side polling version was considered and rejected: two transitions between polls collapse into one observed change, so the legal sequence `quota_deducted → print_sent → print_succeeded` reads as an illegal `quota_deducted → print_succeeded` — a false-positive mode that would make the property untrustworthy. Both write sites are chokepoints, so the SUT-side version is small and exact. |
| **Antithesis Angle** | `MarkSent` (`job_store.go:88`) bypasses `ValidTransition` entirely — it hardcodes the guard in SQL rather than consulting the graph. Any future edit to the graph silently diverges from it. Faults that interleave Sweeper and Deliverer writes explore whether the guards actually cover every path. |
| **Why It Matters** | The quota-deducting set is defined by state. An unexpected edge (say `print_failed → print_succeeded`) silently re-charges a refunded job. |

**Open Questions:**

- Should `MarkSent` be expressed through `ValidTransition` so the graph is the single source of truth? `(partial: MarkSent's SQL guard is equivalent to the graph today, but is not derived from it)`

### timestamps-agree-with-state — completed_at and refunded_at match the job's state

| | |
|---|---|
| **Type** | Safety |
| **Property** | `completed_at` is non-null exactly when the job is in a terminal state, and `refunded_at` is non-null exactly when the job is `refunded`. |
| **Invariant** | `Always` over all job rows. Message: `"job timestamps agree with job state"`. |
| **Antithesis Angle** | The writes are conditional expressions inside the same `UPDATE` (`job_store.go:73-77`), so they cannot tear relative to the state — but the `INSERT` path sets `completed_at` via a separate `CASE` on `quota_insufficient`, and `MarkSent` sets neither. Faults that interrupt a multi-statement path can expose disagreement. |
| **Why It Matters** | These columns are the audit trail. If they disagree with state, reconciling a billing dispute after the fact is impossible. No schema constraint enforces the relationship. |

**Open Questions:**

- Is `quota_insufficient` intended to be terminal-with-timestamp at insert time? It is set there, which is consistent with `IsTerminal`, but the two code paths could drift independently.

### refunded-state-unreachable — The refunded state is never entered

| | |
|---|---|
| **Type** | Reachability |
| **Property** | No job ever reaches state `refunded` in the deployed system. |
| **Invariant** | `Unreachable` at the point where a transition to `job.Refunded` would be performed. Message: `"a job entered the refunded state"`. `Unreachable` rather than `Sometimes` because, on the current code, no path performs this transition — asserting unreachability documents the gap and fails loudly the moment someone wires it up. |
| **Antithesis Angle** | Minimal fault interaction; this is a structural claim about the code, and Antithesis's value is confirming it holds across all explored paths rather than only the ones a developer imagined. |
| **Why It Matters** | `refunded` is a declared terminal state with a legal edge from `print_succeeded`, dedicated write logic for `refunded_at`, and an index-visible column — but grep across all non-test sources finds no caller. Either an operator refund tool is missing, or the state machine advertises a capability the system lacks. If someone later adds the transition without adjusting the quota-deducting set, refunding a `print_succeeded` job silently returns quota. |

**Open Questions:**

- Is an operator refund path planned, or should the state and column be removed? `(needs human input)`

---

## C. Recovery, Sweeping, and Progress

The Sweeper is the only mechanism that reclaims quota from stuck jobs, and it is
the component most likely to interact badly with a live delivery. This category
is where Antithesis's timing control is most valuable.

### sweeper-never-preempts-live-send — The sweeper does not refund a job that is still being sent

| | |
|---|---|
| **Type** | Safety |
| **Property** | No job is transitioned to `print_failed` by the Sweeper while its `Sender.Send` is still in flight. |
| **Invariant** | SUT-side `Always` guarded by instrumentation: a flag set around the send call, asserted false at the Sweeper's transition point for that job id. Message: `"sweeper resolved a job whose send is still in flight"`. This needs SUT-side instrumentation — the window is invisible to a workload, which cannot see that a subprocess is mid-execution. |
| **Antithesis Angle** | The purest timing property in the system. `main.go:140` enforces `SWEEP_AGE_BOUND > SEND_TIMEOUT`, but the age is measured from `submitted_at` (set at INSERT), while the send begins only after destination selection and `MarkSent`. Antithesis delays the send or slows the database so the elapsed time crosses the bound with no crash required. |
| **Why It Matters** | The failure is silent and costs money: the Sweeper writes `print_failed`, quota is returned, the document prints anyway, and the Deliverer's `print_succeeded` write then fails the CAS and is swallowed as `DeliveryFailed, nil` (`deliver.go:120-123`). The user is told it failed, is refunded, and collects the printout. |

**Open Questions:**

- What margin above `SEND_TIMEOUT` actually makes this safe, given analysis time and lock waits are also inside the window? The current guard compares only two of the contributing durations.

### non-terminal-jobs-eventually-resolved — Every job eventually reaches a terminal state

| | |
|---|---|
| **Type** | Liveness |
| **Property** | A job does not remain in `quota_deducted` or `print_sent` indefinitely; the Sweeper resolves it. |
| **Invariant** | `Sometimes(a job the workload observed in a non-terminal state at time T is terminal at T+n, and the Sweeper — not the Deliverer — performed the transition)`, plus a `Reachable` at the Sweeper's "resolved stale job" branch (`sweep.go:59`). The condition must describe an observed **recovery event**, not the absence of stale jobs: "no job is older than X" is trivially true at the start of every timeline, before the workload has done anything, and would report the property satisfied without the Sweeper ever running. |
| **Antithesis Angle** | Kill the app mid-delivery, restart it, and confirm the Sweeper reclaims the quota. Also: kill the app *during* a sweep pass and confirm the next pass finishes the batch. Antithesis's quiet periods are what make the eventual claim checkable. |
| **Why It Matters** | This is the only path by which quota held by a crashed delivery is ever returned. If the Sweeper stalls, users silently lose quota with no error anywhere. |

**Open Questions:**

- The Sweeper takes a batch of `SWEEP_BATCH`; if stale jobs accumulate faster than a batch per interval, does it keep up? The property as stated would fail under sustained overload, which may be correct behaviour rather than a defect.

### spool-record-window-observed — The dangerous window is actually explored

| | |
|---|---|
| **Type** | Reachability |
| **Property** | The run exercises at least one fault landing between spool acceptance and the state write. |
| **Invariant** | `Sometimes(a fault occurred while the send-completed-but-unrecorded flag was set)` with SUT-side instrumentation bracketing `deliver.go:86-88`. Message: `"a fault landed between spool acceptance and the state write"`. This is a `Sometimes` on a meaningful semantic condition, not a line marker — it says the interesting state was reached, which is what makes `charged-iff-spooled` trustworthy. |
| **Antithesis Angle** | This is the exploration hint that tells Antithesis the window is worth branching in. Without it, a green `charged-iff-spooled` may mean only that the window was never hit. |
| **Why It Matters** | Guards against false confidence. A safety property that never had its dangerous path exercised has proven nothing. |

**Open Questions:**

- None.

### shutdown-leaves-jobs-recoverable — Graceful shutdown never strands quota

| | |
|---|---|
| **Type** | Safety |
| **Property** | After SIGTERM and the 10s drain, every job is either terminal or in a state the Sweeper will reclaim — never in a state no code path can advance. |
| **Invariant** | `Always` evaluated after a restart: every job read is in one of the six declared states, and any non-terminal one is older than the Sweeper's cutoff or becomes terminal within it. Message: `"no job is stranded outside the sweeper's reach after shutdown"`. |
| **Antithesis Angle** | Antithesis sends process faults during active deliveries. `main.go:231-243` drains HTTP with a 10s timeout, but in-flight deliveries run on `context.WithoutCancel` — they are not tracked by the WaitGroup and can be killed mid-send. |
| **Why It Matters** | Deploys are the routine case, not the exotic one. Every rolling update kills in-flight prints, so this path executes in production far more often than a crash. |

**Open Questions:**

- Does `httpSrv.Shutdown` wait for handlers that have detached their context via `WithoutCancel`? If not, a normal deploy can kill a send mid-flight. `(partial: Shutdown waits for active handlers to return, and the delivery runs inside the handler — but the 10s cap can expire first)`

---

## D. Resource and Subprocess Boundaries

Two subprocesses and one connection pool sit on the request path, none of them
covered by any existing test.

### analysis-terminates-within-bound — Ghostscript never runs unbounded

| | |
|---|---|
| **Type** | Safety |
| **Property** | A `CountPages` invocation always terminates within a bounded time. |
| **Invariant** | `Always`: the workload asserts every submit response arrives within a chosen ceiling. Message: `"submit completed within its time budget"`. A SUT-side `Unreachable` on a "gs exceeded budget" branch would be stronger, but no such branch exists today. |
| **Antithesis Angle** | Feed adversarial PDFs and slow the filesystem under the temp file. `CountPages` (`analyzer.go:60`) inherits only the request context; `http.Server` sets `ReadHeaderTimeout` but no `ReadTimeout` or `WriteTimeout` (`main.go:228`). |
| **Why It Matters** | A PDF that makes ghostscript spin holds a goroutine, a temp file, and eventually a connection, with no server-side bound. This is a denial-of-service path reachable by any authenticated student uploading one file. |

**Open Questions:**

- What is a defensible ceiling for a legitimate 100-page document on the deployment's CPU allocation? Without it the assertion threshold is arbitrary.

### quota-correct-under-pool-exhaustion — Quota stays correct when the pool is starved

| | |
|---|---|
| **Type** | Safety |
| **Property** | Under connection-pool exhaustion, quota accounting remains correct: no submission is both accepted and unrecorded. |
| **Invariant** | `Always`: the workload asserts that every submit which returned success has a corresponding job row, and that `quota-never-negative` still holds. Message: `"an accepted submission has a recorded job"`. |
| **Antithesis Angle** | `pgxpool` defaults to `max(4, NumCPU)` connections, and a submission holds one for the whole transaction *including* the `FOR UPDATE` wait — so a few concurrent submissions by one user can occupy the pool. Antithesis slows the database to widen the window. **Set `pool_max_conns` explicitly in the environment's `DATABASE_URL`**: the default is sized from the *node's* CPU count, so on a large host the pool may be too big to saturate and the property would never be exercised. |
| **Why It Matters** | Pool exhaustion under load turns into `lock_timeout` errors and 500s; the question is whether it can also produce a state where the user was charged without a job, or delivered without a record. |

**Open Questions:**

- `pgxpool` sizes the pool as `max(4, runtime.NumCPU())`, and `NumCPU` reports the *node's* CPUs, not the container's CPU limit — so the pool is sized off the host. Is a per-replica pool of that size safe against PostgreSQL's `max_connections`? `(needs human input)`

### destination-frozen-across-retries — Retries always target the recorded destination

| | |
|---|---|
| **Type** | Safety |
| **Property** | Every send attempt for a job targets the destination recorded in its `destination_id`. |
| **Invariant** | `Always`: the fake SMB sink records which destination received each payload; the workload asserts it matches the job's `destination_id`. Message: `"payload delivered to the job's recorded destination"`. |
| **Antithesis Angle** | Fail one destination repeatedly and confirm no attempt silently drifts to another. The balancer chooses once (`deliver.go:65`) and `MarkSent` freezes it before any send. |
| **Why It Matters** | The recorded destination is what the user is shown in their job history. If a retry could land elsewhere, the history lies about where the paper is — a real support problem in a building with printers on several floors. It also pins current behaviour: there is no failover, so a wedged printer consumes the whole retry budget. |

**Open Questions:**

- Is the absence of failover to another destination intentional? `(needs human input)`

---

## E. Access Control and Lifecycle

### every-job-has-authenticated-owner — No job exists without an authenticated submitter

| | |
|---|---|
| **Type** | Safety |
| **Property** | Every job row's `user_id` corresponds to a session the system issued; no job is created by an unauthenticated or cross-origin request. |
| **Invariant** | `Always`: the workload issues requests without sessions and with foreign `Origin`/`Sec-Fetch-Site` values and asserts the job count for those identities stays zero. Message: `"no job recorded for an unauthenticated submitter"`. |
| **Antithesis Angle** | Modest fault interaction; the value is in exploring odd request shapes and interleavings against `requireSession` and `checkOrigin` under load and partial failure. |
| **Why It Matters** | A job charges a named user's quota. Creating one without that user's authenticated request spends someone else's money. Both gates on this path had defects recently — `checkOrigin` compared `Sec-Fetch-Site` against a URL and rejected everything, which is the fail-closed direction, but it demonstrates the gate's logic was never exercised end to end. |

**Open Questions:**

- None.

### admin-requires-staff-role — Privileged pages never serve unprivileged sessions

| | |
|---|---|
| **Type** | Safety |
| **Property** | `GET /admin` returns a page only to sessions whose role ranks at or above `RoleStaff`. |
| **Invariant** | `Always`: the workload requests `/admin` with each role and asserts non-staff receive 403. Message: `"admin page served only to staff-or-higher sessions"`. |
| **Antithesis Angle** | **Effectively none, and the catalog should be honest about it.** `internal/web/handlers_test.go:TestAdminRequiresStaff` already covers all four roles against the real mux with real sealed cookies, and no timing, concurrency, or partial-failure condition changes the outcome. The one Antithesis-adjacent angle is revocation lag over `SESSION_TTL`, which needs clock faults — off by default. Retained as near-free regression pinning, not as an exploration target. |
| **Why It Matters** | `/admin` is currently a stub, so present-day impact is low — but the gate is the template every future privileged endpoint will copy, and `sessionFrom` returning a zero-value session on a missing context makes the default `RoleNone`, i.e. fail-closed. Worth pinning before the page does anything. |

**Open Questions:**

- With no server-side session store, a demoted user keeps staff access until `SESSION_TTL` expires. Is that acceptable? `(needs human input)`

### readyz-reflects-db-reachability — The readiness probe never lies

| | |
|---|---|
| **Type** | Safety |
| **Property** | `/readyz` returns 200 only when the database is actually reachable. |
| **Invariant** | `Always`: while the database is partitioned, the workload asserts `/readyz` is not 200. Message: `"readyz reported ready while the database was unreachable"`. |
| **Antithesis Angle** | Exactly the "health reporting accuracy" attack surface. Antithesis partitions the app from PostgreSQL and checks the probe within the 2s timeout window (`handlers.go:176`). |
| **Why It Matters** | Kubernetes routes traffic on this signal. A probe that reports ready during a database outage sends users to a replica that will 500 every request, and during a rolling update it can green-light killing the last working pod. |

**Open Questions:**

- Should `/readyz` also verify `gs` and `smbclient` availability? They are verified only at startup, so a broken toolchain post-start still reports ready.

### provisioning-is-idempotent — Repeated logins never inflate a grant

| | |
|---|---|
| **Type** | Safety |
| **Property** | Provisioning a user repeatedly, including concurrently, never increases their total granted quota beyond one grant per entitled semester. |
| **Invariant** | `Always`: the workload logs the same identity in repeatedly and asserts `SUM(semester_grant.amount)` is unchanged after the first. Message: `"repeat provisioning did not change total granted quota"`. |
| **Antithesis Angle** | Stronger than the existing Go coverage suggests. `TestGrantedEqualsEntitled` is a `rapid` test against a **fake store** — it covers entitlement arithmetic, not the database. The untested surface is concurrent logins racing `EnsureGrant`'s `ON CONFLICT DO NOTHING` against `EnsureSemester`'s `ON CONFLICT DO UPDATE SET id = EXCLUDED.id` (a construction that exists solely to make `RETURNING` fire on conflict) with the database partitioned mid-sequence. That is squarely Antithesis-shaped and runs on every single login. |
| **Why It Matters** | Provisioning runs on *every* login (`handlers.go:158`), so this path executes constantly. Double-granting is free quota; a spurious failure blocks login entirely. `TestGrantedEqualsEntitled` covers the pure logic against a fake store, not the database's conflict handling. |

**Open Questions:**

- `Provision` logs and continues when `EnsureGrant` fails, returning success. Is a partially-provisioned user acceptable, given they can then print with fewer pages than entitled?

---

## F. Configuration and Impossible States

### check-constraint-violation-unreachable — The database never rejects a job on a CHECK

| | |
|---|---|
| **Type** | Reachability |
| **Property** | No submission is ever rejected by a PostgreSQL check-constraint violation; the application validates before it inserts. |
| **Invariant** | `Unreachable` at the `23514` branch of `mapPostgresError` (`store.go:25`). Message: `"a job insert violated a database check constraint"`. `Unreachable` is right: reaching it means application-level validation and schema-level validation disagree, which is a defect regardless of which one is wrong. |
| **Antithesis Angle** | Antithesis varies configuration across timelines. With `COLOR_RATE=0` and an all-colour document, `Rates.Cost` returns 0, which violates `cost_positive` — a crash path opened by a config value alone, with no fault required. |
| **Why It Matters** | The branch exists, which means someone anticipated it; nothing validates that it cannot happen. A user hitting it gets a 500 and the generic "we could not confirm your job's status" message, which is the worst message in the catalog because it tells them to go check their history. |

**Open Questions:**

- Are `COLOR_RATE=0` or negative rates configurations anyone would deploy? If they are illegal, `main.go` should reject them at startup rather than leaving the branch reachable.

---

---

## G. Cross-Cutting and Read Path

Added after property evaluation. These five fill gaps the discovery focuses
missed: every original property examined one user's state in isolation, began its
scrutiny downstream of the page measurement, and checked that the system fails
safe without checking that it recovers.

### cross-user-quota-isolation — One user's activity never affects another's

| | |
|---|---|
| **Type** | Safety |
| **Property** | A submission, grant, or job transition for user A never changes user B's remaining quota or job history. |
| **Invariant** | `Always`: the workload drives at least two identities concurrently, snapshots each user's balance and job-id set, and asserts that operations attributed to A leave B's snapshot unchanged except where B itself acted. Message: `"a user's quota changed without that user acting"`. |
| **Antithesis Angle** | Concurrent submissions by different users under faults, exercising the per-user `FOR UPDATE` on `app_user` (`job_store.go:26`). If that lock were ever taken on the wrong row, or a `WHERE user_id = $1` dropped, the resulting state is **self-consistent for each user examined alone** — which is precisely what every other property does. |
| **Why It Matters** | A dropped user predicate is the most common SQL defect class, and this system's entire data model is per-user filtering: `RemainingQuota`, `JobsForUser`, `JobsWithDestinationForUser`, and the quota check all key on `user_id`. No other property in the catalog requires looking at two users at once, so this failure class is currently invisible. |

**Open Questions:**

- None.

### page-count-matches-document — Billing measures the document it was given

| | |
|---|---|
| **Type** | Safety |
| **Property** | For every submitted document, the `num_pages` and `num_color_pages` recorded equal the corpus's known ground truth for that document. |
| **Invariant** | `Always`: the workload submits from a fixed corpus with known page and colour counts and asserts the stored values match. Message: `"recorded page counts match the submitted document"`. |
| **Antithesis Angle** | Throttle the app so ghostscript is slow or killed mid-render; feed PDFs that produce unusual `inkcov` output. The parser is a regular expression over subprocess stdout (`analyzer.go:41`) carrying a comment about a Ghostscript bug where output "sometimes [has] no newlines" — an acknowledged upstream quirk with no test behind it. |
| **Why It Matters** | This is the first link in the billing chain, and every other billing property begins *downstream* of it. An under-counting parse is a silent, systematic discount applied to whichever documents trigger it, and `stored-cost-matches-rate-formula` would confirm the wrong number was handled consistently. Cost cannot be more correct than the measurement it multiplies. |

**Open Questions:**

- Does a partially-consumed `gs` stdout (process killed mid-write) yield a short match list rather than a parse error? `parseInkcov` counts one page per regex match and returns success for any non-zero count, so a truncated stream would undercount silently rather than fail.

### service-recovers-after-fault — The system comes back

| | |
|---|---|
| **Type** | Liveness |
| **Property** | After a fault that makes the service unavailable is healed, the service returns to accepting submissions. |
| **Invariant** | `Sometimes(after a period in which /readyz returned 503, a later submission succeeds end to end)`, checked during an `ANTITHESIS_STOP_FAULTS` quiet period so recovery is observable without ending the branch. Message: `"service accepted a submission after recovering from an outage"`. |
| **Antithesis Angle** | Partition the app from PostgreSQL, heal it, and confirm `pgxpool` reconnects and the submit path works again. `main.go` pings the database at startup and fails hard if it is unreachable, but there is no reconnect logic in the app — recovery is entirely delegated to the pool. |
| **Why It Matters** | The catalog's safety properties are all satisfied by a system that fails safe and **stays** failed. `readyz-reflects-db-reachability` asserts the probe does not lie during an outage; nothing asserts the outage ends. For a service students rely on during deadline weeks, "never wrong, permanently down" is not an acceptable outcome. |

**Open Questions:**

- Does `pgxpool` recover from a partition without process restart in all cases, or are there error classes that poison the pool? The app has no reconnect path of its own, so the answer bounds what this property can assert.

### disabled-destination-never-receives-job — Disabled targets are never used

| | |
|---|---|
| **Type** | Safety |
| **Property** | No payload is ever spooled to a destination that was disabled at the time of selection, and no job is accepted for a disabled queue. |
| **Invariant** | `Always`: the workload toggles `enabled` on queues and destinations mid-run (via the custom fault) and asserts the SMB sink recorded no receipt for a disabled destination, and that submissions to a disabled queue are rejected with `queue.ErrUnavailable`. Message: `"payload spooled to a disabled destination"`. |
| **Antithesis Angle** | Configuration change under load — a named attack surface in the SUT analysis. `GetQueue` and `DestinationsForQueue` are read per request with no caching, so a toggle takes effect between the queue check (`submit.go:90-98`) and destination selection (`deliver.go:65`). That gap is the target. |
| **Why It Matters** | Disabling a printer is the operators' only lever when one jams or is removed. If a job in flight can still be routed to it, the lever does not work, and the paper is produced somewhere nobody is watching — while the user is told it succeeded. |

**Open Questions:**

- Should a job already in `print_sent` for a destination that is subsequently disabled be allowed to complete? The property as stated only constrains selection, not completion, because the payload has already left.

### dashboard-matches-stored-state — The user is shown what the database holds

| | |
|---|---|
| **Type** | Safety |
| **Property** | The remaining quota and job history rendered to a user match the corresponding stored rows. |
| **Invariant** | `Always`: the workload fetches `/` and `/jobs`, parses the rendered values, and compares them against direct SQL for the same user. Message: `"rendered dashboard disagrees with stored state"`. |
| **Antithesis Angle** | Modest fault sensitivity; the value is in exercising the read path at all, under concurrent writes that make stale or torn reads possible. |
| **Why It Matters** | Students read the dashboard far more often than they print. `JobsWithDestinationForUser` (`user_store.go:57-79`) scans seven columns **positionally** into a struct after a LEFT JOIN — a hazard the codebase acknowledges by testing column order explicitly, with a failure message that asks "column order?". A silent reordering shows every user wrong costs and states. This is the same "values crossing a layer boundary" class as `response-matches-recorded-state`, which is the one defect class this codebase has actually demonstrated. |

**Open Questions:**

- `formatTimeSince` computes against `time.Now()` on the app; under clock faults the rendered age can go negative. Is that a display defect worth asserting, or noise?

---

## Assumptions

- The Antithesis environment substitutes a **fake SMB sink** for real printers,
  recording per-job payload receipts. Several category-A properties are
  uncheckable without it, since `smbclient`'s exit status is otherwise the only
  signal.
- The environment substitutes a **fake IdP**, since every route but the health
  and static endpoints requires an OIDC session.
- The workload can read the database directly to evaluate invariants over stored
  state.
- `COLOR_RATE` is fixed for a timeline.

## Open Questions

- Is multi-replica operation intended? If yes, a whole cluster of
  Sweeper-versus-Sweeper and cross-replica quota properties becomes relevant, and
  the topology in `deployment-topology.md` needs a second app container.
  `(needs human input)`
- Should properties cover the OIDC callback path (`handleAuthCallback`) beyond
  provisioning idempotency? It is entirely untested today, but its faults are
  mostly the IdP's rather than piping's.
