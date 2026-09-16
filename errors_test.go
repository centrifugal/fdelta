package fdelta

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// Every rejection a caller can see must carry a message that says what was
// wrong, wrap ErrInvalidDelta so errors.Is finds it, and be distinct from the
// others. Nothing else in the suite looks at the text, so without this a
// message could be empty, duplicated or panicking and every test would pass.
func TestDeltaErrorMessages(t *testing.T) {
	all := []*deltaError{
		errSizeTerminator, errCopyTerminator, errCopyExceedsSize, errCopyPastSource,
		errInsertExceeds, errInsertPastDelta, errSizeMismatch, errUnknownOperator,
		errUnterminated, errOutputTooLarge,
	}

	seen := make(map[string]bool, len(all))
	for _, e := range all {
		msg := e.Error()
		if !strings.HasPrefix(msg, "fdelta: invalid delta: ") {
			t.Errorf("message %q is not prefixed with the sentinel's own text", msg)
		}
		detail := strings.TrimPrefix(msg, "fdelta: invalid delta: ")
		if detail == "" {
			t.Errorf("message %q has no detail", msg)
		}
		if strings.HasSuffix(detail, ".") || strings.ToLower(detail[:1]) != detail[:1] {
			t.Errorf("detail %q should read as a lower-case clause with no full stop", detail)
		}
		if seen[msg] {
			t.Errorf("duplicate message %q", msg)
		}
		seen[msg] = true

		if !errors.Is(e, ErrInvalidDelta) {
			t.Errorf("%q does not match ErrInvalidDelta", msg)
		}
		if errors.Is(e, ErrChecksumMismatch) {
			t.Errorf("%q must not match ErrChecksumMismatch", msg)
		}
		if !errors.Is(errors.Join(e, errors.New("context")), ErrInvalidDelta) {
			t.Errorf("%q stops matching once wrapped", msg)
		}
	}
}

// The two sentinels are distinct failures a caller routes differently, so
// neither may match the other.
func TestSentinelsAreDistinct(t *testing.T) {
	if errors.Is(ErrChecksumMismatch, ErrInvalidDelta) {
		t.Error("ErrChecksumMismatch must not match ErrInvalidDelta")
	}
	if errors.Is(ErrInvalidDelta, ErrChecksumMismatch) {
		t.Error("ErrInvalidDelta must not match ErrChecksumMismatch")
	}
	for _, e := range []error{ErrInvalidDelta, ErrChecksumMismatch} {
		if !strings.HasPrefix(e.Error(), "fdelta: ") {
			t.Errorf("%q should be namespaced to the package", e)
		}
	}
}

// A delta may declare an output larger than an int can hold, which is only
// possible to reach where int is 32 bits. The 32-bit CI jobs are what cover
// this branch; here it documents the boundary in both directions.
func TestOutputSizeBeyondInt(t *testing.T) {
	huge := []byte("3~~~~~\n") // declares 0xFFFFFFFF bytes of output

	size, err := OutputSize(huge)
	if maxInt > math.MaxInt32 {
		if err != nil {
			t.Fatalf("on a 64-bit platform this fits in an int: %v", err)
		}
		// Compared through uint64: the constant does not fit in an int on a
		// 32-bit platform, and this file still has to compile there.
		if uint64(size) != uint64(math.MaxUint32) {
			t.Fatalf("OutputSize = %d, want %d", size, uint64(math.MaxUint32))
		}
		return
	}

	if !errors.Is(err, ErrInvalidDelta) {
		t.Fatalf("on a 32-bit platform this must be rejected, got size=%d err=%v", size, err)
	}
	if _, err := Apply([]byte("origin"), huge); !errors.Is(err, ErrInvalidDelta) {
		t.Fatalf("Apply must reject it too, got %v", err)
	}
}
