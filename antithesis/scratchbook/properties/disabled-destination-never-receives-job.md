# disabled-destination-never-receives-job

## What led here

Added during property evaluation (Coverage Balance). `deployment-topology.md`
proposes a custom fault that toggles `queue.enabled` and `destination.enabled`
mid-run, and no property consumed it. Configuration change under load is a named
attack surface in the SUT analysis, and this is the system's only such surface.

## Code paths

- `internal/app/submit.go:90-98` — the queue is fetched and `q.Enabled` checked
  at submit time
- `internal/postgres/queue_store.go:14-31` — `GetQueue`, read per request, **no
  caching**
- `internal/app/deliver.go:131-138` — `DestinationsForQueue` then
  `queue.EnabledDestinations`, also per request
- `internal/queue/queue.go:26-35` — `EnabledDestinations` filters on the `Enabled`
  field
- `internal/app/deliver.go:70` — `MarkSent` freezes the chosen destination before
  any send

The gap between the queue check (`submit.go:90`) and destination selection
(`deliver.go:65`) spans the entire analysis and the quota transaction — hundreds
of milliseconds at least, and unbounded if ghostscript is slow. A toggle landing
in that window is the target.

## What goes wrong

- A job accepted for a queue that is disabled moments later: acceptable, since
  the check happened when it happened. Worth pinning so it does not become
  *rejection after charging*, which would be worse.
- A payload spooled to a destination disabled before selection: the lever does
  not work. Paper is produced at a printer nobody is watching — possibly one that
  was disabled because it was removed from the building — while the user is told
  the job succeeded.

## Expensive to rediscover

- There is no health signal on `destination` beyond the manual `enabled` column:
  no failure counter, no circuit breaker, no last-success timestamp. Disabling is
  the operators' *only* lever, which is why it working matters more than it
  looks.
- `EnabledDestinations` returns `nil` when everything is disabled, and
  `pickDestination` turns that into an error that resolves the job to
  `print_failed` with quota returned (`deliver.go:67`) — the correct outcome, and
  worth asserting so a future refactor cannot turn "no destinations" into a
  silent success.
- Nothing re-checks `enabled` between `MarkSent` and the send, so a destination
  disabled after selection still receives the payload. That is arguably correct
  (the decision was made) but should be a stated property rather than an
  accident.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` — no SMB receipt exists for a destination that
  was disabled at selection time. Requires the workload to know the toggle
  timeline, so the custom fault should record its toggles somewhere the workload
  can read (the database itself is the natural place).
- Missing: workload-side `Always` — a submission to a disabled queue is rejected
  with `queue.ErrUnavailable` (HTTP 409) and creates no job row.

## Open Questions

- **Should a job already in `print_sent` for a destination that is subsequently
  disabled be allowed to complete?** The property as written constrains selection
  only, because the payload has already left the process by then. If operators
  expect disabling to stop in-flight jobs, that is a feature the system does not
  have and the property should say so explicitly rather than encoding the current
  behaviour as correct.
