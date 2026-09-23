package fdelta

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestApplyRoundTrips(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			delta := Create(tc.origin, tc.target)

			out, err := Apply(tc.origin, delta)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if !bytes.Equal(out, tc.target) {
				t.Fatalf("Apply produced %q, want %q", out, tc.target)
			}

		})
	}
}

// Malformed deltas arrive from the network, so every one of these must come
// back as an error rather than a panic or wrong data.
func TestApplyRejectsMalformed(t *testing.T) {
	origin := []byte("hello world, this is the source text")
	for _, tc := range []struct {
		name  string
		delta string
		// The exact error, not just the sentinel it wraps. Asserting only the
		// sentinel is too weak to be useful here: a bounds check that stops
		// working usually lets parsing run off the end of the delta instead,
		// which also reports an invalid delta, so the test would still pass.
		// Mutation testing found several checks that were unguarded for
		// precisely that reason.
		want error
	}{
		// The first three run past the end of one of their inputs unless the
		// bounds checks are exactly right, and a direct transcription of
		// delta.c indexes out of bounds on them.
		{"insert past end, count within delta", "A\n7:000000", errInsertPastDelta},
		{"insert past end after an earlier insert", "K\nA:0123456789A:xy", errInsertPastDelta},
		// "3~~~~~" is 0xFFFFFFFF: ofst+cnt wraps to 1 in 32-bit arithmetic, so
		// a check of that width passes and the copy runs from 0xFFFFFFFF to 1.
		{"copy offset wraps 32 bits", "2\n2@3~~~~~,", errCopyPastSource},
		// The same, but terminated, so validation runs to the end rather than
		// stopping early. Without the widened check this reaches the copy.
		{"copy offset wraps 32 bits, terminated", "2\n2@3~~~~~,0;", errCopyPastSource},

		{"empty", "", errSizeTerminator},
		{"only a size", "2", errSizeTerminator},
		{"size not terminated by newline", "2 2@0,", errSizeTerminator},
		{"nothing but a newline", "\n", errUnterminated},
		{"no terminator command", "2\n2@0,", errUnterminated},
		{"nothing after the newline", "2\n", errUnterminated},
		{"unknown operator", "2\n2!0,", errUnknownOperator},
		{"copy not terminated by comma", "2\n2@0!", errCopyTerminator},
		{"copy past end of source", "2\n2@~,", errCopyPastSource},
		{"copy count past end of source", "2\n~@0,", errCopyExceedsSize},
		{"copy exceeds declared size", "1\n2@0,", errCopyExceedsSize},
		{"declared size smaller than output", "1\n2@0,0;", errCopyExceedsSize},
		{"insert exceeds declared size", "1\n2:ab", errInsertExceeds},
		{"insert count huge", "A\n3~~~~~:xy", errInsertExceeds},
		{"declared size larger than output", "Z\n2@0,0;", errSizeMismatch},
		{"truncated mid insert", "A\nA:012", errInsertPastDelta},
		{"checksum wrong", "2\n2@0,0;", ErrChecksumMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out []byte
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Apply panicked: %v", r)
					}
				}()
				out, err = Apply(origin, []byte(tc.delta))
			}()
			if err == nil {
				t.Fatalf("Apply accepted %q and returned %q", tc.delta, out)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Apply(%q) = %v, want %v", tc.delta, err, tc.want)
			}
			// And it must still route to the right public sentinel.
			wantSentinel := ErrInvalidDelta
			if errors.Is(tc.want, ErrChecksumMismatch) {
				wantSentinel = ErrChecksumMismatch
			}
			if !errors.Is(err, wantSentinel) {
				t.Fatalf("Apply(%q) = %v, want it to wrap %v", tc.delta, err, wantSentinel)
			}
			if out != nil {
				t.Fatalf("Apply returned %q alongside an error", out)
			}
		})
	}
}

// A delta applied to the wrong source has to be distinguishable from a
// corrupt one: a client whose base payload has drifted needs to ask for the
// payload in full, not treat the connection as broken.
func TestApplyWrongSourceIsChecksumMismatch(t *testing.T) {
	body := strings.Repeat("shared-body-payload-", 64)
	origin := []byte(body)
	target := []byte(body + "tail")
	delta := Create(origin, target)

	wrong := slices.Clone(origin)
	wrong[len(wrong)/2] ^= 0xff

	_, err := Apply(wrong, delta)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Apply to the wrong source = %v, want ErrChecksumMismatch", err)
	}
	if errors.Is(err, ErrInvalidDelta) {
		t.Fatal("a checksum mismatch must not also report an invalid delta")
	}
}

