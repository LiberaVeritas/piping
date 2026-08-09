# retry-never-double-spools

## What led here

The retry loop retries on *any* error that is not `context.DeadlineExceeded`.
`smbclient` reporting failure does not mean the printer rejected the payload — it
can mean the payload was accepted and the connection then dropped. Nothing
deduplicates.

## Code paths

- `internal/app/deliver.go:85-105` — the loop. On a non-deadline error it logs,
  checks the attempt count, waits `retryWait` (500ms, hardcoded at
  `main.go:168`), and re-sends **the same payload to the same destination**.
- `internal/smb/sender.go:26-40` — `Send` builds a fresh `smbclient ... -c "print
  -"` each call, streaming `payload` on stdin. No job identifier, no idempotency
  token.
- `internal/printticket/printticket.go:104-119` — `XCPT` wraps the PDF in PJL.
  `JobName` is `piping-job-<id>` (`:42`), which is stable across retries — the
  closest thing to an identity the spool sees, but nothing uses it to deduplicate.

## What goes wrong

The printer produces two copies. The user is charged once. The system records
one job, successfully. Every internal invariant holds; the only evidence is
paper.

## Expensive to rediscover

- `sendCtx` is created **once before the loop** (`deliver.go:79`), so
  `MAX_SEND_ATTEMPTS` attempts share a single `SEND_TIMEOUT` budget. With the
  defaults (5 attempts, 5s) a slow printer yields far fewer than five attempts.
  This makes the double-print window narrower than the config suggests, but it
  also means the retry knob does not do what its name implies.
- `context.DeadlineExceeded` short-circuits without retrying (`:90-92`), so the
  timeout path is the *safe* one — the dangerous path is a fast failure after
  acceptance.
- `JobName` in the print ticket is a genuine deduplication opportunity if the
  spool honours it.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: fake SMB sink counting payloads per `JobName` / job id, plus a
  workload-side `Always` that no count exceeds 1.
- Missing (useful): SUT-side `Sometimes` on "a send was retried after a
  non-deadline error", so a green result is distinguishable from an unexercised
  retry path.

## Open Questions

- **Does `smbclient print -` offer any idempotency token the spool honours?** If
  the `JobName` from the print ticket is sufficient, the fix is cheap and the
  property becomes a regression guard. If not, at-most-once delivery is
  unachievable at this boundary and the honest question is which direction to
  fail in.
- **Is a double print worse than a missed print for the operators?** This decides
  whether the retry loop should exist. Retrying trades a rare double-print for a
  rare missed print; the current code silently chooses the former.
