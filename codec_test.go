package fdelta

import (
	"bytes"
	"math"
	"math/rand/v2"
	"testing"
)

func TestAppendIntRoundTrips(t *testing.T) {
	values := []uint32{0, 1, 2, 63, 64, 65, 4095, 4096, 1 << 20, math.MaxUint32 - 1, math.MaxUint32}
	rnd := rand.New(rand.NewPCG(71, 73))
	for range 2000 {
		values = append(values, rnd.Uint32())
	}
	for _, v := range values {
		b := appendInt(nil, v, ';')
		if b[len(b)-1] != ';' {
			t.Fatalf("appendInt(%d) did not end with the command byte: %q", v, b)
		}
		got, next := decodeInt(b, 0)
		if got != v {
			t.Fatalf("decodeInt(appendInt(%d)) = %d", v, got)
		}
		if next != len(b)-1 {
			t.Fatalf("decodeInt stopped at %d, want %d (%q)", next, len(b)-1, b)
		}
	}
}

// The encoding is the format's, so it is checked against the specification
// directly: the value written most significant six-bit digit first, in the
// alphabet the format defines.
func TestAppendIntMatchesSpecification(t *testing.T) {
	encode := func(v uint32) []byte {
		if v == 0 {
			return []byte{alphabet[0]}
		}
		var out []byte
		for v > 0 {
			out = append([]byte{alphabet[v%64]}, out...)
			v /= 64
		}
		return out
	}
	rnd := rand.New(rand.NewPCG(79, 83))
	for i := range 5000 {
		v := uint32(i)
		if i > 1000 {
			v = rnd.Uint32()
		}
		want := append(encode(v), ';')
		if got := appendInt(nil, v, ';'); !bytes.Equal(got, want) {
			t.Fatalf("appendInt(%d) = %q, want %q", v, got, want)
		}
	}
}

func TestAppendIntNeverExceedsBuffer(t *testing.T) {
	// maxIntDigits must really bound the digits a uint32 can take, or
	// appendInt would index out of its stack buffer.
	if got := len(appendInt(nil, math.MaxUint32, ';')) - 1; got > maxIntDigits {
		t.Fatalf("MaxUint32 took %d digits, maxIntDigits is %d", got, maxIntDigits)
	}
}

func TestDigitCountMatchesEncoding(t *testing.T) {
	for _, v := range []int{-5, -1, 0, 1, 63, 64, 4095, 4096, 262143, 262144,
		16777216, 1<<30 - 1} {
		want := len(appendInt(nil, uint32(max(v, 0)), ';')) - 1
		if got := digitCount(v); got != want {
			t.Fatalf("digitCount(%d) = %d, want %d", v, got, want)
		}
	}
}

func TestDecodeDigitTable(t *testing.T) {
	// Every alphabet byte decodes to its position, and nothing else decodes at
	// all. In particular bytes above 0x7f must terminate a number: masking the
	// index with 0x7f instead, to fit a 128-entry table, would decode 0x80|c
	// as the digit c.
	for i := range 256 {
		want := int8(-1)
		if idx := bytes.IndexByte([]byte(alphabet), byte(i)); idx >= 0 && i < 128 {
			want = int8(idx)
		}
		if got := decodeDigit[i]; got != want {
			t.Fatalf("decodeDigit[%d] = %d, want %d", i, got, want)
		}
	}
	if decodeDigit[0x80|'0'] != -1 {
		t.Fatal("a high byte must not decode as a digit")
	}
}

func TestDecodeIntStopsAtNonDigit(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint32
		next int
	}{
		{"", 0, 0},
		{"@", 0, 0},    // an empty number is allowed, as in delta.c
		{":abc", 0, 0}, // likewise
		{"0;", 0, 1},
		{"10@", 64, 2},
		{"~,", 63, 1},
		{"3~~~~~,", 1<<32 - 1, 6},
		// 0xb0 is 0x80|'0', so masking the index with 0x7f would decode it as
		// the digit zero and return (640, 2). 0x80 alone does not distinguish
		// the two, since its low seven bits are not a digit either.
		{"A\xb0", 10, 1},
		{"A\x80", 10, 1},
	} {
		got, next := decodeInt([]byte(tc.in), 0)
		if got != tc.want || next != tc.next {
			t.Fatalf("decodeInt(%q) = (%d, %d), want (%d, %d)", tc.in, got, next, tc.want, tc.next)
		}
	}
}

func TestDecodeIntFromOffset(t *testing.T) {
	b := []byte("xx10@")
	if got, next := decodeInt(b, 2); got != 64 || next != 4 {
		t.Fatalf("decodeInt from offset = (%d, %d), want (64, 4)", got, next)
	}
}

// delta.c's digit_count walks a threshold upward in a signed int, which
// overflows to zero for v >= 2^30 and leaves the loop spinning: the reference
// encoder does not terminate on payloads of a gibibyte or more. This one is
// total, and must keep returning the mathematically right answer past that
// point, since the encoder prices copy commands with it.
func TestDigitCountIsTotal(t *testing.T) {
	for _, tc := range []struct {
		v    int
		want int
	}{
		{1<<30 - 1, 5},
		{1 << 30, 6},
		{math.MaxInt32, 6},
	} {
		if got := digitCount(tc.v); got != tc.want {
			t.Fatalf("digitCount(%d) = %d, want %d", tc.v, got, tc.want)
		}
	}

	// And it must agree with what the encoder actually writes, right to the
	// top of the range a delta can represent. Values above maxInt cannot be
	// passed as an int at all on a 32-bit platform, and the encoder never
	// produces them there, since they could not index a slice.
	for _, v := range []uint32{1<<30 - 1, 1 << 30, 1 << 31, math.MaxUint32} {
		if uint64(v) > uint64(maxInt) {
			continue
		}
		if got, want := digitCount(int(v)), len(appendInt(nil, v, ';'))-1; got != want {
			t.Fatalf("digitCount(%d) = %d but the encoder writes %d digits", v, got, want)
		}
	}
}
