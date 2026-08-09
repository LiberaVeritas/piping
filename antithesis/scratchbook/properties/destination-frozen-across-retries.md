# destination-frozen-across-retries

## What led here

The load balancer chooses a destination once, before any send, and `MarkSent`
persists that choice in the same statement that moves the job to `print_sent`.
Every retry then reuses the local variable. There is no failover.

## Code paths

- `internal/app/deliver.go:65` — `dest, err := d.pickDestination(ctx, j.QueueID)`
- `internal/app/deliver.go:130-152` — `pickDestination`: filter enabled
  destinations, load the queue's policy, build a balancer, `Choose` one
- `internal/queue/queue.go:66-73` — `uniformBalancer.Choose` picks uniformly at
  random via `rand.IntN`
- `internal/app/deliver.go:70` — `MarkSent(ctx, j.ID, dest.ID)` writes
  `destination_id` **before** any send
- `internal/app/deliver.go:85-105` — the retry loop closes over `dest`; nothing
  re-picks

## What goes wrong

Two distinct concerns, which is why this property is worth stating even though
the current behaviour is correct:

1. **The history would lie.** `JobsWithDestinationForUser`
   (`internal/postgres/user_store.go:57-79`) joins `destination_id` to show the
   user *where their paper is*. In a building with printers on several floors,
   sending them to the wrong floor is a real support problem. If a future change
   added failover without updating the row, the history would point at the
   original choice.
2. **It pins the absence of failover.** A wedged printer consumes the entire
   retry budget for every job the balancer routes to it, and the balancer keeps
   routing to it because `EnabledDestinations` only filters on the `enabled`
   column — there is no health signal, no circuit breaker, and no way for a
   failing destination to remove itself.

## Expensive to rediscover

- The choice is random per job (`uniformBalancer`), not sticky, so a wedged
  printer degrades a fraction of jobs rather than all of them — which makes the
  problem look intermittent and user-specific in reports.
- `pickDestination` failing (no enabled destinations, unknown policy) resolves
  the job from `quota_deducted` straight to `print_failed` via
  `deliver.go:67` — quota returned, nothing printed, correct.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: fake SMB sink recording which destination address received each
  payload, plus a workload-side `Always` matching it against the job's
  `destination_id`.
- Missing (useful): `Sometimes` on "a job was retried at least once", so the
  property is known to have been exercised rather than trivially true.

## Open Questions

- **Is the absence of failover to another destination intentional?** If a
  retry should try a different printer when one is wedged, this property inverts
  — it would become "every attempt targets *an* enabled destination of the
  queue, and `destination_id` reflects the one that succeeded". The two versions
  are mutually exclusive, so the answer determines the property, not just its
  priority. `(needs human input)`

### Investigation Log

#### Is the absence of failover to another destination intentional?

- **Examined:** `internal/app/deliver.go:64-106` (the loop closes over a single
  `dest`), `:130-152` (`pickDestination`, called once), `internal/queue/queue.go`
  in full (`EnabledDestinations`, `LoadBalancerPolicy`, `uniformBalancer`),
  `schema.sql:37-45` (the `destination` table), and the git log for a commit
  touching retry or failover behaviour.
- **Found:** the design is coherently single-destination. `MarkSent` persists the
  choice *before* the first send, `destination_id` is a foreign key surfaced to
  the user in their history, and the only policy implemented is `uniform`. There
  is no health column on `destination`, no per-destination failure counter, and
  no circuit breaker — so even if the loop wanted to re-pick, it has no signal to
  pick better.
- **Not found:** any comment, TODO, or test expressing an intent either way. The
  `policy` column and the `LoadBalancerPolicy` type are built for extension (a
  string-keyed registry with exactly one entry), which suggests routing was meant
  to grow — but nothing indicates whether failover is part of that plan.
- **Conclusion:** the code is internally consistent with "no failover by design",
  but the extensible policy machinery is equally consistent with "not yet
  implemented". Exhausted the directory; tagged `(needs human input)`.
