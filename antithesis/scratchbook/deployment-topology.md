---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# Deployment Topology

Five containers. Each one is justified below; two of them (`smb-sink`, `idp`)
exist only because properties in the catalog are otherwise uncheckable, and the
cost of dropping each is stated explicitly.

```text
   +------------------+
   | workload         |  test driver + assertions
   +--------+---------+
      |     |      \
 HTTP |     | SQL   \ SMB (read receipts)
      v     v        v
+----------+  +----------+  +------------+
| piping   |->| postgres |  | smb-sink   |
| (SUT)    |  |          |  | (samba)    |
+----+-----+  +----------+  +------------+
     |   \                        ^
     |    \______ SMB (print) ____|
     |
     v  HTTP (OIDC)
+----------+
| idp      |
+----------+
```

## Components

### `postgres` — dependency

| | |
|---|---|
| **Image** | Official `postgres:18-alpine` — identical to `k8s/app.yaml` |
| **Runs** | `postgres`, schema loaded from `schema.sql` at init |
| **Connections** | inbound from `piping` and `workload` |
| **Replicas** | 1 |

Separate container so it can be partitioned from `piping` independently — that
network link is the fault path for `readyz-reflects-db-reachability` and
`quota-correct-under-pool-exhaustion`.

Load `schema.sql` via `/docker-entrypoint-initdb.d/`. Do **not** apply
`migration/01_import_from_tepid.sql`; the legacy import is out of scope and would
add uncontrolled data.

Consider setting `pool_max_conns` explicitly in `DATABASE_URL` — see
`quota-correct-under-pool-exhaustion`, where investigation found the pool is
sized from the *node's* CPU count.

### `piping` — service (SUT)

| | |
|---|---|
| **Image** | Adapt the existing `Containerfile` — it is already multi-stage alpine with `ghostscript` and `samba-client` installed |
| **Runs** | the `piping` binary, one process |
| **Connections** | → `postgres` (5432), → `smb-sink` (445), → `idp` (HTTP); inbound from `workload` (8080) |
| **Replicas** | 1 (see open question) |

Changes needed to the existing `Containerfile`:

1. Add `github.com/antithesishq/antithesis-sdk-go` and build with the Antithesis
   Go instrumentation so thread-pausing faults and coverage-guided exploration
   work.
2. Keep `USER piping`. Note that the image sets a *named* user, so any Kubernetes
   `runAsNonRoot` is a separate concern and does not apply here.
3. Nothing else — the production image already carries both subprocesses, which
   is exactly the fidelity we want.

### `smb-sink` — dependency

| | |
|---|---|
| **Image** | New: Alpine + `samba`, configured with one printer share |
| **Runs** | `smbd` |
| **Connections** | inbound from `piping` (printing) and `workload` (reading receipts) |
| **Replicas** | 1 |

**Why this container is mandatory.** Six of the seven Category A and C properties
compare quota state against whether the document actually reached a printer.
Without an observable spool there is nothing to compare against, and
`charged-iff-spooled` — the catalog's headline property — degenerates into
"quota matches what the app believes", which is a tautology.

**Design.** Configure the share with a `print command` that appends a receipt
line (job name, byte count, timestamp) to a file in a second, readable share, and
then discards the spool file. The workload reads receipts with `smbclient`, so:

- one process in the container,
- one protocol on the wire,
- **the receipt is written by `smbd`'s own print pipeline**, which means "receipt
  exists" is exactly "the spool accepted the job".

That last point directly addresses the open question on `charged-iff-spooled`
about what `smbclient` exit 0 means: in this environment the two are pinned
together by construction, so the property is checkable against the strong reading
rather than the weak one. It does not answer what a *real* Xerox spool does —
that remains a question for the operators.

`printticket.FromJob` sets `JobName = piping-job-<id>` (`printticket.go:42`),
which gives receipts a job identity without any SUT change. `retry-never-double
-spools` counts receipts per job name.

Separate container so the link from `piping` can be partitioned *after* payload
delivery — the exact fault that makes a retry double-print.

### `idp` — dependency

| | |
|---|---|
| **Image** | New: small Go OIDC stub, or `ghcr.io/navikt/mock-oauth2-server` |
| **Runs** | one HTTP process serving `/.well-known/openid-configuration`, `/authorize`, `/token`, `/userinfo` |
| **Connections** | inbound from `piping`; the workload drives login through `piping` |
| **Replicas** | 1 |

Every route except `/healthz`, `/readyz`, `/static/` and the callback is behind
`requireSession`, so without an IdP the workload cannot reach the application at
all.

`main.go:186` prefers discovery and falls back to three explicit endpoint
variables, so the stub must serve the discovery document or the environment must
set `OIDC_AUTHORIZATION_ENDPOINT`, `OIDC_TOKEN_ENDPOINT`, and
`OIDC_USER_INFO_ENDPOINT`. Serving discovery is closer to production.

The stub must let the workload control `groups`, `faculty`, and enrolment claims
per identity — `RoleFromGroups` and `EligibleForQuota` read them, and
`provisioning-is-idempotent` and `admin-requires-staff-role` need to vary them.

