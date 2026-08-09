# shutdown-leaves-jobs-recoverable

## What led here

Deploys are the most frequent process termination in production — far more common
than crashes. Every rolling update kills pods with prints in flight. The k8s
manifest uses `strategy: Recreate` for the database and the default rolling
update for the app.

## Code paths

- `cmd/piping/main.go:41` — `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)`
- `cmd/piping/main.go:231-243` — on `ctx.Done()`, `httpSrv.Shutdown` with a 10s
  timeout on a context detached via `WithoutCancel`
- `internal/app/submit.go:136` — `ctx = context.WithoutCancel(ctx)` **before**
  the store and delivery calls, so the job's work is deliberately decoupled from
  the client
- `internal/app/sweep.go:34-36` — the Sweeper exits on `ctx.Done()` without
  finishing its current pass

## What goes wrong

A job left in `quota_deducted` or `print_sent` is recoverable — the Sweeper will
reclaim it after `SWEEP_AGE_BOUND`. The property is that shutdown never produces
anything *outside* that set of recoverable states. Since the state column is an
enum and the only writes are guarded CAS statements, the theoretical risk is low;
the practical risk is a job whose delivery was killed after the payload reached
the printer, which is `charged-iff-spooled`'s territory viewed through the
lifecycle lens.

## Expensive to rediscover

- `WithoutCancel` means the delivery does not observe shutdown at all. It keeps
  running until the process dies or the send completes — `Shutdown` waits for the
  handler, but only until its own 10s cap. With `SEND_TIMEOUT=5s` and retries
  sharing that budget, a delivery normally fits; with a slower configuration it
  would be killed mid-send.
- The Sweeper is not in the `WaitGroup` (`main.go:231-243` waits only on the
  shutdown goroutine). A pass interrupted mid-batch simply stops; the remaining
  jobs wait for the next process to start.
- Nothing drains or refuses new submissions during shutdown — requests accepted
  in the final moments start deliveries that may not finish.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` after each restart, checking every job is
  either terminal or within the Sweeper's reach, and that the Sweeper does in
  fact resolve it within a bounded number of intervals.
- Reuses the `charged-iff-spooled` sink receipts to distinguish "recoverable and
  not printed" from "recoverable but actually printed".

## Open Questions

- **Does `httpSrv.Shutdown` wait for handlers that detached their context via
  `WithoutCancel`?** It waits for active handlers to return, and the delivery runs
  inside the handler, so normally yes — but the 10s cap can expire first, after
  which the process exits with the send in flight. That makes the answer
  configuration-dependent: with a `SEND_TIMEOUT` above roughly 10s, ordinary
  deploys start killing live sends.
  `(partial: Shutdown waits for active handlers to return, and the delivery runs inside the handler — but the 10s cap can expire first)`

### Investigation Log

#### Does `httpSrv.Shutdown` wait for handlers that detached their context via `WithoutCancel`?

- **Examined:** `cmd/piping/main.go:231-243` (the shutdown goroutine),
  `internal/app/submit.go:136` (`context.WithoutCancel`) and `:149` (the
  `Deliver` call, still inside the handler's call stack), Go's documented
  `http.Server.Shutdown` semantics.
- **Found:** `WithoutCancel` detaches the *context*, not the goroutine. The
  delivery still runs synchronously inside the handler, so the connection stays
  active and `Shutdown` waits for it. The detachment matters only for
  cancellation signals — the handler will not abort when the client disconnects
  or when the parent context is cancelled, which is the intended behaviour.
- **The cap is the real limit:** `Shutdown` is given a 10s context. When that
  expires it returns `ctx.Err()`, the goroutine logs, `run` returns, and the
  process exits with the delivery still in flight. So the answer is
  configuration-dependent: with `SEND_TIMEOUT` plus analysis time under ~10s a
  deploy drains cleanly; above it, ordinary deploys start killing live sends.
- **Not found:** any tracking of in-flight deliveries independent of the HTTP
  connection — nothing would let a future async delivery path be drained at all.
- **Conclusion:** mechanism resolved. What remains is a bound question (is 10s
  the right cap for this deployment's `SEND_TIMEOUT`?), which is a configuration
  decision rather than a code fact. Kept as `(partial: ...)`.
