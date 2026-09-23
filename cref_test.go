//go:build cgo && cref

package fdelta

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/centrifugal/fdelta/internal/cref"
)

// These tests run this package against Fossil's own src/delta.c, the
// implementation that defines the format. Build them with:
//
//	go test -tags cref ./...
//
// Create is NOT expected to produce the same bytes as delta.c. The two reduce
// a block hash to a bucket differently (see bucketIndex), and delta.c reads
// its hash window through a signed char where this reads it unsigned, so on
// some inputs they reach different, equally valid deltas. What must hold is
// that each side accepts the other's deltas and reproduces the target exactly,
// which is what portability actually means here.

func TestCRef_OurDeltasApplyInC(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			out, err := cref.Apply(tc.origin, Create(tc.origin, tc.target))
			if err != nil {
				t.Fatalf("delta.c rejected our delta: %v", err)
			}
			if !bytes.Equal(out, tc.target) {
				t.Fatalf("delta.c produced %q, want %q", out, tc.target)
			}
		})
	}
}

func TestCRef_CDeltasApplyInOurs(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Apply(tc.origin, cref.Create(tc.origin, tc.target))
			if err != nil {
				t.Fatalf("we rejected a delta from delta.c: %v", err)
			}
			if !bytes.Equal(out, tc.target) {
				t.Fatalf("we produced %q, want %q", out, tc.target)
			}
		})
	}
}

func TestCRef_RoundTripsBothWays_Random(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(173, 179))
	for range 400 {
		origin := randomBytes(rnd, rnd.IntN(4096))
		target := mutate(rnd, origin)

		out, err := cref.Apply(origin, Create(origin, target))
		if err != nil || !bytes.Equal(out, target) {
			t.Fatalf("delta.c could not apply our delta: err=%v\norigin %q\ntarget %q", err, origin, target)
		}
		out, err = Apply(origin, cref.Create(origin, target))
		if err != nil || !bytes.Equal(out, target) {
			t.Fatalf("we could not apply a delta.c delta: err=%v\norigin %q\ntarget %q", err, origin, target)
		}
	}
}

// The checksum is the one part of the encoder whose value travels in the
// delta, so it has to agree with delta.c exactly, at every length.
func TestCRef_Checksum(t *testing.T) {
	rnd := rand.New(rand.NewPCG(181, 191))
	for n := range 300 {
		b := randomBytes(rnd, n)
		if got, want := checksum(b), cref.Checksum(b); got != want {
			t.Fatalf("checksum of %d bytes = %#08x, delta.c says %#08x", n, got, want)
		}
	}
	for _, n := range []int{1 << 10, 1<<10 + 3, 16 << 10, 1<<16 + 7} {
		b := randomBytes(rnd, n)
		if got, want := checksum(b), cref.Checksum(b); got != want {
			t.Fatalf("checksum of %d bytes = %#08x, delta.c says %#08x", n, got, want)
		}
	}
}

// digitCount prices every copy command, so a disagreement would change which
// matches the encoder takes.
//
// The comparison stops below cref.MaxDigitCount: delta.c's digit_count does
// not terminate at or above it. See TestDigitCountIsTotal, which covers the
// range the reference cannot reach.
func TestCRef_DigitCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	for _, v := range []int{0, 1, 2, 63, 64, 65, 4095, 4096, 262143, 262144,
		16777215, 16777216, 1 << 24, cref.MaxDigitCount - 1} {
		if got, want := digitCount(v), cref.DigitCount(v); got != want {
			t.Fatalf("digitCount(%d) = %d, delta.c says %d", v, got, want)
		}
	}
	rnd := rand.New(rand.NewPCG(193, 197))
	for range 20000 {
		v := rnd.IntN(cref.MaxDigitCount)
		if got, want := digitCount(v), cref.DigitCount(v); got != want {
			t.Fatalf("digitCount(%d) = %d, delta.c says %d", v, got, want)
		}
	}
}

// delta.c hashes through a signed char, so the two agree only while every byte
// of the window is ASCII. That is the whole range where a comparison is
// meaningful, and it covers the arithmetic.
func TestCRef_BlockHashOnASCII(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(199, 211))
	b := make([]byte, 4096)
	for i := range b {
		b[i] = byte(rnd.IntN(0x80))
	}
	for pos := 0; pos+hashWindow <= len(b); pos++ {
		if got, want := blockHash(b, pos), cref.HashOnce(b[pos:]); got != want {
			t.Fatalf("blockHash at %d = %#08x, delta.c says %#08x", pos, got, want)
		}
	}
}

