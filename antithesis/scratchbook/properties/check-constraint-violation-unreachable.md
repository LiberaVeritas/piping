# check-constraint-violation-unreachable

## What led here

`mapPostgresError` has a dedicated branch for check-constraint violations
(`internal/postgres/store.go:25-26`), which means someone anticipated the case.
Nothing validates that it cannot happen. Working backwards from the schema's
CHECK constraints to the application's validation found a gap that configuration
alone can open.

## The gap

- `schema.sql:67` — `CONSTRAINT cost_positive CHECK (cost > 0)`
- `internal/quota/quota.go:13-15` — `Cost(pages, colorPages) = (pages -
  colorPages) + colorPages * ColorRate`
- `cmd/piping/main.go:92-95` — `COLOR_RATE` is read with `envInt` and **not
  validated**; any integer is accepted, including 0 and negatives

With `COLOR_RATE=0` and a document where every page is colour, `pages -
colorPages == 0` and `colorPages * 0 == 0`, so cost is 0. `Submit` has no
zero-cost gate (`internal/app/submit.go:120` computes it and moves on), so the
INSERT runs and PostgreSQL rejects it.

Negative `COLOR_RATE` makes it worse: cost can go negative, which also violates
the constraint, and if it ever *didn't*, a negative cost would credit quota.

## What the user sees

`mapPostgresError` wraps it, `Submit` returns it as a non-quota error
(`submit.go:144`), `mapSubmitError` falls through to the default branch
(`handlers.go:212-215`), and the user gets a 500 with:

> "Something went wrong and we could not confirm your job's status. Check your
> job history before submitting again."

That is the worst message in the catalog — it tells the user the system does not
know what happened, for a case where nothing happened at all.

## Other constraints in the same family

The remaining CHECKs are covered by application gates, which is why this property
is stated over the whole class rather than just `cost_positive`:

- `num_pages_positive` — guarded by `submit.go:107` (`pages <= 0` → `ErrUnreadable`)
  and `analyzer.go:79-81`
- `color_pages_within_total` — guarded by `submit.go:107` (`colorPages > pages`)
- `color_pages_nonneg` — guarded by the same line
- `copies_positive` — guarded by `submit.go:115`
- `amount_positive` on grants — `QuotaFor` returns only 0, 250, 500, or 1000

So exactly one constraint has no application-side counterpart, and it is
reachable through configuration rather than input.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: `Unreachable` in the `23514` branch of `mapPostgresError`
  (`store.go:25`). Placing it there covers the whole class in one assertion and
  fires regardless of which constraint was violated.
- The real fix is startup validation of `COLOR_RATE >= 1` in `main.go`, alongside
  the existing `SWEEP_AGE_BOUND > SEND_TIMEOUT` check — that file already
  establishes the pattern of rejecting incoherent configuration at boot.

## Open Questions

- **Would anyone deploy `COLOR_RATE=0`?** Free colour printing is a plausible
  policy decision (a department absorbing the cost), which makes this more than
  theoretical — the operator would set 0 expecting "colour costs the same as
  mono" and instead get 500s on any all-colour document. If 0 is meant to be
  legal, the fix is a floor on cost rather than a floor on the rate.
