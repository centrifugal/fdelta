package fdelta

import (
	"bytes"
	"strings"
	"testing"
)

// The append forms document that dst may not overlap the other arguments.
// These pin down what that does and does not cover, so the contract is a
// tested statement rather than a hopeful sentence in a doc comment.

// dst sharing an array with origin is the mistake a caller reusing buffers is
// most likely to make: applying a delta straight back over its own base.
func TestAppendApplyOverlappingDstIsNotSilentlyWrong(t *testing.T) {
	body := strings.Repeat("shared-body-payload-", 64)
	base := []byte(body)
	target := []byte(body + "tail")
	delta := Create(base, target)

	// The correct pattern: two buffers, applying into the one the base does
	// not live in.
	scratch := make([]byte, 0, len(target))
	out, err := AppendApply(scratch, base, delta)
	if err != nil {
		t.Fatalf("AppendApply into a separate buffer: %v", err)
	}
	if !bytes.Equal(out, target) {
		t.Fatal("applying into a separate buffer gave the wrong result")
	}

	// Applying into the base's own array is what the docs forbid, so the
	// result is not specified and this deliberately does not assert one.
	// What it does check is that the forbidden call stays inside Go's memory
	// safety: no panic, no read or write out of bounds under -race. Asserting
	// a particular answer here would be asserting behaviour the package does
	// not promise, which is how a test becomes flaky for a legitimate reason.
	self := make([]byte, len(base), len(base)+len(target))
	copy(self, base)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("overlapping dst panicked: %v", r)
			}
		}()
		got, err := AppendApply(self[:0], self[:len(base)], delta)
		t.Logf("overlapping dst (unspecified): err=%v, happened to be correct=%v",
			err, bytes.Equal(got, target))
	}()
}

// Create with origin and target sharing one array is legitimate and common:
// it is how an unchanged payload is encoded.
func TestCreateWithIdenticalSlices(t *testing.T) {
	buf := []byte(strings.Repeat("shared-body-payload-", 64))
	delta := Create(buf, buf)
	out, err := Apply(buf, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(out, buf) {
		t.Fatal("origin and target sharing an array did not round trip")
	}

	// And overlapping windows of one array.
	long := []byte(strings.Repeat("shared-body-payload-", 128))
	a, b := long[:len(long)/2], long[len(long)/4:3*len(long)/4]
	out, err = Apply(a, Create(a, b))
	if err != nil {
		t.Fatalf("Apply of overlapping windows: %v", err)
	}
	if !bytes.Equal(out, b) {
		t.Fatal("overlapping origin and target windows did not round trip")
	}
}

// Apply must never hand back a slice that aliases either input, or a later
// write to origin would rewrite data the caller already holds.
func TestApplyResultNeverAliasesInputs(t *testing.T) {
	for _, tc := range corpus() {
		if len(tc.target) == 0 {
			continue
		}
		delta := Create(tc.origin, tc.target)
		out, err := Apply(tc.origin, delta)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(tc.origin) > 0 && &out[0] == &tc.origin[0] {
			t.Fatalf("%s: result aliases origin", tc.name)
		}
		if &out[0] == &delta[0] {
			t.Fatalf("%s: result aliases the delta", tc.name)
		}
	}
}
