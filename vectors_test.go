package fdelta

import (
	"bytes"
	"embed"
	"fmt"
	"path"
	"testing"
)

// Frozen test vectors whose deltas were produced by Fossil itself:
// generate-deltas.sh in the same directory built each one with
// `fossil test-delta-create`, so these are the reference implementation's own
// output, captured. Everything else in the suite compares this package against
// implementations running right now, which cannot catch the two of them
// drifting together. These bytes cannot drift.
//
//go:embed testdata/fossil-vectors
var vectorFS embed.FS

type vector struct {
	name                   string
	origin, target, stored []byte
}

func vectors(tb testing.TB) []vector {
	tb.Helper()
	read := func(p string) []byte {
		b, err := vectorFS.ReadFile(path.Join("testdata/fossil-vectors", p))
		if err != nil {
			tb.Fatal(err)
		}
		return b
	}
	var out []vector
	for i := 1; i <= 5; i++ {
		out = append(out, vector{
			name:   fmt.Sprintf("vector-%d", i),
			origin: read(fmt.Sprintf("%d/origin", i)),
			target: read(fmt.Sprintf("%d/target", i)),
			stored: read(fmt.Sprintf("%d/delta", i)),
		})
	}
	return out
}

// Our deltas must be no larger than the ones Fossil produced for the same
// inputs.
//
// The two encoders are not expected to emit the same bytes: this one indexes
// its blocks differently (see bucketIndex) and reads the hash window as
// unsigned where delta.c reads it as signed, so on some inputs they reach
// different, equally valid deltas. What must not happen is this package
// compressing worse than the implementation that defines the format.
func TestFossilVectors_NoWorseThanFossil(t *testing.T) {
	for _, v := range vectors(t) {
		t.Run(v.name, func(t *testing.T) {
			if got := len(Create(v.origin, v.target)); got > len(v.stored) {
				t.Fatalf("our delta is %d bytes, Fossil produced %d", got, len(v.stored))
			}
		})
	}
}

// And Apply must turn Fossil's stored deltas back into the targets.
func TestFossilVectors_ApplyStoredDelta(t *testing.T) {
	for _, v := range vectors(t) {
		t.Run(v.name, func(t *testing.T) {
			got, err := Apply(v.origin, v.stored)
			if err != nil {
				t.Fatalf("Apply of a stored Fossil delta: %v", err)
			}
			if !bytes.Equal(got, v.target) {
				t.Fatalf("Apply produced %d bytes, want the %d byte target", len(got), len(v.target))
			}
		})
	}
}

func TestFossilVectors_RoundTrip(t *testing.T) {
	for _, v := range vectors(t) {
		t.Run(v.name, func(t *testing.T) {
			got, err := Apply(v.origin, Create(v.origin, v.target))
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if !bytes.Equal(got, v.target) {
				t.Fatal("round trip did not reproduce the target")
			}
		})
	}
}

// The stored deltas also have to survive the checks Apply makes, so corrupting
// them must be caught rather than silently producing something else.
func TestFossilVectors_CorruptedStoredDelta(t *testing.T) {
	for _, v := range vectors(t) {
		t.Run(v.name, func(t *testing.T) {
			for i := range v.stored {
				corrupt := bytes.Clone(v.stored)
				corrupt[i] ^= 0x01
				out, err := Apply(v.origin, corrupt)
				if err == nil && !bytes.Equal(out, v.target) {
					t.Fatalf("corruption at byte %d was accepted with wrong output", i)
				}
			}
		})
	}
}

// One of the vectors is a bitmap, so the corpus reaches bytes above 0x7f,
// where the integer decoder and the window hash both behave differently from
// ASCII. Guard against someone replacing the fixtures with text only.
func TestFossilVectors_CoverHighBytes(t *testing.T) {
	var sawHigh, sawShortOrigin bool
	for _, v := range vectors(t) {
		for _, b := range v.origin {
			if b >= 0x80 {
				sawHigh = true
				break
			}
		}
		if len(v.origin) <= hashWindow {
			sawShortOrigin = true
		}
	}
	if !sawHigh {
		t.Error("no vector contains bytes above 0x7f")
	}
	if !sawShortOrigin {
		t.Error("no vector has an origin at or below the hash window")
	}
}