// The checksum is a plain sum of big-endian words, so a wrong source that
// leaves the sum unchanged goes undetected. This pins the limit the
// documentation describes, so that the documentation stays true: two values
// swapped eight bytes apart are in the same byte lane and cancel out.
func TestApplyChecksumMissesSumPreservingDrift(t *testing.T) {
	origin := []byte(`{"user":"alice","status":"online","x":1,"yyy":5,"score":1200,"rank":"gold","pad":"0123456789"}`)
	target := []byte(`{"user":"alice","status":"online","x":1,"yyy":5,"score":1350,"rank":"gold","pad":"0123456789"}`)
	drifted := []byte(`{"user":"alice","status":"online","x":5,"yyy":1,"score":1200,"rank":"gold","pad":"0123456789"}`)
	if checksum(drifted) != checksum(origin) {
		t.Fatal("the drifted source no longer has the same checksum; the test needs a new example")
	}

	out, err := Apply(drifted, Create(origin, target))
	if err != nil {
		t.Fatalf("Apply = %v; if the checksum now catches this, update the docs that say it cannot", err)
	}
	if bytes.Equal(out, target) {
		t.Fatal("the delta does not copy the drifted bytes, so this example shows nothing")
	}
}

// Every single-bit corruption of a valid delta must either be rejected or
// produce exactly the original target, never anything else and never a panic.
func TestApplyCorruptedDelta(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	origin := []byte(strings.Repeat("shared-body-payload-", 64))
	target := []byte(strings.Repeat("shared-body-payload-", 64) + "tail")
	delta := Create(origin, target)

	for i := range delta {
		for bit := range 8 {
			corrupt := slices.Clone(delta)
			corrupt[i] ^= 1 << bit
			out, err := Apply(origin, corrupt)
			if err == nil && !bytes.Equal(out, target) {
				t.Fatalf("corrupt delta accepted with wrong output at byte %d bit %d:\ngot  %q\nwant %q",
					i, bit, out, target)
			}
		}
	}
}

// Truncation at every offset must be rejected or exact, never a panic.
func TestApplyTruncatedDelta(t *testing.T) {
	origin := []byte(strings.Repeat("shared-body-payload-", 64))
	target := []byte(strings.Repeat("shared-body-payload-", 64) + "tail")
	delta := Create(origin, target)

	for i := range len(delta) {
		out, err := Apply(origin, delta[:i])
		if err == nil && !bytes.Equal(out, target) {
			t.Fatalf("delta truncated to %d bytes was accepted with wrong output", i)
		}
	}
}

func TestAppendApply(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			delta := Create(tc.origin, tc.target)

			got, err := AppendApply(nil, tc.origin, delta)
			if err != nil {
				t.Fatalf("AppendApply(nil): %v", err)
			}
			if !bytes.Equal(got, tc.target) {
				t.Fatalf("AppendApply(nil) = %q, want %q", got, tc.target)
			}

			prefix := []byte("keep me")
			got, err = AppendApply(slices.Clone(prefix), tc.origin, delta)
			if err != nil {
				t.Fatalf("AppendApply: %v", err)
			}
			if !bytes.HasPrefix(got, prefix) {
				t.Fatalf("AppendApply overwrote the prefix: %q", got)
			}
			if !bytes.Equal(got[len(prefix):], tc.target) {
				t.Fatalf("AppendApply appended %q, want %q", got[len(prefix):], tc.target)
			}
		})
	}
}

// AppendApply promises dst is untouched on error. That is only possible
// because the delta is validated before anything is written.
func TestAppendApplyLeavesDstOnError(t *testing.T) {
	origin := []byte(strings.Repeat("shared-body-payload-", 64))
	prefix := []byte("keep me exactly as I am")

	for _, delta := range []string{"A\n7:000000", "2\n2@3~~~~~,", "2\n2@0,0;", "", "nonsense"} {
		dst := slices.Clone(prefix)
		got, err := AppendApply(dst, origin, []byte(delta))
		if err == nil {
			t.Fatalf("AppendApply accepted %q", delta)
		}
		if !bytes.Equal(got, prefix) {
			t.Fatalf("AppendApply(%q) returned %q, want dst unchanged as %q", delta, got, prefix)
		}
		if !bytes.Equal(dst, prefix) {
			t.Fatalf("AppendApply(%q) modified dst in place: %q", delta, dst)
		}
	}
}

func TestAppendApplyReusesBuffer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(53, 59))
	buf := make([]byte, 0, 64)
	for range 200 {
		origin := randomBytes(rnd, rnd.IntN(2048))
		target := mutate(rnd, origin)
		var err error
		buf, err = AppendApply(buf[:0], origin, Create(origin, target))
		if err != nil {
			t.Fatalf("AppendApply: %v", err)
		}
		if !bytes.Equal(buf, target) {
			t.Fatalf("reused buffer gave %q, want %q", buf, target)
		}
	}
}

