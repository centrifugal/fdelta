package fdelta

import (
	"bytes"
	"testing"
)

// Random tests sample the input space; these cover parts of it completely, so
// that a boundary case cannot survive by never being drawn.

// Every pair of strings over a two-symbol alphabet up to length 10. This is
// entirely below the hash window, so it covers the short-source path and the
// unscanned tail exhaustively: 2047 strings, so about four million pairs.
func TestCreateExhaustiveShortInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive test in short mode")
	}
	var words [][]byte
	for n := range 11 {
		for bits := range 1 << n {
			w := make([]byte, n)
			for i := range n {
				w[i] = byte('a' + (bits>>i)&1)
			}
			words = append(words, w)
		}
	}
	t.Logf("%d words, %d pairs", len(words), len(words)*len(words))

	for _, origin := range words {
		for _, target := range words {
			got, err := Apply(origin, Create(origin, target))
			if err != nil || !bytes.Equal(got, target) {
				t.Fatalf("origin %q target %q: err=%v got %q", origin, target, err, got)
			}
		}
	}
}

// Every single-byte edit of a source, at every position, for lengths either
// side of the hash window and of twice it. These are the lengths where the
// block loop, the scan loop and the tail interact.
func TestCreateExhaustiveSingleEdits(t *testing.T) {
	for _, n := range []int{0, 1, 14, 15, 16, 17, 18, 31, 32, 33, 47, 48, 49, 64} {
		origin := make([]byte, n)
		for i := range origin {
			origin[i] = byte('a' + i%7)
		}

		targets := [][]byte{bytes.Clone(origin)}
		for at := range n {
			for _, b := range []byte{0x00, 'a', 'z', 0x7f, 0x80, 0xff} {
				// Substitute.
				sub := bytes.Clone(origin)
				sub[at] = b
				targets = append(targets, sub)
				// Insert.
				ins := append(append(bytes.Clone(origin[:at]), b), origin[at:]...)
				targets = append(targets, ins)
			}
			// Delete, truncate here, and take only the tail from here.
			targets = append(targets,
				append(bytes.Clone(origin[:at]), origin[at+1:]...),
				bytes.Clone(origin[:at]),
				bytes.Clone(origin[at:]))
		}

		for _, target := range targets {
			got, err := Apply(origin, Create(origin, target))
			if err != nil || !bytes.Equal(got, target) {
				t.Fatalf("origin %d bytes, target %q: err=%v", n, target, err)
			}
			// And in the other direction, so that growing and shrinking are
			// both covered at every length.
			got, err = Apply(target, Create(target, origin))
			if err != nil || !bytes.Equal(got, origin) {
				t.Fatalf("reversed, origin %d bytes, target %q: err=%v", n, target, err)
			}
		}
	}
}

// Every one and two byte input to the integer decoder, so that the digit table
// and the terminating conditions are covered completely rather than sampled.
func TestDecodeIntExhaustiveShort(t *testing.T) {
	digit := func(b byte) (int, bool) {
		i := bytes.IndexByte([]byte(alphabet), b)
		return i, i >= 0
	}

	buf := make([]byte, 1)
	for a := range 256 {
		buf[0] = byte(a)
		v, next := decodeInt(buf, 0)
		if d, ok := digit(byte(a)); ok {
			if v != uint32(d) || next != 1 {
				t.Fatalf("decodeInt(%q) = (%d, %d), want (%d, 1)", buf, v, next, d)
			}
		} else if v != 0 || next != 0 {
			t.Fatalf("decodeInt(%q) = (%d, %d), want (0, 0)", buf, v, next)
		}
	}

	buf = make([]byte, 2)
	for a := range 256 {
		for b := range 256 {
			buf[0], buf[1] = byte(a), byte(b)
			v, next := decodeInt(buf, 0)

			da, aok := digit(byte(a))
			db, bok := digit(byte(b))
			switch {
			case !aok:
				if v != 0 || next != 0 {
					t.Fatalf("decodeInt(%q) = (%d, %d), want (0, 0)", buf, v, next)
				}
			case !bok:
				if v != uint32(da) || next != 1 {
					t.Fatalf("decodeInt(%q) = (%d, %d), want (%d, 1)", buf, v, next, da)
				}
			default:
				if want := uint32(da)*64 + uint32(db); v != want || next != 2 {
					t.Fatalf("decodeInt(%q) = (%d, %d), want (%d, 2)", buf, v, next, want)
				}
			}
		}
	}
}
