# spool-record-window-observed

## What led here

`charged-iff-spooled` is the catalog's headline safety property, and it is
exactly the kind of property that can pass for the wrong reason. If no timeline
ever places a fault between spool acceptance and the state write, the property
reports green having proven nothing.

This property exists to make that distinguishable.

## Code paths

The window is two statements wide:

- `internal/app/deliver.go:86` — `err = d.sender.Send(sendCtx, dest, ticketedDoc)`
  returns nil: the payload is at the printer
- `internal/app/deliver.go:88` — `return d.resolve(ctx, j.ID, job.PrintSent,
  DeliverySucceeded, nil)` → the guarded UPDATE to `print_succeeded`

Between them the process holds the only knowledge that the document printed, in
a local variable.

## Why a Sometimes rather than a Reachable

The interesting thing is not that line 87 executes — it always does on the happy
path. The interesting thing is that *a fault landed while the system was in that
state*. That is a semantic condition, which is what `Sometimes(cond)` is for.
`Reachable` on the line would be satisfied by every successful print and would
tell us nothing.

## What it buys

- Confirms the timeline explored the dangerous interleaving.
- Gives Antithesis a branch anchor: a `Sometimes` on a rare semantic state is an
  exploration hint, so marking this window makes the search more likely to
  revisit and vary it.
- Serves as a replay checkpoint when `charged-iff-spooled` does fail — the two
  assertions together localise the failure to this window rather than anywhere in
  delivery.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing (required): a SUT-side flag, set after `Send` returns nil and cleared
  after `resolve` commits, exported so the assertion can observe "a fault
  occurred while set". The same flag serves `sweeper-never-preempts-live-send`
  and `charged-iff-spooled`, so it is one instrumentation change supporting three
  properties.

## Open Questions

- None.