func TestCRef_OutputSize(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			d := Create(tc.origin, tc.target)
			got, err := OutputSize(d)
			if err != nil {
				t.Fatalf("OutputSize: %v", err)
			}
			if want := cref.OutputSize(d); got != want {
				t.Fatalf("OutputSize = %d, delta.c says %d", got, want)
			}
		})
	}
}

// The contract that matters for interoperability: anything delta.c accepts,
// this package accepts and produces the same bytes for. This package may be
// stricter, and is, on malformed input that delta.c happens to tolerate.
func TestCRef_ApplyAgreesOnMalformed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	origin := []byte(strings.Repeat("shared-body-payload-", 32))
	valid := Create(origin, []byte(strings.Repeat("shared-body-payload-", 32)+"tail"))

	var deltas [][]byte
	deltas = append(deltas, valid)
	for _, s := range []string{
		"", "2", "2\n", "2 2@0,", "2\n2@0,", "2\n2!0,", "2\n2@0!",
		"A\n7:000000", "K\nA:0123456789A:xy", "2\n2@3~~~~~,", "2\n2@~,",
		"2\n~@0,", "1\n2@0,", "1\n2:ab", "A\n3~~~~~:xy", "3~~~~~\n2@0,",
		"Z\n2@0,0;", "1\n2@0,0;", "2\n2@0,0;", "A\nA:012", "\n", "0\n0;",
	} {
		deltas = append(deltas, []byte(s))
	}
	// Plus every single-byte truncation and corruption of a valid delta.
	for i := range valid {
		deltas = append(deltas, valid[:i])
		c := bytes.Clone(valid)
		c[i] ^= 0x01
		deltas = append(deltas, c)
		c = bytes.Clone(valid)
		c[i] = 0
		deltas = append(deltas, c)
	}

	for _, d := range deltas {
		want, cErr := cref.Apply(origin, d)
		got, goErr := Apply(origin, d)

		if cErr == nil {
			if goErr != nil {
				t.Fatalf("delta.c accepted a delta we rejected (%v): %q", goErr, d)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("delta.c produced %q, we produced %q, for %q", want, got, d)
			}
			continue
		}
		if !errors.Is(cErr, cref.ErrRejected) {
			t.Fatalf("unexpected error from delta.c: %v", cErr)
		}
		// delta.c rejected it. We must reject it too: being more lenient than
		// the reference is the direction that would be a bug.
		if goErr == nil {
			t.Fatalf("we accepted a delta delta.c rejected, producing %q: %q", got, d)
		}
	}
}

// What Create returns for an input too large for the format has to be
// rejected by the reference too, or a peer running delta.c could accept it.
func TestCRef_UnrepresentableDeltaIsRejected(t *testing.T) {
	delta := appendUnrepresentable(nil)
	for _, origin := range [][]byte{nil, []byte("x"), bytes.Repeat([]byte("origin"), 100)} {
		if out, err := cref.Apply(origin, delta); !errors.Is(err, cref.ErrRejected) {
			t.Fatalf("delta.c applied %q to a %d byte origin: %q, %v", delta, len(origin), out, err)
		}
	}
}

// The encoder is allowed to differ from delta.c in which delta it picks, but
// not to be materially worse at picking one.
func TestCRef_DeltaSizesAreComparable(t *testing.T) {
	for _, tc := range corpus() {
		if len(tc.target) < 1024 {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			ours := len(Create(tc.origin, tc.target))
			theirs := len(cref.Create(tc.origin, tc.target))
			// Allow a little slack for the off-by-one in the candidate limit,
			// but nothing that would signal a real regression in matching.
			if float64(ours) > float64(theirs)*1.05+64 {
				t.Fatalf("our delta is %d bytes, delta.c gets %d", ours, theirs)
			}
		})
	}
}

func FuzzCRefApplyAgrees(f *testing.F) {
	origin := []byte(strings.Repeat("shared-body-payload-", 8))
	f.Add(origin, Create(origin, append(bytes.Clone(origin), "tail"...)))
	f.Add(origin, []byte("2\n2@0,"))
	f.Add(origin, []byte("A\n7:000000"))
	f.Add([]byte("0"), []byte("A\n7:000000"))
	f.Fuzz(func(t *testing.T, origin, delta []byte) {
		want, cErr := cref.Apply(origin, delta)
		got, goErr := Apply(origin, delta)
		if cErr != nil {
			return // delta.c rejected it; we are allowed to be stricter.
		}
		if goErr != nil {
			t.Fatalf("delta.c accepted a delta we rejected (%v)\norigin %q\ndelta %q", goErr, origin, delta)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("delta.c produced %q, we produced %q\norigin %q\ndelta %q", want, got, origin, delta)
		}
	})
}