**The container we could drop, and what it costs.** The session cookie is sealed
with `ENCRYPTION_KEY`, which the environment sets — so the workload could mint
valid session cookies directly and skip login entirely, removing this container
and one network link. That would sacrifice `provisioning-is-idempotent` (whose
whole subject is the callback path), weaken `every-job-has-authenticated-owner`
to a check of `requireSession` rather than of real authentication, and remove
`admin-requires-staff-role`'s ability to test roles as the IdP actually assigns
them. Recommendation: keep the IdP; note the shortcut as a fallback if the stub
proves expensive to build.

### `workload` — client

| | |
|---|---|
| **Image** | New: Go, with `smbclient` and the Antithesis Go SDK |
| **Runs** | emits `setup_complete` once `piping` answers `/readyz`, then stays alive |
| **Connections** | → `piping` (HTTP), → `postgres` (SQL, for invariant checks), → `smb-sink` (SMB, for receipts) |
| **Replicas** | 1 |

Go, so it can import `piping/internal/job` and `piping/internal/quota` and check
invariants against the same definitions the SUT uses — notably
`QuotaDeductingStateNames`, which defines the quota-deducting state set and would
otherwise be duplicated and able to drift.

Test template at `/opt/antithesis/test/v1/piping/`. Sketch:

- `parallel_driver_submit` — authenticate, upload PDFs of varied page and colour
  mixes, record `(job id, expected cost, rendered result text)`
- `parallel_driver_login` — repeated and concurrent logins for one identity
  (`provisioning-is-idempotent`)
- `anytime_check_invariants` — the `Always` assertions over stored state
- `eventually_quota_reconciles` — the terminal liveness check for
  `non-terminal-jobs-eventually-resolved` and the settled form of
  `charged-iff-spooled`
- `helper_pdfs/` — a corpus: valid mono, valid colour, all-colour, oversized,
  zero-page, malformed, and one crafted to be slow to render
  (`analysis-terminates-within-bound`). The `helper_` prefix keeps Antithesis
  from treating it as a command.

Mid-run liveness (checking the Sweeper reclaimed quota without ending the branch)
should use `ANTITHESIS_STOP_FAULTS` rather than `eventually_`, since the run
should continue afterwards.

## Fault Requirements

Two fault classes this catalog depends on are **commonly disabled by default** and
must be confirmed with the tenant before the run is meaningful.

| Fault | Needed by | Status |
|---|---|---|
| **Node termination** | `charged-iff-spooled` (crash half), `spool-record-window-observed`, `shutdown-leaves-jobs-recoverable`, `non-terminal-jobs-eventually-resolved` | **Disabled by default — must be enabled.** Without it, the crash window is never exercised and these properties pass vacuously. |
| **Clock jitter** | `sweeper-never-preempts-live-send` (app/database skew), `provisioning-is-idempotent` (semester rollover at a boundary) | **Commonly disabled — confirm.** Both properties remain partly testable without it. |
| Network partitions | `readyz-reflects-db-reachability`, `retry-never-double-spools`, `quota-correct-under-pool-exhaustion` | Default |
| Node throttling | `analysis-terminates-within-bound`, `sweeper-never-preempts-live-send` (slow the app until the sweep window is crossed with no crash) | Default |
| Thread pausing | `single-winner-guarded-transition`, `no-overspend-under-concurrent-submit` | Default, requires SUT instrumentation (planned above) |

**Custom fault worth building:** toggle `queue.enabled` and `destination.enabled`
in the database mid-run. Both are read per request (`GetQueue`,
`DestinationsForQueue`) and nothing caches them, so this exercises configuration
change under load — a named attack surface — without restarting anything.

## Configuration Dimensions

Vary across timelines:

- `COLOR_RATE` — including `0`, which is the trigger for
  `check-constraint-violation-unreachable`
- `SWEEP_AGE_BOUND` relative to `SEND_TIMEOUT` — the startup guard permits values
  barely above, which is where `sweeper-never-preempts-live-send` lives
- `MAX_SEND_ATTEMPTS` and `SEND_TIMEOUT` — they share one budget, so the
  interesting region is small `SEND_TIMEOUT` with large `MAX_SEND_ATTEMPTS`
- `SESSION_TTL` — short values make `admin-requires-staff-role`'s revocation
  window observable within a run

## SDK Selection

Go SDK (`antithesis-sdk-go`) in both `piping` and `workload`. The workload needs
it to emit assertions; the SUT needs it for the three SUT-side instrumentation
points identified in the catalog (the delivery-window flag, the Sweeper
preemption check, and the `Unreachable` markers), plus coverage instrumentation
so thread-pausing and guided exploration work.

## Assumptions

- Antithesis runs the containerized deployment, not the Go test suite.
- `smbd` can be configured with a print command that produces a durable receipt;
  if not, the sink becomes a purpose-built minimal SMB responder, which is
  considerably more work and should be scoped before committing.
- The workload may read the database directly. If that is considered too
  privileged a view, every stored-state property would need an equivalent
  observation through the web UI, which is weaker and in some cases impossible
  (`timestamps-agree-with-state` has no UI surface at all).

## Open Questions

- **Is multi-replica operation intended?** One `piping` replica is assumed. A
  second would enable Sweeper-versus-Sweeper contention and cross-replica quota
  races — a meaningful expansion of the state space, and one the current
  single-`FOR UPDATE` design should handle but has never been tested against.
  `(needs human input)`
- Is node termination enabled for this tenant? Four properties are vacuous
  without it. `(needs human input)`
