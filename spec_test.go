package fdelta

import (
	"bytes"
	"testing"
)

// Conformance against the published format specification at
// https://fossil-scm.org/home/doc/tip/www/delta_format.wiki, using the worked
// examples it gives rather than any implementation's behaviour.

// Section 4.1, "Integer encoding".
func TestSpec_IntegerEncodingExamples(t *testing.T) {
	for _, tc := range []struct {
		v    uint32
		want string
	}{
		{0, "0"},
		{6246, "1Xb"},
		// The spec writes this one as the signed value -1101438770.
		{uint32(4294967296 - 1101438770), "2zMM3E"},
	} {
		got := appendInt(nil, tc.v, ';')
		if string(got[:len(got)-1]) != tc.want {
			t.Errorf("appendInt(%d) = %q, want %q", tc.v, got[:len(got)-1], tc.want)
		}
		if back, next := decodeInt([]byte(tc.want), 0); back != tc.v || next != len(tc.want) {
			t.Errorf("decodeInt(%q) = (%d, %d), want (%d, %d)", tc.want, back, next, tc.v, len(tc.want))
		}
	}
}

// Section 3.0: the alphabet, verbatim from the specification.
func TestSpec_Alphabet(t *testing.T) {
	const want = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz~"
	if alphabet != want {
		t.Fatalf("alphabet = %q, want %q", alphabet, want)
	}
	if len(alphabet) != 64 {
		t.Fatalf("alphabet has %d characters, want 64", len(alphabet))
	}
}

// Section 4.2, "Delta encoding": the specification prints a delta and takes it
// apart command by command. Walking it with this package's decoder must
// reproduce that table exactly.
func TestSpec_DeltaEncodingExample(t *testing.T) {
	const delta = "1Xb\n" +
		"4E@0,2:thFN@4C,6:scenda1B@Jd,6:scenda5x@Kt,6:pieces79@Qt," +
		"F: Example: eskil~E@Y0,2zMM3E;"

	type command struct {
		op   byte
		n    int
		ofst int
		lit  string
	}
	want := []command{
		{'@', 270, 0, ""},
		{':', 2, 0, "th"},
		{'@', 983, 268, ""},
		{':', 6, 0, "scenda"},
		{'@', 75, 1256, ""},
		{':', 6, 0, "scenda"},
		{'@', 380, 1336, ""},
		{':', 6, 0, "pieces"},
		{'@', 457, 1720, ""},
		{':', 15, 0, " Example: eskil"},
		{'@', 4046, 2176, ""},
	}

	b := []byte(delta)
	size, i := decodeInt(b, 0)
	if size != 6246 {
		t.Fatalf("header size = %d, want 6246", size)
	}
	if b[i] != '\n' {
		t.Fatalf("header not terminated by a newline")
	}
	i++

	var got []command
	var total int
	for i < len(b) {
		n, j := decodeInt(b, i)
		op := b[j]
		i = j + 1
		switch op {
		case '@':
			ofst, k := decodeInt(b, i)
			if b[k] != ',' {
				t.Fatalf("copy command at %d not terminated by a comma", i)
			}
			i = k + 1
			got = append(got, command{'@', int(n), int(ofst), ""})
			total += int(n)
		case ':':
			got = append(got, command{':', int(n), 0, string(b[i : i+int(n)])})
			i += int(n)
			total += int(n)
		case ';':
			// The trailer's checksum, as the specification prints it.
			if n != uint32(4294967296-1101438770) {
				t.Fatalf("trailer checksum = %d, want the documented value", n)
			}
			i = len(b)
		default:
			t.Fatalf("unknown operator %q at %d", op, i-1)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("parsed %d commands, the specification lists %d", len(got), len(want))
	}
	for k := range want {
		if got[k] != want[k] {
			t.Errorf("command %d = %+v, the specification says %+v", k, got[k], want[k])
		}
	}
	// The specification's own numbers have to add up to its header.
	if total != int(size) {
		t.Fatalf("the commands produce %d bytes, the header declares %d", total, size)
	}
}

// Section 2.3.2 says "The size zero is special, its usage indicates that the
// range extends to the end of the original."
//
// The reference implementation does not do that: a zero-length copy copies
// nothing, and delta.c rejects a delta that relies on the documented reading
// (TestZeroLengthCopy in the cref tests checks that directly). Every
// implementation of the format follows delta.c here, so this package does too.
// The rule appears to be a documented intention that was never implemented.
//
// No encoder emits a zero-length copy, so nothing turns on it in practice, but
// it is pinned here so the behaviour is a decision on record rather than an
// accident.
func TestSpec_ZeroLengthCopyCopiesNothing(t *testing.T) {
	origin := []byte("0123456789abcdef")
	delta := appendInt(nil, 0, '\n')
	delta = copyCmd(delta, 0, 4)
	delta = appendInt(delta, checksum(nil), ';')

	got, err := Apply(origin, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a zero-length copy produced %q; this package follows delta.c, "+
			"which copies nothing, not the specification's text", got)
	}
}

// Section 2.2 describes the checksum as summing "modulo 2^32-1". The reference
// implementation sums in a 32-bit unsigned integer, so the modulus is 2^32,
// and the checksums in the specification's own example and in its test vectors
// are the ones that arithmetic produces. This pins the modulus that is
// actually interoperable.
func TestSpec_ChecksumModulusIsTwoToThe32(t *testing.T) {
	// Two words that sum past 2^32. Modulo 2^32 the answer wraps to 1; modulo
	// 2^32-1 it would be 2.
	b := []byte{0xff, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x02}
	if got := checksum(b); got != 1 {
		t.Fatalf("checksum = %d, want 1 (the sum taken modulo 2^32)", got)
	}
}

// Section 2.3: "The target is constructed from beginning to end, with the data
// generated by each instruction appended after the data of all previous
// instructions, with no gaps."
func TestSpec_OutputIsCommandsConcatenated(t *testing.T) {
	origin := []byte("0123456789abcdef")
	delta := appendInt(nil, uint32(len("cd")+len("XY")+len("01")), '\n')
	delta = copyCmd(delta, 2, 12)
	delta = insertCmd(delta, "XY")
	delta = copyCmd(delta, 2, 0)
	delta = appendInt(delta, checksum([]byte("cdXY01")), ';')

	got, err := Apply(origin, delta)
	if err != nil || !bytes.Equal(got, []byte("cdXY01")) {
		t.Fatalf("Apply = %q (%v), want %q", got, err, "cdXY01")
	}
}
