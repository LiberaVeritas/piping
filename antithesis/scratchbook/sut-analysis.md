---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# SUT Analysis — piping

Discovery was run in single-agent mode: sequential passes over attention focuses
1–11, then the wildcard pass last. Focus provenance is noted per finding as
`[F<n>]`.

## Summary

`piping` is a university print-quota service: students authenticate via OIDC,
receive a per-semester page grant, upload a PDF, and it is rendered to a print
ticket and pushed to an SMB printer. One Go binary, one PostgreSQL database, two
spawned subprocesses (`gs`, `smbclient`), one external IdP.

The system's central claim is a **billing invariant**: a user's quota is spent if
and only if their document printed. Every interesting failure mode is a way to
break that equivalence in one direction or the other.

## Architecture and Data Flow [F1]

Single process, `cmd/piping/main.go`. Layering is clean and adapter-driven:

```
web (handlers, views, html/template + htmx)
  └─ app (Submitter, Deliverer, Sweeper, Provisioner)
       └─ adapters: postgres.Store · ghostscript.Analyzer · smb.Sender
                    oidc.Client · session.Manager · seal.Sealer
```

Routing (`internal/web/server.go:54`): a root mux serves `/static/`, `/healthz`,
`/readyz`, `/auth/callback` unauthenticated; everything else is wrapped in
`requireSession`. `GET /admin` additionally requires `RoleStaff` via
`requireRole`. `POST /job` is wrapped in `checkOrigin`.

**The print path is synchronous inside the HTTP request** — the browser holds the
connection through a ghostscript spawn and every `smbclient` attempt:

```
POST /job
  → MaxBytesReader(maxBytes + 1MiB) → ParseMultipartForm(8MiB) → io.ReadAll
  → Submit: size / magic / queue-enabled / page / copies gates
  → Analyzer.CountPages       (writes temp file, spawns `gs -sDEVICE=inkcov`)
  → cost = Rates.Cost(pages, colorPages) * copies
  → ctx = context.WithoutCancel(ctx)          ← job outlives the client
  → CheckQuotaAndStore        (tx: FOR UPDATE → derive remaining → INSERT job)
  → Deliver: pickDestination → MarkSent → printticket.XCPT → Sender.Send (retry)
           → resolve (guarded UPDATE to terminal state)
  → render result fragment
```

`Sweeper.Run` is the only background goroutine (`main.go:211`), ticking every
`SWEEP_INTERVAL`.

## State Management and Persistence [F2]

All durable state is in PostgreSQL (`schema.sql`): `app_user`, `semester`,
`semester_grant`, `queue`, `destination`, `job`.

**Quota is derived, never stored.** Both `RemainingQuota` and the in-transaction
check in `CheckQuotaAndStore` compute:

```
SUM(semester_grant.amount) - SUM(job.cost WHERE state ∈ {quota_deducted, print_sent, print_succeeded})
```

Consequences: there is no balance row to corrupt, but *every* state transition
that moves a job in or out of that three-state set is a financial event. The set
is defined in Go (`job.QuotaDeductingStateNames`), not in the schema.

The job state machine (`internal/job/job.go:79`) lives entirely in Go. **The
database enforces no transition legality** — `job_state` is an enum, and the only
guard is the application's compare-and-swap `WHERE id = $1 AND state = $5`.

Session state is client-side only: an AES-256-GCM sealed cookie
(`internal/seal`), with the label as additional authenticated data. There is no
server-side session store, therefore **no revocation** — a role change or logout
elsewhere cannot invalidate an outstanding cookie before `SESSION_TTL`.

In-flight state: a job sitting in `quota_deducted` or `print_sent` is holding the
user's quota. If the process dies there, only the Sweeper reclaims it.

`Analyzer.CountPages` writes the upload to `os.CreateTemp` and removes it via
`defer`; process death leaks those files.

## Concurrency Model [F3]

