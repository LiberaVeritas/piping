# admin-requires-staff-role

## What led here

`GET /admin` is the only role-gated route. The page is currently a stub, so the
present-day impact is low — but the gate is the pattern every future privileged
endpoint will copy, and the role it checks comes from a client-held cookie with
no server-side revocation.

## Code paths

- `internal/web/server.go:60` — `appMux.Handle("GET /admin",
  s.requireRole(user.RoleStaff, s.handleAdmin))`, with a `// TODO` above it
- `internal/web/server.go:102-113` — `requireRole`: compares `RoleRank(sess.Role)`
  against `RoleRank(requiredRole)`, 403 otherwise
- `internal/user/user.go:106-118` — `RoleRank`: `RoleNone` 0, `RoleUser` 1,
  `RoleStaff` 2, `RoleAdmin` 3; the `default` returns 0
- `internal/user/user.go:91-104` — `RoleFromGroups` maps IdP groups to roles at
  login
- `internal/session/session.go:61-82` — the role is sealed into the cookie at
  issue time with `MaxAge` = `SESSION_TTL`
- `internal/web/handlers.go:73-75` — `handleAdmin` renders a static page

## What goes wrong

An unprivileged session reaching a privileged page. Today that means seeing an
empty admin template; once the page does anything, it means whatever that is.

## Expensive to rediscover

- Ranking is ordinal, so `RoleAdmin` passes a `RoleStaff` gate. Any future gate
  that wants *exactly* staff, not admin, cannot express it with `requireRole`.
- `RoleRank`'s `default: return 0` means an unrecognised role string fails
  closed. Combined with `sessionFrom` returning a zero session, every unknown
  path lands on `RoleNone`.
- **There is no server-side session store.** A role change at the IdP does not
  propagate until the cookie expires. A demoted staff member keeps staff access
  for up to `SESSION_TTL` (default 60 minutes), and there is no logout endpoint
  that clears anything server-side — `session.Clear` only overwrites the cookie
  in the user's own browser.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` — `/admin` returns 403 for every session below
  staff rank.
- Low fault sensitivity; the value is in exploring cookie lifetime and role
  transitions rather than injected faults.

## Open Questions

- **With no server-side session store, a demoted user keeps staff access until
  `SESSION_TTL` expires. Is that acceptable?** If not, the fix is a server-side
  session or role revalidation per request, which changes this property from a
  request-scoped check into one about propagation delay — a materially different
  assertion. `(needs human input)`

### Investigation Log

#### With no server-side session store, a demoted user keeps staff access until `SESSION_TTL` expires. Is that acceptable?

- **Examined:** `internal/session/session.go` in full — `Manager` holds only
  `seal`, `ttl`, and `log`; there is no store, no revocation list, and no
  identifier that could key one. `FromRequest` validates the seal and the `Exp`
  claim and nothing else. `Clear` (`:84-95`) writes an expiring cookie to the
  *current response*, so it can only log out the browser making the request.
  Also examined `internal/web/server.go` for a logout route (none exists) and
  `internal/user/user.go:91-104` for where roles originate.
- **Found:** the design is fully stateless by construction. The role is sealed
  into the cookie at `Issue` time (`session.go:61-82`) from
  `RoleFromGroups(userInfo.Groups)` at login, and is never re-read. Default
  `SESSION_TTL` is 60 minutes (`main.go:135`), so that is the propagation delay
  for any privilege *reduction*. A privilege *increase* also waits for re-login,
  which is the benign direction.
- **Not found:** any requirement document, threat model, or comment stating an
  acceptable revocation window. Nothing in the repo indicates whether staff
  demotion is a real operational event.
- **Conclusion:** the mechanism and the exact window (up to `SESSION_TTL`) are
  fully established; whether 60 minutes of stale privilege is acceptable is a
  policy decision for the owner, not a fact in the code. Tagged `(needs human
  input)`.
