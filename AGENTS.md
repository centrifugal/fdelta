# Working on fdelta

This package implements the Fossil delta encoding. `Create` builds a binary
diff between two byte slices; `Apply` turns the old bytes plus the diff back
into the new ones. Centrifugo and Centrifuge use it to send only what changed
in a publication, so `Create` runs on a server, on every publication, and
`Apply` runs in clients on bytes that arrived over a network.

That shapes everything below. Read this before changing code.

## Invariants

These are not preferences. A change that breaks one is wrong even if every test
passes, so if you find yourself about to break one, stop and say so instead.

1. **`Apply` never panics, for any input.** It takes bytes from a peer. It also
   never returns output alongside an error, never returns partial output, and
   never allocates on behalf of a delta it has not already validated.
2. **Deltas stay interoperable.** The command stream, the integer encoding and
   the checksum are the format. Other languages' implementations read what this
   produces and this reads what they produce. Nothing in `codec.go`,
   `checksum.go`, or the command emission in `create.go` may change behaviour.
3. **`Apply` is never more lenient than `delta.c`.** Stricter is fine and is
   how the panics were fixed. Accepting something Fossil rejects is a bug.
4. **No dependencies, no cgo, no `unsafe`** in the shipped package. `go.mod`
   has no `require` block and CI fails if one appears. The C code under
   `internal/cref` is test-only, build-tagged, and imported by nothing outside
   a test.
5. **Correct on 32-bit and big-endian.** Several bounds checks only carry
   weight where `int` is 32 bits, and the word-at-a-time comparisons have to
   agree whatever the machine's byte order. CI runs the suite on 386, arm/v7
   and s390x.
6. **`Create` is a pure function of its inputs**, identical on every platform.
   `testdata/golden.txt` is verified on big-endian and 32-bit.

## Running things

Everything goes through the Makefile, and CI runs the same targets, so what
passes locally passes there. `make` on its own lists them.

```sh
make check          # the gate: fmt, vet, no-cgo, tests, race, coverage, lint, sec
make test           # just the suite
make test-cref      # additionally against Fossil's own delta.c (needs a C toolchain)
make cross          # 32-bit and big-endian under Docker
make interop-js     # against the JavaScript decoder browsers run (needs node)
make fuzz           # every fuzz target; FUZZTIME=2m for something thorough
make bench
make golden         # regenerate testdata/golden.txt and show the diff
make exhaustive     # the codec over all 2^32 values, the checksum tails in full
make check-all      # all of the above
```

`make check` is what to run before handing work back. It deliberately leaves
out the three that need something extra — a C toolchain, Docker, or time — and
prints a reminder of them when it passes.

**Run `make test-cref` for any change to `create.go`, `apply.go`, `codec.go` or
`checksum.go`.** It compiles `internal/cref/fossil/delta.c` through cgo and
checks this package against the reference: identical checksums and digit
counts, deltas that apply in both directions, and no delta the reference
accepts that this package does not accept to the same bytes.

**Run `make cross` for any change to a bounds check or to the word-at-a-time
comparisons.** Emulation is slow, so it uses `-short`.

## The golden file

`testdata/golden.txt` holds the exact delta this encoder produces for a fixed
set of inputs. It pins the encoder against itself.

If a change makes `TestGolden` fail, that is the test doing its job. **Do not
regenerate it to make a build green.** Work out why the output changed first.
Only once the change is understood and intended:

```sh
make golden
```

which regenerates it and shows the diff. Read that diff. A change there is a
change in the bytes every client receives.

The golden file pins bytes but cannot tell right from wrong, so it sits
alongside the checks that can: every golden delta is also applied back through
this package, and under `cref` through `delta.c`.

## Checking against other implementations

`internal/jsref` holds the JavaScript decoder, behind the `jsref` tag. It is
the one that runs in browsers: Centrifugo's JavaScript SDK vendors `applyDelta`
from it, so it reads what a Go server writes. `delta.c` proves conformance to
the reference; this proves the deployed path. **Run `make interop-js` for any
change to `create.go`.**

`internal/cref` holds Fossil's actual `src/delta.c`, unmodified, behind the
`cref` tag. It is the authority on the format: `fossil/delta.c` is vendored
verbatim and `shim.c` only includes it and exposes its static functions.

Without a C toolchain the format is still pinned from two directions:
`testdata/fossil-vectors/` holds origin/target/delta triples Fossil itself
produced, so `Apply` is checked against real reference output, and
`testdata/golden.txt` pins what this encoder emits. Everything else in the
suite checks this package against the specification directly rather than
against another implementation — the checksum against summed big-endian words,
`blockHash` against the definition of the hash, `digitCount` against what
`appendInt` actually writes.

## Things that look wrong and are not

Leave these alone unless you have measured that the change is an improvement,
and say so if you do.

