# every-job-has-authenticated-owner

## What led here

A job row charges a named user's quota. The `user_id` comes from the session
cookie and nothing else — there is no server-side check that the user exists at
submit time beyond the foreign key, and the foreign key is satisfied by
provisioning at login.

Both gates protecting this path had defects recently, which is why it is worth
asserting rather than assuming.

## Code paths

- `internal/web/server.go:69` — `root.Handle("/", s.requireSession(appMux))`
- `internal/web/server.go:75-95` — `requireSession`: no valid cookie means a 303
  to the IdP, never a handler invocation
- `internal/web/server.go:115-138` — `checkOrigin`, wrapping `POST /job` only
- `internal/web/handlers.go:106` — `Username: sessionFrom(r.Context()).Sub`
- `internal/web/server.go:97-100` — `sessionFrom` returns a **zero-value
  session** when the context has none, which yields `Sub == ""`
- `schema.sql:49` — `user_id text NOT NULL REFERENCES app_user (id)`

## What goes wrong

A job created without an authenticated request spends someone's quota. The
foreign key is the last line of defence: `Sub == ""` would fail the FK, since no
`app_user` row has an empty id — so the failure mode is a 500 rather than a
misattributed charge. That is fail-closed, but it depends on an accident (no
empty-string user) rather than a check.

## Expensive to rediscover

- `sessionFrom` swallowing the missing-context case is deliberate and makes every
  downstream check fail closed (`RoleRank(RoleNone) == 0`), but it means a
  handler mounted outside `requireSession` would not fail loudly — it would
  quietly act as an anonymous user.
- The `checkOrigin` gate compares `Sec-Fetch-Site` against an expected token and,
  as a second layer, `Origin` against the origin derived from
  `OIDC_REDIRECT_URI`. A defect where the header was compared against the URL
  made *every* upload 403 — the fail-closed direction, but it demonstrates the
  gate had never been exercised end-to-end against a real browser.
- Only `POST /job` is origin-checked. `GET` routes are not, which is correct for
  safe methods, but there is no other state-changing endpoint today to compare
  against.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` — after issuing unauthenticated and
  cross-origin submissions, the job count attributable to those identities
  remains zero.
- Missing (useful): SUT-side `Unreachable` on "a job insert was attempted with an
  empty `user_id`", which converts the accidental FK protection into an explicit
  one.

## Open Questions

- None.
