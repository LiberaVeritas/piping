# piping

It pipes documents from computers to printers

## features

- Print from web native
- Concurrency safe with atomic updates so your quota doesn't go poof
- Not just tepid; it's piping

## misc

Quota checking and job creation happen atomically in the same transaction. This avoids double spends, if two quota checks happen before either creates a job.

Job states form a state machine. State transitions are guarded and atomic, except from `print_sent` since that requires calling out to smb and waiting, during which a crash or db update can happen. So there is a periodic `sweep` task that resolves jobs stuck in non terminal states.
