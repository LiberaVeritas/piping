# analysis-terminates-within-bound

## What led here

`Analyzer.CountPages` spawns ghostscript on user-supplied bytes and has **no
timeout of its own**, and the HTTP server sets no request timeout. Every other
subprocess in the system is bounded; this one is not.

## Code paths

- `internal/ghostscript/analyzer.go:43-83` — `CountPages`: writes the upload to
  `os.CreateTemp`, spawns `gs -sDEVICE=inkcov`, waits for it via `cmd.Run()`
- `internal/ghostscript/analyzer.go:60` — `exec.CommandContext(ctx, ...)` where
  `ctx` is the request context, still cancellable by the client at this point
  (`submit.go:136` detaches it only *after* analysis)
- `cmd/piping/main.go:225-229` — `http.Server{ReadHeaderTimeout: 2 * time.Second}`
  and nothing else. No `ReadTimeout`, no `WriteTimeout`, no `IdleTimeout`.
- `cmd/piping/main.go:161-166` — `VerifyDevice` at startup *does* use a 5s
  timeout, which shows the pattern was known and simply not applied to the
  per-request path.

## What goes wrong

A PDF crafted to make ghostscript spin holds:

- one goroutine,
- one temp file (removed only by `defer`, so it survives until the process dies),
- eventually a pgx pool connection once analysis completes,

for as long as the client keeps the connection open. Any authenticated student
can do this by uploading one file, repeatedly. There is no rate limit anywhere in
the codebase.

## Expensive to rediscover

- The client *can* cancel it — the context is still live during analysis — which
  means a well-behaved browser closing the tab does free the resources. The
  attack requires only that the client hold the connection open, which is
  trivial.
- `-dSAFER` is passed (`analyzer.go:62`), which constrains file access but does
  nothing about CPU.
- The temp file is written before the spawn and removed by `defer os.Remove`;
  process death (including an OOM kill triggered by this very path) leaks it into
  the container's writable layer.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` bounding submit latency.
- Missing (stronger, requires a code change): a SUT-side `Unreachable` on a "gs
  exceeded its budget" branch. No such branch exists — adding
  `context.WithTimeout` around the analysis would create one, and would be the
  actual fix rather than merely the detection.

## Open Questions

- **What ceiling is defensible for a legitimate 100-page document on the
  deployment's CPU allocation?** Without a measured figure the assertion
  threshold is arbitrary, and too tight a bound turns a slow-but-correct analysis
  into a spurious failure. The k8s manifest requests 100m CPU with a 1-core
  limit, which makes the answer deployment-specific.
