package fdelta

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// Every delta the suite feeds to Apply elsewhere is one this package's own
// encoder produced, so the shapes tested are only the shapes it happens to
// emit. The format allows a great deal more: zero-length commands, copies that
// jump backwards, copies that read the same bytes twice, deltas made entirely
// of inserts, thousands of tiny commands. A decoder has to be right about all
// of them, and a real peer may be a different implementation that emits them.
//
// buildDelta assembles an arbitrary valid delta directly from commands, and
// returns the output it must produce.
func buildDelta(rnd *rand.Rand, origin []byte, commands int) (delta, want []byte) {
	var out []byte
	var body []byte

	for range commands {
		switch {
		case len(origin) > 0 && rnd.IntN(2) == 0:
			// Copy: any offset, any length that stays inside origin,
			// including zero length and the whole of it.
			ofst := rnd.IntN(len(origin) + 1)
			cnt := rnd.IntN(len(origin) - ofst + 1)
			body = appendInt(body, uint32(cnt), '@')
			body = appendInt(body, uint32(ofst), ',')
			out = append(out, origin[ofst:ofst+cnt]...)
		default:
			// Insert: any bytes, including none.
			n := rnd.IntN(24)
			lit := make([]byte, n)
			for i := range lit {
				lit[i] = byte(rnd.IntN(256))
			}
			body = appendInt(body, uint32(n), ':')
			body = append(body, lit...)
			out = append(out, lit...)
		}
	}

	delta = appendInt(nil, uint32(len(out)), '\n')
	delta = append(delta, body...)
	delta = appendInt(delta, checksum(out), ';')
	return delta, out
}

func TestApplyOnArbitraryValidDeltas(t *testing.T) {
	rnd := rand.New(rand.NewPCG(211, 223))
	for _, originLen := range []int{0, 1, 15, 16, 17, 64, 1024} {
		origin := randomBytes(rnd, originLen)
		for _, commands := range []int{0, 1, 2, 5, 50, 500} {
			for range 40 {
				delta, want := buildDelta(rnd, origin, commands)

				got, err := Apply(origin, delta)
				if err != nil {
					t.Fatalf("origin %d bytes, %d commands: %v\ndelta %q", originLen, commands, err, delta)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("origin %d bytes, %d commands:\n got  %q\n want %q\n delta %q",
						originLen, commands, got, want, delta)
				}

				// The appending form must agree, and OutputSize must have
				// predicted the size.
				buf, err := AppendApply(nil, origin, delta)
				if err != nil || !bytes.Equal(buf, want) {
					t.Fatalf("AppendApply disagrees: err=%v", err)
				}
				if size, err := OutputSize(delta); err != nil || size != len(want) {
					t.Fatalf("OutputSize = %d (%v), want %d", size, err, len(want))
				}
			}
		}
	}
}

// The shapes above, but each one deliberately rather than by chance.
//
// The commands are built with the encoder rather than written out as base-64
// by hand: the alphabet is 0-9A-Z_a-z~, so 'g' is 43 and not 16, and hand
// encoding offsets is a reliable way to write a test that fails for the wrong
// reason.
func copyCmd(dst []byte, cnt, ofst int) []byte {
	dst = appendInt(dst, uint32(cnt), '@')
	return appendInt(dst, uint32(ofst), ',')
}

func insertCmd(dst []byte, lit string) []byte {
	dst = appendInt(dst, uint32(len(lit)), ':')
	return append(dst, lit...)
}

func TestApplyOnEdgeShapedDeltas(t *testing.T) {
	origin := []byte("0123456789abcdef") // 16 bytes
	for _, tc := range []struct {
		name string
		body func([]byte) []byte
		want string
	}{
		{"nothing at all", func(b []byte) []byte { return b }, ""},
		{"zero length copy", func(b []byte) []byte { return copyCmd(b, 0, 0) }, ""},
		{"zero length copy at the end", func(b []byte) []byte { return copyCmd(b, 0, 16) }, ""},
		{"zero length insert", func(b []byte) []byte { return insertCmd(b, "") }, ""},
		{"copy the whole source", func(b []byte) []byte { return copyCmd(b, 16, 0) }, "0123456789abcdef"},
		{"copy the same bytes twice", func(b []byte) []byte { return copyCmd(copyCmd(b, 4, 0), 4, 0) }, "01230123"},
		{"copies jumping backwards", func(b []byte) []byte { return copyCmd(copyCmd(b, 4, 12), 4, 0) }, "cdef0123"},
		{"copy the last byte", func(b []byte) []byte { return copyCmd(b, 1, 15) }, "f"},
		{"overlapping copies", func(b []byte) []byte { return copyCmd(copyCmd(b, 8, 0), 8, 4) }, "01234567456789ab"},
		{"inserts only", func(b []byte) []byte { return insertCmd(insertCmd(b, "abc"), "def") }, "abcdef"},
		{"insert then copy then insert", func(b []byte) []byte {
			return insertCmd(copyCmd(insertCmd(b, "x"), 2, 0), "y")
		}, "x01y"},
		{"many tiny copies", func(b []byte) []byte {
			for i := range 4 {
				b = copyCmd(b, 1, i)
			}
			return b
		}, "0123"},
		{"zero length among real ones", func(b []byte) []byte {
			return copyCmd(insertCmd(copyCmd(copyCmd(b, 1, 0), 0, 5), ""), 1, 1)
		}, "01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delta := appendInt(nil, uint32(len(tc.want)), '\n')
			delta = tc.body(delta)
			delta = appendInt(delta, checksum([]byte(tc.want)), ';')

			got, err := Apply(origin, delta)
			if err != nil {
				t.Fatalf("Apply(%q): %v", delta, err)
			}
			if string(got) != tc.want {
				t.Fatalf("Apply(%q) = %q, want %q", delta, got, tc.want)
			}
		})
	}
}