- **`bucketIndex` multiplies and takes the high bits** instead of taking a
  remainder. This is the one deliberate departure from `delta.c` and it is
  load-bearing: the format's hash is concentrated (its low half is a plain byte
  sum, at most 4080), so a remainder leaves most buckets empty and the chains
  long — and when the block count is a power of two, which it is whenever the
  payload is, the hash's high half cancels out of the remainder entirely.
  "Simplifying" it back to `%` reintroduces a denial of service: a mebibyte of
  random bytes goes from ~18ms to ~300ms. `TestBucketDistributionOnHighEntropyInput`
  guards the property; `TestBucketMultiplierIsSuitable` guards the constant's
  requirements. The constant's *value* is arbitrary — an alternate odd
  multiplier moves thousands of blocks between buckets and produces identical
  deltas — but it must be odd and full-width.
- **The 250-candidate search limit** matches `delta.c` exactly. Note the loop
  shape: the limit is tested before the decrement, so the body runs 250 times,
  not 249.
- **`digitCount` uses `bits.Len`, not `bits.Len32`.** Narrowing first answers
  zero for `v == 1<<32`. A sizing function that can under-report is the wrong
  shape to leave around, even where the encoder cannot reach it.
- **`commonSuffixLen` takes indices, not slices.** Re-slicing both inputs for
  every candidate costs more than the comparison when candidates fail
  immediately, which is the common case.
- **`blockHash` is written out rather than looped.** Go does not unroll, and
  the four independent groups are what let the additions issue in parallel;
  the grouping is valid because neither half of the hash can overflow 16 bits
  over a 16-byte window.
- **The `u32` helper and the `#nosec G115` annotations** each carry the
  reason that narrowing is safe. Do not add a blanket suppression; if a new
  narrowing appears, justify it in the same way or restructure to avoid it.
- **`apply_test.go` asserts the exact internal error**, not just
  `ErrInvalidDelta`. That is deliberate: a bounds check that stops working
  usually lets parsing run off the end instead, which also reports an invalid
  delta, so the weaker assertion passed with the bug reintroduced.

## Performance

`Create` and `Apply` are on the hot path of every publication. Rough figures on
an Apple M4 for a 16 KiB JSON payload with one field changed: `Create` ~5.7µs
with one allocation, `Apply` ~1.5µs with one allocation. `AppendCreate` and
`AppendApply` take a destination buffer and remove that allocation; reuse is
worth real time in `Apply` (about a third of the cost is allocating and zeroing
the output) and only the allocation in `Create`.

Run `make bench` before and after any change to the hot path and compare with
`benchstat`. Single runs on a laptop are noise; several of the optimisations
here were within 5% of each other and needed `-count` to tell apart.
`make bench-adversarial` covers the high-entropy case and should stay roughly
flat in ns/byte across sizes — if it starts climbing, the block index has
regressed.

## Where the coverage comes from

Beyond the unit tests, four things carry most of the weight, and a change that
makes any of them vacuous has removed real coverage:

- `genvalid_test.go` assembles deltas directly from arbitrary valid commands,
  so `Apply` is tested on shapes this encoder never emits — zero-length
  commands, overlapping and backwards copies, inserts only. Under `cref` the
  same deltas are put through `delta.c`.
- `exhaustive_test.go` covers parts of the input space completely rather than
  sampling: all pairs of short strings over a two-symbol alphabet, every
  single-byte edit at every position around the hash window, every one and two
  byte input to the decoder.
- `testdata/golden.txt` pins the encoder's output, and
  `testdata/fossil-vectors/` pins it against bytes Fossil produced.
- The fuzz targets, three of which are differential against `delta.c`.
- `spec_test.go` checks conformance against the published format
  specification, including reproducing its own worked example command by
  command. Two of its tests pin places where the specification's text and the
  reference implementation disagree; read them before "fixing" either.
- `exhaustive_codec_test.go`, behind the `exhaustive` tag, verifies the codec
  over the whole 32-bit domain and the checksum over every input short enough
  for its tail handling to apply.

## Checking that a test actually tests something

The suite has been mutation-tested, and that found real holes. When you add a
test for a bug, verify it fails without the fix:

```sh
# revert the fix, or introduce the bug deliberately
make test        # must FAIL
# restore
make test        # must PASS
```

This is not ceremony. The original test for the insert-overrun panic passed
with the bug reintroduced, because it asserted only that *an* error came back,
and the broken bounds check produced a different error rather than none.

If you change the algorithm, re-run a mutation sweep: inject each of a dozen
plausible bugs (drop a bounds check, change the candidate limit by one, flip
the checksum's endianness, take the low bits of the bucket hash) and confirm
the suite catches each. Survivors are either test gaps or equivalent mutants;
work out which before dismissing them.

## Before saying a change is done

- `make check` passes
- for changes to the encoder or decoder, `make test-cref` and `make fuzz` too
- for changes to a bounds check or the word-at-a-time comparisons, `make cross`
- `testdata/golden.txt` is either unchanged or its diff is explained

## What you cannot change

The wire format. The command stream, the base-64 integer encoding and the
checksum are fixed by Fossil and by every other implementation in every other
language. If something appears to require changing them, it is the wrong
approach; raise it rather than working around it.

Note also that the format's own limits are not this package's to fix: a delta's
lengths are 32-bit, so inputs must stay below 4 GiB, and a short delta can
legitimately declare a very large output (`OutputSize` exists so a caller can
impose its own ceiling). Both are documented in `SECURITY.md`.
