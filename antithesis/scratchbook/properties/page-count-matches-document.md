# page-count-matches-document

## What led here

Added during property evaluation (Coverage Balance and Wildcard). The billing
chain is:

```
PDF → gs subprocess → regex over stdout → (pages, colorPages) → cost → quota
```

The original catalog began its scrutiny at `cost`. Everything to the left was
taken on faith, and the leftmost link is a regular expression over the stdout of
a subprocess.

## Code paths

- `internal/ghostscript/analyzer.go:41`:
  ```go
  // (Ghostscript bug 699342), sometimes no newlines
  var cmykLine = regexp.MustCompile(`(\d+\.\d+)\s+(\d+\.\d+)\s+(\d+\.\d+)\s+(\d+\.\d+)\s+CMYK OK`)
  ```
  The comment names a known upstream output quirk. There is no test for it.
- `internal/ghostscript/analyzer.go:87-102` — `parseInkcov`: **one page per regex
  match**. `pages` is not read from any page-count field; it is the number of
  matches.
- `internal/ghostscript/analyzer.go:97` — a page is colour iff
  `chromaSpread(c,m,y) > threshold`, with `COLOR_THRESHOLD` defaulting to 0.0005
- `internal/app/submit.go:100-110` — the counts are sanity-checked only for
  *internal* consistency (`pages <= 0`, `colorPages > pages`), never against the
  document
- `internal/ghostscript/analyzer.go:79-81` — zero pages becomes
  `pdf.ErrUnreadable`

`internal/ghostscript` has **no test file at all**.

## What goes wrong

An under-counting parse is a silent, systematic discount applied to whichever
documents trigger it. The user is charged for fewer pages than print. Every
downstream property confirms the wrong number was handled consistently, because
they all take the count as their input.

Over-counting is the mirror image: the student is overcharged and has no way to
dispute it, since the stored `num_pages` is the only record and it agrees with
the cost.

The colour threshold is a second, independent measurement: a document just over
`COLOR_THRESHOLD` on one page is billed at `COLOR_RATE` for that page. Nothing
tests the boundary.

## Expensive to rediscover

- `pages` is derived from *match count*, so any output truncation silently
  reduces the page count rather than producing an error. A `gs` killed partway
  through writing stdout yields a short but well-formed match list.
- `cmd.Run()` returning success is the only correctness gate; there is no
  cross-check against the PDF's own page tree.
- The temp file is written and `gs` reads it back — a filesystem fault between
  write and read changes what is measured relative to what is printed, since the
  printer receives the in-memory `doc` bytes, not the temp file.

## Instrumentation

Per `existing-assertions.md`, no Antithesis SDK assertions exist.

- Missing: workload-side `Always` comparing stored `num_pages` /
  `num_color_pages` against corpus ground truth. The workload controls the
  corpus, so ground truth is free.
- Missing (useful): SUT-side `Sometimes` on "inkcov output produced zero matches
  for a non-empty document", which distinguishes a parse failure from an empty
  PDF — currently both surface as `ErrUnreadable`.

## Open Questions

- **Does a partially-consumed `gs` stdout yield a short match list rather than a
  parse error?** `parseInkcov` returns `(len(matches), colorCount, nil)` for any
  number of matches, and `CountPages` only rejects zero. So a truncated stream
  undercounts silently. If confirmed under a kill fault, this is a defect rather
  than a hypothetical, and the fix is to cross-check the count against a second
  source (`gs -dNODISPLAY` page count, or the PDF page tree).
