//go:build exhaustive

package fdelta

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// Exhaustive verification over the whole 32-bit domain. These are not samples:
// they check every value the format can encode, so they establish the
// properties rather than give evidence for them.
//
// They take tens of seconds, so they are behind a build tag:
//
//	make exhaustive
//
// CI runs them on its weekly schedule.

// For every uint32: the encoder writes it, the decoder reads back exactly it,
// stops in exactly the right place, and digitCount predicted exactly the
// number of digits written.
func TestExhaustive_CodecRoundTrip(t *testing.T) {
	shards := runtime.NumCPU()
	const span = 1 << 32
	var checked atomic.Uint64

	var wg sync.WaitGroup
	for s := range shards {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			buf := make([]byte, 0, maxIntDigits+1)
			lo := uint64(s) * (span / uint64(shards))
			hi := lo + span/uint64(shards)
			if s == shards-1 {
				hi = span
			}
			for x := lo; x < hi; x++ {
				v := uint32(x)
				buf = appendInt(buf[:0], v, ';')

				if buf[len(buf)-1] != ';' {
					t.Errorf("appendInt(%d) lost the command byte", v)
					return
				}
				digits := len(buf) - 1
				if digits < 1 || digits > maxIntDigits {
					t.Errorf("appendInt(%d) wrote %d digits", v, digits)
					return
				}
				if got := digitCount(int(v)); got != digits {
					t.Errorf("digitCount(%d) = %d, encoder wrote %d", v, got, digits)
					return
				}
				if v != 0 && buf[0] == '0' {
					t.Errorf("appendInt(%d) wrote a leading zero: %q", v, buf)
					return
				}
				back, next := decodeInt(buf, 0)
				if back != v {
					t.Errorf("decodeInt(appendInt(%d)) = %d", v, back)
					return
				}
				if next != digits {
					t.Errorf("decodeInt(%q) stopped at %d, want %d", buf, next, digits)
					return
				}
			}
			checked.Add(hi - lo)
		}(s)
	}
	wg.Wait()
	if n := checked.Load(); n != span {
		t.Fatalf("checked %d values, want %d", n, uint64(span))
	}
	t.Logf("verified all %d uint32 values", span)
}

// The number of digits can only change at a power of 64, so monotonicity is
// established by checking the two values either side of each boundary rather
// than by walking the domain again.
func TestExhaustive_DigitCountBoundaries(t *testing.T) {
	buf := make([]byte, 0, maxIntDigits+1)
	width := func(v uint32) int {
		buf = appendInt(buf[:0], v, ';')
		return len(buf) - 1
	}
	for n := 1; n <= maxIntDigits; n++ {
		last := uint64(1)<<(6*n) - 1 // largest value of n digits
		if width(uint32(last)) != n {
			t.Fatalf("%d should take %d digits, took %d", last, n, width(uint32(last)))
		}
		if last+1 < 1<<32 {
			if got := width(uint32(last + 1)); got != n+1 {
				t.Fatalf("%d should take %d digits, took %d", last+1, n+1, got)
			}
		}
	}
}

// The checksum reads its input as big-endian words with zero padding, and the
// padding is where a tail-length bug would hide. Every input of length zero to
// three is checked against that definition directly: 16.8 million cases, which
// is the whole of the space where the tail handling applies.
func TestExhaustive_ChecksumShortInputs(t *testing.T) {
	spec := func(b []byte) uint32 {
		var sum uint32
		for i := 0; i < len(b); i += 4 {
			var w uint32
			for j := range 4 {
				if i+j < len(b) {
					w |= uint32(b[i+j]) << (24 - 8*j)
				}
			}
			sum += w
		}
		return sum
	}

	if checksum(nil) != 0 {
		t.Fatal("checksum of nothing must be zero")
	}
	buf := make([]byte, 3)
	for a := range 256 {
		buf[0] = byte(a)
		if got, want := checksum(buf[:1]), spec(buf[:1]); got != want {
			t.Fatalf("checksum(%v) = %#08x, want %#08x", buf[:1], got, want)
		}
		for b := range 256 {
			buf[1] = byte(b)
			if got, want := checksum(buf[:2]), spec(buf[:2]); got != want {
				t.Fatalf("checksum(%v) = %#08x, want %#08x", buf[:2], got, want)
			}
			for c := range 256 {
				buf[2] = byte(c)
				if got, want := checksum(buf[:3]), spec(buf[:3]); got != want {
					t.Fatalf("checksum(%v) = %#08x, want %#08x", buf[:3], got, want)
				}
			}
		}
	}
	t.Log("verified every input of length 0 to 3")
}
