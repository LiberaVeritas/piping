# provisioning-is-idempotent

## What led here

Provisioning runs on **every login**, not just the first
(`internal/web/handlers.go:158`). It is therefore one of the hottest write paths
in the system, and its correctness rests on two `ON CONFLICT` clauses.

## Code paths

- `internal/web/handlers.go:157-163` — `handleAuthCallback` calls
  `prov.Provision(ctx, u, semester.Current(time.Now()))` on every callback
- `internal/app/provision.go:30-55` — `Provision`: `EnsureUser`, then for each
  entitled semester `EnsureSemester` followed by `EnsureGrant`
- `internal/postgres/user_store.go:10-19` — `EnsureUser`: `INSERT ... ON CONFLICT
  (id) DO NOTHING`
- `internal/postgres/quota_store.go:47-60` — `EnsureSemester`: `ON CONFLICT (id)
  DO UPDATE SET id = EXCLUDED.id RETURNING default_quota` — the no-op update
  exists solely so `RETURNING` fires on conflict
- `internal/postgres/quota_store.go:62-72` — `EnsureGrant`: `ON CONFLICT
  (user_id, semester_id) DO NOTHING`
- `schema.sql:27` — `one_grant_per_semester UNIQUE (user_id, semester_id)`, the
  real enforcement

## What goes wrong

- **Double grant** — free quota, and it compounds with every login.
- **Spurious failure** — a `23505` surfacing as an error would break login
  entirely, since `Provision` runs before the session is issued.

## Expensive to rediscover

- `Provision` deliberately **does not fail** when a grant fails: it logs and
  continues (`provision.go:47-52`), returning nil. So a user can log in
  successfully with a partially-applied set of grants and simply have less quota
  than they are entitled to, with no user-visible error. Re-logging in will retry
  the missing grants, which is why the soft failure is defensible — but only
  because provisioning is idempotent, making this property load-bearing for that
  design decision.
- `EnsureSemester` returns the **existing** `default_quota` on conflict, not the
  one just computed. So the first login of any user in a semester fixes that
  semester's quota for everyone thereafter. `QuotaFor` is deterministic per code,
  so this is stable — unless `DEFAULT_QUOTA` changes between deployments, in
  which case the first-writer's value persists.
- The grant amount is snapshotted at first grant; later changes to
  `semester.default_quota` do not propagate to existing grants.
- `semester.Current(time.Now())` is evaluated per login, so a login at a semester
  boundary can create the new semester's grant — the rollover is a real timing
  event, driven by wall-clock time on the app container.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist. Note that
`internal/app/provision_test.go` (`TestGrantedEqualsEntitled`) is a `rapid`
property test against a **fake store** — it covers the entitlement logic, not the
database's conflict handling, which is what this property targets.

- Missing: workload-side `Always` — repeated and concurrent logins for one
  identity leave `SUM(semester_grant.amount)` unchanged after the first.
- Missing (useful): `Sometimes` on "a grant insert hit the unique conflict",
  confirming concurrent logins actually raced.

## Open Questions

- **Is a partially-provisioned user acceptable?** `Provision` returns success
  after logging a failed grant, so the user logs in with less quota than they are
  entitled to and no error anywhere they can see. If that is intended (recoverable
  on next login), the property is only about not *over*-granting. If it is not,
  there is a second property here about eventual completeness of entitlement.