func TestApplyDoesNotModifyInputs(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			delta := Create(tc.origin, tc.target)
			origin, d := slices.Clone(tc.origin), slices.Clone(delta)
			if _, err := Apply(origin, d); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if !bytes.Equal(origin, tc.origin) {
				t.Error("Apply modified origin")
			}
			if !bytes.Equal(d, delta) {
				t.Error("Apply modified the delta")
			}
		})
	}
}

// The result must not share memory with either input, or a later change to
// origin would silently rewrite data a caller already holds.
func TestApplyResultDoesNotAlias(t *testing.T) {
	origin := []byte(strings.Repeat("shared-body-payload-", 64))
	target := slices.Clone(origin)
	delta := Create(origin, target)

	out, err := Apply(origin, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	origin[0] ^= 0xff
	if out[0] == origin[0] {
		t.Fatal("Apply returned a slice aliasing origin")
	}
}

func TestOutputSize(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := OutputSize(Create(tc.origin, tc.target))
			if err != nil {
				t.Fatalf("OutputSize: %v", err)
			}
			if got != len(tc.target) {
				t.Fatalf("OutputSize = %d, want %d", got, len(tc.target))
			}
		})
	}
}

func TestOutputSizeRejectsMalformed(t *testing.T) {
	for _, delta := range []string{"", "2", "2 ", "nonsense"} {
		if _, err := OutputSize([]byte(delta)); !errors.Is(err, ErrInvalidDelta) {
			t.Fatalf("OutputSize(%q) = %v, want ErrInvalidDelta", delta, err)
		}
	}
}

// OutputSize is only useful as a pre-filter if Apply can never exceed what it
// reported.
func TestOutputSizeBoundsApply(t *testing.T) {
	rnd := rand.New(rand.NewPCG(61, 67))
	for range 300 {
		origin := randomBytes(rnd, rnd.IntN(2048))
		delta := randomBytes(rnd, rnd.IntN(64))
		declared, err := OutputSize(delta)
		out, applyErr := Apply(origin, delta)
		if applyErr != nil {
			continue
		}
		if err != nil {
			t.Fatalf("Apply accepted a delta OutputSize rejected: %q", delta)
		}
		if len(out) != declared {
			t.Fatalf("Apply produced %d bytes, OutputSize declared %d", len(out), declared)
		}
	}
}

func TestApplyAllocations(t *testing.T) {
	origin := []byte(jsonDoc(16<<10, 1))
	target := []byte(jsonDoc(16<<10, 2))
	delta := Create(origin, target)

	got := testing.AllocsPerRun(100, func() {
		if _, err := Apply(origin, delta); err != nil {
			t.Fatal(err)
		}
	})
	// One allocation, for the output, sized exactly by the validation pass.
	if got > 1 {
		t.Fatalf("Apply made %v allocations per call, want at most 1", got)
	}
}

// Rejecting a delta must not allocate at all: a peer sending bad deltas in a
// loop should cost nothing to refuse.
func TestApplyRejectionAllocations(t *testing.T) {
	origin := []byte(jsonDoc(16<<10, 1))
	// Declares a 4 GiB output. A implementation that trusts the header before
	// validating would allocate that.
	bad := []byte("3~~~~~\n2@0,")

	got := testing.AllocsPerRun(100, func() {
		if _, err := Apply(origin, bad); err == nil {
			t.Fatal("expected an error")
		}
	})
	if got != 0 {
		t.Fatalf("rejecting a delta made %v allocations per call, want 0", got)
	}
}

// Apply holds no state at all, unlike Create, but that is a property worth
// pinning rather than assuming: a future optimisation reaching for a pool
// here would have to keep it true.
func TestApplyConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	type job struct{ origin, delta, want []byte }
	var jobs []job
	for _, tc := range corpus() {
		jobs = append(jobs, job{tc.origin, Create(tc.origin, tc.target), tc.target})
	}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 0, 64)
			for range 25 {
				for _, j := range jobs {
					got, err := Apply(j.origin, j.delta)
					if err != nil || !bytes.Equal(got, j.want) {
						t.Errorf("concurrent Apply: err=%v, match=%v", err, bytes.Equal(got, j.want))
						return
					}
					// And the appending form, which shares the same validation.
					buf, err = AppendApply(buf[:0], j.origin, j.delta)
					if err != nil || !bytes.Equal(buf, j.want) {
						t.Errorf("concurrent AppendApply: err=%v, match=%v", err, bytes.Equal(buf, j.want))
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
