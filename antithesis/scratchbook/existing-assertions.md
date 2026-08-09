---
sut_path: /Users/hk/piping
commit: e0c4683ad4a2a055ce3caa1a13fd172e154cb47d
updated: 2026-08-09
external_references: []
---

# Existing Antithesis SDK Assertions

## Summary

**None.** The codebase has no Antithesis instrumentation.

## Scan Method

Searched all `*.go`, `go.mod`, CI YAML, and `Containerfile` for:

- The string `antithesis` (case-insensitive) — no matches
- Assertion functions and macros: `assert_always`, `assert_sometimes`,
  `assert_reachable`, `assert_unreachable`, `AlwaysOrUnreachable`,
  `SometimesSome` — no matches
- Any local assertion helper (`func assert`, imports of an `assert` package) — no matches

`go.mod` has no `github.com/antithesishq/antithesis-sdk-go` dependency.

## Implication for Property Work

Every property in `property-catalog.md` starts from zero instrumentation. Each
evidence file therefore states plainly what would have to be added, and no
evidence file may describe an assertion as "already present."

The codebase does have a substantial native test suite (Go `testing` plus
`pgregory.net/rapid` property tests) whose invariants overlap several catalog
properties. Those are Go-test assertions, not Antithesis SDK assertions — they
run in `go test`, not inside an Antithesis timeline, and they do not guide
Antithesis's search. See `sut-analysis.md` ("Existing Test Strategy") for what
they already cover, which is the basis for judging where Antithesis adds value
rather than duplicating.

## Assumptions

- The SDK would be added as `github.com/antithesishq/antithesis-sdk-go`, whose
  assertions are no-ops outside an Antithesis environment and are therefore safe
  to leave in production builds.

## Open Questions

- None.
