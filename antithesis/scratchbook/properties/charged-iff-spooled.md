# charged-iff-spooled

## What led here

The codebase states this invariant in its own test suite:
`internal/app/deliver_test.go:100` fails with `"BILLING: outcome %v but
DeductsQuota(%v)=%v — user charged iff printed must hold"`. That is a claimed
guarantee, and per the skill's discipline it is a claim to test, not a verified
fact. The Go test verifies it in-process against a scripted sender; it cannot
reach process death, database faults, or a real subprocess boundary.

## Code paths

- `internal/app/deliver.go:64-106` — `Deliver`. The ordering that matters:
  - `:70` `MarkSent` — `quota_deducted → print_sent`, records `destination_id`
  - `:86` `d.sender.Send(...)` — spawns `smbclient`, payload leaves the process
  - `:88` `d.resolve(..., PrintSent, DeliverySucceeded, nil)` → guarded UPDATE to
    `print_succeeded`
- `internal/smb/sender.go:26-40` — `Send`. Exit 0 is the only success signal.
- `internal/job/job.go:67-73` — `DeductsQuota`: `quota_deducted`, `print_sent`,
  `print_succeeded` hold quota. `print_failed` and `refunded` do not.
- `internal/postgres/quota_store.go:11-21` — `RemainingQuota` derives the balance
  from that state set.

## The window

Between `:86` returning nil and `:88` committing, the document is at the printer
and the database still says `print_sent`. Anything that prevents `:88` from
committing — process kill, database partition, a lost CAS race with the Sweeper —
leaves a printed document that the system will later treat as failed and refund.

The reverse direction also exists: `MarkSent` at `:70` commits *before* the send.
A crash between `:70` and `:86` leaves a job in `print_sent` holding quota with
nothing printed, which the Sweeper correctly reclaims.

## What goes wrong

- **Refund a printed job** — free printing, undetectable from inside the system.
- **Charge an unprinted job** — the student loses pages; recoverable only by the
  Sweeper, and only if it runs.

## Expensive to rediscover

`resolve` swallows a lost CAS: `deliver.go:120-123` maps `ErrUnexpectedState` to
`return DeliveryFailed, nil`. So when the Sweeper wins, the Deliverer reports
failure *with no error*, and the user is shown "Printing failed … your quota was
refunded" while their document is coming out of the printer. No log line marks
this as anomalous — it is `log.Warn("job found already resolved")`.

## Instrumentation

Cross-referenced against `existing-assertions.md`: the codebase has **no
Antithesis SDK assertions**, so everything below is missing and must be added.

- Missing: a workload-side `Always` comparing job state against the fake SMB
  sink's per-job receipt log.
- Missing: SUT-side flag bracketing `deliver.go:86-88` — needed by
  `spool-record-window-observed`, and useful as a replay anchor here.
- The property is only as strong as the sink's receipt semantics; see open
  questions.

## Open Questions

- **Does `smbclient` exit 0 guarantee the spool durably accepted the job, or only
  that the connection succeeded?** If exit 0 can precede durable acceptance, then
  "spooled" is not observable at this boundary and the strongest honest property
  is charged-iff-*attempted*, which is materially weaker. If it does guarantee
  acceptance, the fake sink's receipt is a faithful stand-in and the property is
  exactly as stated.
- **Should a job whose send succeeded but whose record was lost be reconciled on
  restart?** Today nothing reconciles; the Sweeper assumes non-terminal means
  not-printed. If reconciliation is intended, the property needs a third
  post-restart state ("printed, awaiting reconciliation") and the Sweeper needs a
  way to distinguish it.
