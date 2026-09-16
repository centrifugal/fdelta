package fdelta

import "errors"

var (
	// ErrInvalidDelta reports that a delta is not well formed: a truncated or
	// corrupt command stream, a count that runs past the end of the delta or
	// of the source, or a size that disagrees with the commands. Every error
	// returned by [Apply] and [OutputSize] other than [ErrChecksumMismatch]
	// wraps it, so errors.Is(err, ErrInvalidDelta) matches them all.
	ErrInvalidDelta = errors.New("fdelta: invalid delta")

	// ErrChecksumMismatch reports that a delta is well formed but the bytes it
	// produced do not match the checksum it carries. In practice this means it
	// was applied to the wrong source: a client whose base payload has drifted
	// from the one the delta was built against sees this rather than silently
	// wrong data, and should ask for the payload in full.
	ErrChecksumMismatch = errors.New("fdelta: checksum mismatch")
)

// deltaError is a static description of a malformed delta. Keeping these as
// package-level values means no formatting or allocation on a rejection, which
// matters when a peer can send bad deltas as fast as it likes.
type deltaError struct{ detail string }

func (e *deltaError) Error() string { return ErrInvalidDelta.Error() + ": " + e.detail }
func (e *deltaError) Unwrap() error { return ErrInvalidDelta }

var (
	errSizeTerminator  = &deltaError{"size integer not terminated by a newline"}
	errCopyTerminator  = &deltaError{"copy command not terminated by a comma"}
	errCopyExceedsSize = &deltaError{"copy exceeds the declared output size"}
	errCopyPastSource  = &deltaError{"copy extends past the end of the source"}
	errInsertExceeds   = &deltaError{"insert exceeds the declared output size"}
	errInsertPastDelta = &deltaError{"insert extends past the end of the delta"}
	errSizeMismatch    = &deltaError{"output size does not match the declared size"}
	errUnknownOperator = &deltaError{"unknown delta operator"}
	errUnterminated    = &deltaError{"unterminated delta"}
	errOutputTooLarge  = &deltaError{"declared output size exceeds the maximum representable length"}
)