func FuzzCRefRoundTrip(f *testing.F) {
	body := strings.Repeat("shared-body-payload-", 8)
	f.Add([]byte(body), []byte(body+"tail"))
	f.Add([]byte(""), []byte("x"))
	f.Add([]byte("0123456789abcdef"), []byte("0123456789abcdefg"))
	f.Fuzz(func(t *testing.T, origin, target []byte) {
		out, err := cref.Apply(origin, Create(origin, target))
		if err != nil {
			t.Fatalf("delta.c rejected our delta: %v\norigin %q\ntarget %q", err, origin, target)
		}
		if !bytes.Equal(out, target) {
			t.Fatalf("delta.c produced %q, want %q", out, target)
		}

		out, err = Apply(origin, cref.Create(origin, target))
		if err != nil {
			t.Fatalf("we rejected a delta.c delta: %v\norigin %q\ntarget %q", err, origin, target)
		}
		if !bytes.Equal(out, target) {
			t.Fatalf("we produced %q, want %q", out, target)
		}
	})
}

// Deltas our own encoder would never emit -- zero-length commands, copies that
// jump backwards or overlap, deltas made only of inserts -- but which the
// format allows and another implementation may well produce. delta.c has to
// agree with us on every one.
func TestCRef_ArbitraryValidDeltas(t *testing.T) {
	rnd := rand.New(rand.NewPCG(227, 229))
	for _, originLen := range []int{0, 1, 16, 17, 256} {
		origin := randomBytes(rnd, originLen)
		for _, commands := range []int{0, 1, 3, 20, 200} {
			for range 30 {
				delta, want := buildDelta(rnd, origin, commands)

				got, err := Apply(origin, delta)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("we rejected a valid delta: %v\ndelta %q", err, delta)
				}
				ref, err := cref.Apply(origin, delta)
				if err != nil {
					t.Fatalf("delta.c rejected a valid delta: %v\ndelta %q", err, delta)
				}
				if !bytes.Equal(ref, want) {
					t.Fatalf("delta.c produced %q, want %q\ndelta %q", ref, want, delta)
				}
			}
		}
	}
}

func FuzzCRefArbitraryValidDeltas(f *testing.F) {
	f.Add(uint64(1), 64, 8)
	f.Add(uint64(2), 0, 3)
	f.Add(uint64(3), 1024, 200)
	f.Fuzz(func(t *testing.T, seed uint64, originLen, commands int) {
		if originLen < 0 || originLen > 1<<16 || commands < 0 || commands > 2000 {
			t.Skip()
		}
		rnd := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))
		origin := randomBytes(rnd, originLen)
		delta, want := buildDelta(rnd, origin, commands)

		got, err := Apply(origin, delta)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("we rejected a valid delta: %v", err)
		}
		ref, err := cref.Apply(origin, delta)
		if err != nil || !bytes.Equal(ref, want) {
			t.Fatalf("delta.c disagrees: err=%v", err)
		}
	})
}

// The format specification says a zero-length copy "indicates that the range
// extends to the end of the original". delta.c does not implement that: it
// copies nothing, and rejects a delta that relies on the documented reading.
// This package follows delta.c, because that is what interoperates. Pinned
// here against the reference so the divergence is a recorded decision.
func TestCRef_ZeroLengthCopyFollowsDeltaC(t *testing.T) {
	origin := []byte("0123456789abcdef")

	// Copying nothing: both accept it and produce nothing.
	empty := appendInt(nil, 0, '\n')
	empty = copyCmd(empty, 0, 4)
	empty = appendInt(empty, checksum(nil), ';')

	got, err := cref.Apply(origin, empty)
	if err != nil || len(got) != 0 {
		t.Fatalf("delta.c on a zero-length copy: err=%v out=%q", err, got)
	}
	got, err = Apply(origin, empty)
	if err != nil || len(got) != 0 {
		t.Fatalf("we differ from delta.c on a zero-length copy: err=%v out=%q", err, got)
	}

	// The specification's reading, that it copies to the end of the source.
	// delta.c rejects this, and so must we.
	toEnd := []byte("456789abcdef")
	spec := appendInt(nil, uint32(len(toEnd)), '\n')
	spec = copyCmd(spec, 0, 4)
	spec = appendInt(spec, checksum(toEnd), ';')

	if _, err := cref.Apply(origin, spec); err == nil {
		t.Fatal("delta.c now implements the specification's zero-length copy; " +
			"this package should be revisited")
	}
	if _, err := Apply(origin, spec); err == nil {
		t.Fatal("we accepted a delta delta.c rejects")
	}
}
