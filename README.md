# fdelta

[![Go Reference](https://pkg.go.dev/badge/github.com/centrifugal/fdelta.svg)](https://pkg.go.dev/github.com/centrifugal/fdelta)

Fossil delta compression for Go.

Instead of storing or transmitting the whole of something, store or transmit
only what changed relative to a version the other side already has. `Create`
builds the diff, `Apply` reconstructs the new bytes from the old ones plus the
diff.

* [Delta format specification](https://fossil-scm.org/home/doc/tip/www/delta_format.wiki)
* [Encoder algorithm](https://fossil-scm.org/home/doc/tip/www/delta_encoder_algorithm.wiki)
* [Original implementation](https://fossil-scm.org/home/doc/tip/src/delta.c)

Other implementations:
- [C#](https://github.com/endel/FossilDelta)
- [Go](https://github.com/shadowspore/fossil-delta) (package used as the baseline for this implementation)
- [Haxe](https://github.com/endel/fossil-delta-hx)
- [Python](https://github.com/ggicci/python-fossil-delta)
- [JavaScript](https://github.com/dchest/fossil-delta-js) ([online demo](https://dchest.github.io/fossil-delta-js/))

Deltas are portable in both directions between all of them.

## Install

```
go get github.com/centrifugal/fdelta
```

No dependencies and no cgo: `go.mod` has no `require` block, and importing the
package pulls in nothing beyond `encoding/binary`, `math/bits`, `slices`,
`errors` and `sync`.

## Example

```go
package main

import (
	"fmt"

	"github.com/centrifugal/fdelta"
)

func main() {
	origin := []byte(`{"user":"alice","status":"online","score":1200,"rank":"gold"}`)
	target := []byte(`{"user":"alice","status":"online","score":1350,"rank":"gold"}`)

	delta := fdelta.Create(origin, target)

	patched, err := fdelta.Apply(origin, delta)
	if err != nil {
		panic(err)
	}

	fmt.Printf("target: %d bytes\n", len(target)) // target: 61 bytes
	fmt.Printf("delta:  %d bytes\n", len(delta))  // delta:  32 bytes
	fmt.Printf("match:  %v\n", string(patched) == string(target))
}
```

`Create` always succeeds, but a delta is not always worth sending: for payloads
with little in common it comes out larger than the payload itself. Compare
`len(delta)` against `len(target)` and send whichever is smaller.

## API

```go
func Create(origin, target []byte) []byte
func Apply(origin, delta []byte) ([]byte, error)

// Appending forms, for callers reusing a buffer.
func AppendCreate(dst, origin, target []byte) []byte
func AppendApply(dst, origin, delta []byte) ([]byte, error)

// The output size a delta declares, read from its header alone.
func OutputSize(delta []byte) (int, error)
```

## Errors

`Apply` distinguishes the two failures a caller handles differently:

```go
out, err := fdelta.Apply(base, delta)
switch {
case errors.Is(err, fdelta.ErrChecksumMismatch):
    // Well formed, but base is not the source it was built from.
    // Ask for the payload in full.
case errors.Is(err, fdelta.ErrInvalidDelta):
    // The delta itself is corrupt.
}
```

It never panics, never returns partial output, and never allocates for a
*malformed* delta — deltas usually arrive from somewhere else, so it is written
to hold up against bytes chosen to break it.

A *well formed* delta is a different matter, and this is the one thing to know
before applying deltas from an untrusted peer. A copy command costs about seven
bytes and can copy the whole of `origin`, so a tiny delta can legitimately
demand an enormous output. Against a 1 MiB origin, a 3.5 KB delta can ask for
500 MB; the ceiling is 4 GiB per call. That is inherent to the format, not to
this implementation.

Call `OutputSize` first. It reads the header alone, allocates nothing, and
`Apply` will not produce more than it reported:

```go
if n, err := fdelta.OutputSize(delta); err != nil || n > maxPayload {
    // refuse, having done no work
}
```

The checksum is an integrity check, not authentication: whoever supplies the
delta chooses the output and can supply a matching checksum. See
[SECURITY.md](SECURITY.md).

## Performance

Apple M4, Go 1.26, median of three runs. `go test -bench .` reproduces these.

**`Create`**, by payload size and shape of change:

| payload | one field changed | block rewritten | unchanged | nothing in common |
|---|---:|---:|---:|---:|
| 1 KiB | 470 ns | 1.40 µs | 412 ns | 3.30 µs |
| 4 KiB | 1.51 µs | 3.80 µs | 1.46 µs | 12.3 µs |
| 16 KiB | 5.69 µs | 15.2 µs | 5.64 µs | 57.0 µs |
| 64 KiB | 22.2 µs | 171 µs | 22.1 µs | 287 µs |

**`Apply`**:

| payload | time | throughput |
|---|---:|---:|
| 1 KiB | 139 ns | 7.4 GB/s |
| 4 KiB | 451 ns | 9.1 GB/s |
| 16 KiB | 1.52 µs | 10.8 GB/s |
| 64 KiB | 6.21 µs | 10.6 GB/s |

`Create` allocates once, for the delta itself — 128 bytes for a payload with
one field changed, whatever its size, since the hash tables come from a pool.
`Apply` allocates once, for the output, sized exactly by its validation pass;
rejecting a malformed delta allocates nothing at all. `AppendCreate` and
`AppendApply` remove even those: a 16 KiB payload applies in about 700 ns into
a buffer that is already large enough, against 1.52 µs when the output has to
be allocated and zeroed.

## Notes on the encoder

`Create` does not emit the byte-for-byte delta the reference implementation
would choose, though it emits one of the same size, and every implementation
reads it.

The difference is in how a block hash is reduced to a bucket in the index the
encoder builds over `origin`. The format's hash is concentrated — its low half
is a plain byte sum, at most 4080 over a 16-byte window — so reducing it with a
remainder leaves most buckets empty and the collision chains long. The modulus
is the block count, which is a power of two whenever the payload is, and the
hash's high half then cancels out of the remainder entirely: at 1 MiB that
leaves 2.8% of buckets used and chains 120 long.

The effect on high-entropy payloads is severe, and worst at sizes a remote peer
is quite likely to hit. A mebibyte of random bytes takes 292 ms to encode that
way, against 13 ms for 1,000,000 random bytes — a 22x penalty for a 4.8%
difference in size.

This package multiplies the hash by an odd constant and takes the high bits
instead, folding every input bit into the bucket. Occupancy at 1 MiB goes to
55%, the longest chain to 10, and that mebibyte encodes in 17.8 ms, with the
cost roughly linear in payload size and no dependence on whether that size is a
power of two. The hash is the encoder's private index and never appears in a
delta, so nothing about the format changes.

## Testing

```
make test          # unit, differential, property and golden tests
make test-race
make test-cref     # additionally against Fossil's own delta.c
make check         # everything CI runs, bar the slow jobs
```

`make` on its own lists every target.

The `cref` tag compiles a vendored, unmodified copy of `src/delta.c` through
cgo and runs this package against it: identical checksums and digit counts,
deltas that apply in both directions, and no delta the reference accepts that
this package does not accept to the same bytes. It needs a C toolchain, which
is why it is behind a tag; an ordinary `go test` needs nothing.

Interoperability is checked in two directions that matter. `delta.c` is the
reference, and `make interop-js` runs this encoder's deltas through the
JavaScript decoder from
[fossil-delta-js](https://github.com/dchest/fossil-delta-js) — the code
browsers actually run, since Centrifugo's JavaScript SDK vendors it.

`testdata/fossil-vectors/` holds frozen origin/target/delta triples whose
deltas were produced by Fossil itself, and `testdata/golden.txt` pins what this
encoder emits. Seven fuzz targets run in CI on every push and for ten minutes
each weekly, three of them differentially against `delta.c`.

Some tests cover their input space completely rather than sampling it: every
pair of strings over a two-symbol alphabet up to length ten, every single-byte
edit at every position for lengths either side of the hash window, and every
one and two byte input to the integer decoder. `Apply` is additionally checked
against deltas assembled directly from arbitrary valid commands — zero-length
copies, copies that overlap or run backwards, deltas made only of inserts —
which the encoder here would never emit but another implementation may. The suite also runs
on 32-bit ARM, 32-bit x86 and big-endian s390x, since several bounds checks
only carry weight where `int` is 32 bits and the word-at-a-time comparisons
have to agree whatever the machine's byte order.

## Contributing

[AGENTS.md](AGENTS.md) documents the invariants, how to run the suite in each
of its modes, the golden file workflow, and the parts of the source that look
wrong but are deliberate. Read it before changing anything here.

## License

BSD 2-Clause. See [LICENSE](LICENSE), which carries the notices of every
implementation this descends from, beginning with the original C in Fossil by
D. Richard Hipp.
