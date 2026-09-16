package fdelta

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// seedPairs are origin/target seeds shaped like the inputs the encoder is
// built for, so the fuzzer starts from deltas with real structure rather than
// from noise that never gets past the header.
func seedPairs(f *testing.F) {
	body := strings.Repeat("shared-body-payload-", 8)
	f.Add([]byte(body), []byte(body+"tail"))
	f.Add([]byte(body), []byte("x"+body))
	f.Add([]byte(body+body), []byte(body))
	f.Add([]byte(""), []byte("x"))
	f.Add([]byte("x"), []byte(""))
	f.Add([]byte("0123456789abcdef"), []byte("0123456789abcdefg"))
	f.Add([]byte(jsonDoc(2<<10, 1)), []byte(jsonDoc(2<<10, 2)))
	f.Add(make([]byte, 1024), append(make([]byte, 1023), 1))
	f.Add(bytes.Repeat([]byte{0xff}, 512), bytes.Repeat([]byte{0x80}, 512))
}

// seedDeltas are delta-shaped seeds, including every malformed delta the tests
// call out by name. Seeding with these matters: a fuzzer starting from
// unstructured bytes spends its whole budget failing to parse a header and
// never reaches the command loop where the interesting bugs live.
func seedDeltas(f *testing.F) {
	origin := []byte(strings.Repeat("shared-body-payload-", 8))
	f.Add(origin, Create(origin, append(bytes.Clone(origin), "tail"...)))
	f.Add(origin, Create(origin, origin))
	for _, s := range []string{
		"", "2", "2\n", "\n", "0\n0;", "2 2@0,",
		"2\n2@0,", "2\n2!0,", "2\n2@0!", "2\n2@0,0;",
		"A\n7:000000", "K\nA:0123456789A:xy", "A\nA:012",
		"2\n2@3~~~~~,", "2\n2@~,", "2\n~@0,", "3~~~~~\n2@0,",
		"1\n2@0,", "1\n2:ab", "A\n3~~~~~:xy", "Z\n2@0,0;",
	} {
		f.Add(origin, []byte(s))
	}
	f.Add([]byte("0"), []byte("A\n7:000000"))
	f.Add([]byte("origin"), []byte("garbage"))
}

// Whatever Create produces, Apply must turn back into exactly the target. That
// the delta is also one Fossil reads is fuzzed under the cref tag, against
// src/delta.c itself.
func FuzzRoundTrip(f *testing.F) {
	seedPairs(f)
	f.Fuzz(func(t *testing.T, origin, target []byte) {
		delta := Create(origin, target)

		got, err := Apply(origin, delta)
		if err != nil {
			t.Fatalf("Apply rejected our own delta: %v", err)
		}
		if !bytes.Equal(got, target) {
			t.Fatalf("round trip produced %q, want %q", got, target)
		}
	})
}

// Apply takes deltas straight off the network, so no input may panic it, it
// must leave its inputs alone, and whatever it accepts must come out at the
// size the delta declared. That it is never more lenient than Fossil is fuzzed
// under the cref tag.
func FuzzApply(f *testing.F) {
	seedDeltas(f)
	f.Fuzz(func(t *testing.T, origin, delta []byte) {
		originCopy := slices.Clone(origin)
		deltaCopy := slices.Clone(delta)

		out, err := Apply(origin, delta)

		if !bytes.Equal(origin, originCopy) {
			t.Fatal("Apply modified origin")
		}
		if !bytes.Equal(delta, deltaCopy) {
			t.Fatal("Apply modified the delta")
		}

		if err != nil {
			if out != nil {
				t.Fatalf("Apply returned %q alongside an error", out)
			}
			return
		}

		// A declared size that Apply honoured must match what came out.
		if declared, sizeErr := OutputSize(delta); sizeErr != nil || declared != len(out) {
			t.Fatalf("Apply produced %d bytes; OutputSize says %d (%v)", len(out), declared, sizeErr)
		}
	})
}

// The appending forms must be indistinguishable from the plain ones, and must
// leave dst alone whenever they fail.
func FuzzAppendForms(f *testing.F) {
	seedPairs(f)
	f.Fuzz(func(t *testing.T, origin, target []byte) {
		prefix := []byte("prefix")

		delta := Create(origin, target)
		got := AppendCreate(slices.Clone(prefix), origin, target)
		if !bytes.HasPrefix(got, prefix) || !bytes.Equal(got[len(prefix):], delta) {
			t.Fatalf("AppendCreate = %q, want %q + %q", got, prefix, delta)
		}

		out, err := Apply(origin, delta)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		gotApply, err := AppendApply(slices.Clone(prefix), origin, delta)
		if err != nil {
			t.Fatalf("AppendApply: %v", err)
		}
		if !bytes.HasPrefix(gotApply, prefix) || !bytes.Equal(gotApply[len(prefix):], out) {
			t.Fatalf("AppendApply = %q, want %q + %q", gotApply, prefix, out)
		}
	})
}

// AppendApply promises dst is untouched when it fails, which is what lets a
// caller reuse a buffer without checking.
func FuzzAppendApplyPreservesDst(f *testing.F) {
	seedDeltas(f)
	f.Fuzz(func(t *testing.T, origin, delta []byte) {
		prefix := []byte("do not touch me")
		dst := slices.Clone(prefix)
		got, err := AppendApply(dst, origin, delta)
		if err == nil {
			return
		}
		if !bytes.Equal(got, prefix) {
			t.Fatalf("AppendApply returned %q on error, want dst unchanged", got)
		}
		if !bytes.Equal(dst, prefix) {
			t.Fatalf("AppendApply modified dst in place on error: %q", dst)
		}
	})
}