- One goroutine per HTTP request; one long-lived Sweeper goroutine.
- **Quota serialization is per-user, in the database**: `SELECT id FROM app_user
  WHERE id = $1 FOR UPDATE` under `SET LOCAL lock_timeout = '5s'`
  (`job_store.go:20-31`). Isolation is pgx's default, Read Committed.
- **Job transitions are compare-and-swap**: guarded `UPDATE ... WHERE state = $5`,
  with `RowsAffected() == 0` surfacing as `job.ErrUnexpectedState`.
- No mutexes or shared mutable Go state on the request path; package-level vars
  are read-only.
- The Deliverer and the Sweeper contend for the same rows. `Deliver` explicitly
  anticipates losing (`deliver.go:71-74`, "delivery intercepted before send") and
  `resolve` anticipates it again (`deliver.go:120-123`).
- `pgxpool` defaults to `max(4, NumCPU)` connections. A submission holds a
  connection for the whole transaction *including* the `FOR UPDATE` wait, so
  concurrent submissions by one user can occupy the pool for up to
  `lock_timeout`.

## Claimed Safety Guarantees [F4]

Stated in comments, tests, or schema. **These are claims to test, not verified
facts.**

| Claim | Source |
| --- | --- |
| "read verify and update atomically" — quota check and insert are one unit | `job_store.go:12` |
| "must be atomic to avoid double spend" | `submit.go:29` |
| "lock the user row so concurrent txn on same user blocks" | `job_store.go:24` |
| "Non-success means print_failed with quota back by the time we respond" | `handlers.go:124` |
| "resolved by sweep; quota returned" | `deliver.go:73` |
| "user charged iff printed must hold" | `deliver_test.go:100` |
| `cost > 0`, `num_pages > 0`, `num_color_pages <= num_pages`, `copies > 0`, `amount >= 0` | `schema.sql:63-67`, `:26` |
| One grant per user per semester | `schema.sql:27` |
| A sealed OIDC state blob cannot be replayed as a session | `seal_test.go` label separation |

## Claimed Liveness Guarantees [F5]

| Claim | Source |
| --- | --- |
| Stale non-terminal jobs are eventually resolved to `print_failed` | `sweep.go:43-61` |
| Sends are retried up to `MAX_SEND_ATTEMPTS`, bounded by `SEND_TIMEOUT` | `deliver.go:85-105` |
| `SWEEP_AGE_BOUND` must exceed `SEND_TIMEOUT` so the sweeper cannot race delivery | `main.go:140-145` (hard error, plus a warning below 2×) |
| HTTP shutdown completes within 10s | `main.go:237` |
| `/readyz` reflects database reachability within 2s | `handlers.go:176` |

## Failure and Degradation Modes [F8]

- **Analyzer**: a `gs` exit error becomes `pdf.ErrUnreadable` → 422; a non-exit
  failure → 500. `CountPages` has **no timeout of its own** — it inherits the
  request context, which at that point is still cancellable by the client.
- **Sender**: `smbclient` runs under `sendCtx`, created **once before the retry
  loop** (`deliver.go:79`). All attempts therefore share a single `SEND_TIMEOUT`
  budget; `MAX_SEND_ATTEMPTS` is only reachable if attempts are fast.
  `context.DeadlineExceeded` short-circuits without retrying.
- **Deliverer losing a race** is treated as success-with-`DeliveryFailed` and a
  nil error, deliberately.
- **The send happens before the state update.** `Sender.Send` returning nil means
  the spool accepted the document; the `print_sent → print_succeeded` write
  happens after. Any failure, crash, or lost race between those two points leaves
  a printed document recorded as not-printed.

## External Dependencies [F9]

