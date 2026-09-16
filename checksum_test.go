package fdelta

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"
)

// The value is defined as the input read as big-endian words with zero
// padding. Stating that independently guards against both implementations
// drifting together.
func TestChecksumIsSumOfBigEndianWords(t *testing.T) {
	rnd := rand.New(rand.NewPCG(101, 103))
	for n := range 200 {
		b := randomBytes(rnd, n)
		padded := make([]byte, (len(b)+3)/4*4)
		copy(padded, b)
		var want uint32
		for i := 0; i < len(padded); i += 4 {
			want += binary.BigEndian.Uint32(padded[i:])
		}
		if got := checksum(b); got != want {
			t.Fatalf("checksum of %d bytes = %#08x, want %#08x", n, got, want)
		}
	}
}

func TestChecksumEmpty(t *testing.T) {
	if got := checksum(nil); got != 0 {
		t.Fatalf("checksum(nil) = %#08x, want 0", got)
	}
}

func TestChecksumAllHighBytes(t *testing.T) {
	// All-0xff input maximises the carries between the packed words.
	for _, n := range []int{1, 3, 4, 15, 16, 17, 1 << 12} {
		b := make([]byte, n)
		for i := range b {
			b[i] = 0xff
		}
		want := uint32(0)
		for i := 0; i < n; i += 4 {
			w := uint32(0)
			for j := range 4 {
				if i+j < n {
					w |= 0xff << (24 - 8*j)
				}
			}
			want += w
		}
		if got := checksum(b); got != want {
			t.Fatalf("checksum of %d high bytes = %#08x, want %#08x", n, got, want)
		}
	}
}
