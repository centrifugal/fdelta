# CLAUDE.md

See [AGENTS.md](AGENTS.md). It is the guide for working on this package and
applies here in full: the invariants, how to run the suite in each of its
modes, the golden file workflow, the two test oracles, and the list of things
that look wrong but are deliberate.

Three points from it are worth repeating, because they are the ones most easily
lost:

- **`Apply` parses bytes from a peer.** It must never panic, never return
  partial output, and never allocate for a delta it has not validated.
- **`bucketIndex` multiplying instead of taking a remainder is deliberate.**
  Reverting it to `%` reintroduces a denial of service.
- **A failing `TestGolden` means the encoder's output changed.** Understand why
  before regenerating it; never regenerate it just to get a green build.

Run `go test ./...` and `go test -tags cref ./...` before reporting a change to
the encoder or decoder as done.