| Dependency | Failure handling |
| --- | --- |
| PostgreSQL (`pgxpool`) | Pinged at startup, fatal if down. No app-level retry; pool reconnects. |
| `gs` binary | `VerifyDevice` at startup with a 5s timeout; spawned per submission. |
| `smbclient` + remote SMB printers | Spawned per attempt; NT_STATUS codes extracted for logs. |
| OIDC IdP | Discovery at startup, falling back to three explicit endpoint env vars. `http.Client` timeout 10s. No retry, no circuit breaker. |

## Product Context [F10]

Users are students; the money is page quota. Ranked by user-visible harm:

1. Charged but not printed (quota lost, nothing came out).
2. Printed but not charged (service gives away pages).
3. Quota display disagrees with reality.
4. Cannot print at all (the `checkOrigin` defect fixed at
   `e0c4683`'s working tree was exactly this).

`/admin` is a stub — `handleAdmin` renders a page with no data. Low harm.

## Existing Test Strategy [F7]

Substantial and unusually good for a project this size — this matters because it
determines where Antithesis *adds* value rather than duplicating.

Covered by Go tests, including `pgregory.net/rapid` property tests:

- `internal/postgres` against a **real PostgreSQL**: column-order scan tests,
  concurrent submission cannot overspend, exactly one racer wins a transition,
  and a rapid state machine asserting derived quota equals `granted - spent`.
- `internal/app`: a rapid state machine over the Deliverer asserting
  charged-iff-printed and terminality; Submitter gate table; Sweeper batch
  behaviour.
- `internal/web`: handler-level auth, CSRF, form validation, template execution.
- `internal/job`, `queue`, `quota`, `pdf`, `printticket`, `seal`, `semester`,
  `session`, `user`: pure-function and round-trip coverage.

**Not covered anywhere**, and this is the Antithesis-shaped gap:

- `internal/ghostscript`, `internal/smb`, `internal/oidc`, `cmd/piping` have no
  tests at all — every subprocess and network boundary is untested.
- Process death at any point (notably between a successful send and the state
  write).
- Database unavailability *mid-request* (only startup failure is handled).
- Connection-pool exhaustion, `lock_timeout` expiry.
- Clock skew between the app and the database.
- The Sweeper racing a live Deliverer against a real database — the app tests use
  fakes, and the DB tests do not run the Sweeper.
- More than one replica.

## Unproven Assumptions [F11]

These are conditions the developers never had to test, and are the most
productive targets.

1. **`gs` always terminates.** No per-analysis timeout, and `http.Server` sets
   only `ReadHeaderTimeout` (`main.go:228`) — no `ReadTimeout` or `WriteTimeout`.
   A PDF that makes ghostscript spin holds a request goroutine and a temp file.
2. **`smbclient` exit 0 means the document printed.** It means a spool accepted
   it. The charged-iff-printed claim is unfalsifiable at this boundary; the
   system can only ever assert charged-iff-spooled.
3. **`SWEEP_AGE_BOUND > SEND_TIMEOUT` is sufficient margin.** The guard compares
   only those two values, but the age is measured from `submitted_at`, which is
   set at INSERT — *before* destination selection and the whole send. Analysis
   time, scheduling delay, and lock waits are not in the budget.
4. **`cost > 0` always holds.** `Rates.Cost = (pages - colorPages) + colorPages *
   ColorRate`. With `COLOR_RATE=0` and an all-colour document, cost is 0, which
   violates the `cost_positive` CHECK and surfaces as a 500. A config value alone
   opens a crash path.
5. **One replica.** Nothing in the code assumes it, but nothing tests otherwise;
   every replica runs its own Sweeper.
6. **App and database clocks agree.** The Sweeper computes `cutoff =
   time.Now().Add(-ageBound)` in Go and compares it to `submitted_at`, set by
   Postgres `now()`.
7. **IdP-supplied enrolment codes are well-formed.** `QuotaFor` does arithmetic
   on the code; a malformed code like `202603` silently yields the default quota
   and renders as `"Semester 202603"`.
8. **Grant amounts are stable.** An amount is snapshotted from
   `semester.default_quota` at first grant; later changes to the semester do not
   propagate, by design but undocumented.

## Bug History and Density [F6]

No issue tracker was in scope (user's answer: "just this directory"), and the git
history is short. Two defects were nevertheless confirmed from primary evidence
during the session that produced this analysis, both with demonstrations rather
than reports:

- **`CheckQuotaAndStore` never assigned the computed state to the returned job**,
  making `quota.ErrInsufficient` unreachable, so over-quota submissions returned
  success and were delivered. Class: *a computed decision not propagated to the
  value the caller inspects.*
- **`checkOrigin` compared the `Sec-Fetch-Site` header against a URL** derived
  from `OIDC_REDIRECT_URI`, so every upload returned 403. Class: *a configured
  value compared against a value from a different domain.*

Both are fixed in the working tree. They are recorded here because the classes
generalize: the codebase's risk is concentrated in *values crossing layer
boundaries*, not in its algorithms.

Suspiciously quiet: `internal/ghostscript` and `internal/smb` have no bug history
and no tests. That combination usually means undertested, not correct.

## Wildcard [F12]

Findings the other eleven lenses would not have produced:

- **The retry budget is not what it appears.** `sendCtx` is created once, outside
  the loop, so `MAX_SEND_ATTEMPTS=5` with `SEND_TIMEOUT=5s` does not mean five 5s
  attempts — it means five attempts sharing 5s. The knob suggests durability it
  does not provide, and the interaction is invisible in either constant alone.
- **Destination selection happens once, and is then frozen by `MarkSent`.**
  Retries re-send to the *same* destination, so the load balancer offers
  distribution but no failover. A single wedged printer consumes the whole retry
  budget for every job routed to it.
- **`refunded` is dead.** It is a valid state with a legal transition
  (`PrintSucceeded → Refunded`) and dedicated write logic (`refunded_at`,
  `job_store.go:75-77`), but **no code path ever performs it** — confirmed by
  grep across non-test sources. A whole terminal state and column are unreachable
  in production. Either an operator tool is missing, or the state machine claims
  a capability the system does not have.
- **`context.WithoutCancel` decouples the job from the client, with no cap.** A
  user who closes the tab is still charged and still prints; the goroutine
  continues with no overall deadline (see assumption 1).
- **Cross-cut — the sweeper's refund and the printer's paper are not
  coordinated.** The only thing separating "quota returned" from "paper produced"
  is a duration comparison in `main.go`. No crash is required: a sufficiently
  slow send crosses `SWEEP_AGE_BOUND`, the Sweeper writes `print_failed`, the
  document prints anyway, and the guarded CAS then makes the Deliverer's
  `print_succeeded` write fail silently and return `DeliveryFailed, nil`. The
  user is told it failed, is refunded, and holds the printout. This is reachable
  by *timing alone*, which is precisely Antithesis's strength.
- **Several read paths are dead code** (`JobsForQueue`, `JobsForQueueForUser`,
  `JobsForUser`, `SpentQuota` — no callers outside the package and its tests),
  yet `sql_test.go` keeps their SQL valid. Harmless, but it means the SQL surface
  is larger than the reachable surface.

## Assumptions

- The deployment is the one in `k8s/`: one app replica, one PostgreSQL, an
  NFS-backed volume.
- `smbclient`'s exit status is the only available signal about print success;
  no printer-side confirmation channel exists.
- Antithesis would target the containerized deployment, not the `go test` suite.

## Open Questions

- Is `refunded` intended to be reachable — is an operator refund tool planned, or
  should the state and `refunded_at` column be removed? (needs human input)
- Is multi-replica operation intended? It changes whether Sweeper-vs-Sweeper
  contention is worth testing. (needs human input)
- What is the real-world upper bound on `smbclient` latency for these printers?
  It determines whether assumption 3 is a live risk or a theoretical one.
  (needs human input)
