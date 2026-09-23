package fdelta

import "slices"

// maxInt is the largest value an int can hold on this platform. A delta
// declares its output size as an unsigned 32-bit number, which does not fit in
// an int on a 32-bit platform.
const maxInt = int(^uint(0) >> 1)

// Apply returns the bytes that delta produces from origin.
//
// It returns [ErrChecksumMismatch] when the delta is well formed but the output
// does not match the checksum it carries, which almost always means origin is
// not the source it was built from, and an error wrapping [ErrInvalidDelta]
// when the delta itself is malformed. It never returns partial output and
// never panics, whatever the delta contains.
//
// The result does not alias origin or delta.
//
// # Applying deltas from an untrusted peer
//
// A malformed delta is rejected without allocating anything. A well formed one
// is a different matter: a copy command costs a handful of bytes and can copy
// the whole of origin, so a delta that is itself tiny can legitimately demand
// an enormous output, and Apply will produce it. Measured on a 1 MiB origin, a
// 3.5 KB delta can ask for 500 MB, and the format's own ceiling is 4 GiB per
// call, because the declared size is a 32-bit number.
//
// Call [OutputSize] and impose your own ceiling before calling Apply. It reads
// the header alone, costs no allocation, and Apply is guaranteed not to
// produce more than it reported:
//
//	if n, err := fdelta.OutputSize(delta); err != nil || n > maxPayload {
//		// refuse without doing any further work
//	}
//
// Note also what the checksum is and is not. It binds the output to the claim
// the delta makes about it, so it catches corruption and most cases of a wrong
// origin. It is a plain sum of 32-bit words, fixed by the format, so it cannot
// catch them all: an origin that differs from the right one in a way that
// leaves the sum unchanged, such as two values swapped four bytes apart,
// applies without error to the wrong output. It is also not authentication: whoever supplies the delta chooses the output, and can
// supply a correct checksum for whatever they chose. If the deltas you apply
// need to be trusted, that has to come from somewhere else.
func Apply(origin, delta []byte) ([]byte, error) {
	size, sum, body, err := planApply(len(origin), delta)
	if err != nil {
		return nil, err
	}
	out := make([]byte, size)
	execApply(out, origin, delta, body)
	if checksum(out) != sum {
		return nil, ErrChecksumMismatch
	}
	return out, nil
}

// AppendApply appends the bytes that delta produces from origin to dst and
// returns the extended slice, in the manner of [append]. It lets a caller that
// applies many deltas reuse one buffer.
//
// Reuse is worth real time here, unlike in [AppendCreate]: the output is as
// large as the payload, and once the copying is this fast, allocating and
// zeroing that buffer is most of the work. Applying a 16 KiB payload takes
// roughly a third as long with a buffer that is already big enough.
//
// On any error dst is returned unchanged and dst[:len(dst)] is untouched. A
// malformed delta is rejected before a single byte is written; a checksum
// mismatch is only discovered after the output has been produced, so by then
// the spare capacity beyond len(dst) may have been written to.
//
// dst, including its spare capacity, may not overlap origin or delta.
func AppendApply(dst, origin, delta []byte) ([]byte, error) {
	size, sum, body, err := planApply(len(origin), delta)
	if err != nil {
		return dst, err
	}
	start := len(dst)
	// Only reachable where int is 32 bits: without it, a large enough dst and
	// output would overflow the length slices.Grow is asked for, and it panics.
	if size > maxInt-start {
		return dst, errOutputTooLarge
	}
	out := slices.Grow(dst, size)[:start+size]
	execApply(out[start:], origin, delta, body)
	if checksum(out[start:]) != sum {
		return dst, ErrChecksumMismatch
	}
	return out, nil
}

// OutputSize returns the size of the output that delta declares in its header,
// without examining the rest of it.
//
// This is a cheap pre-filter, not a validation: it reports what the delta
// claims. [Apply] independently verifies that the commands really do produce
// that many bytes, and fails otherwise, so a caller that rejects an oversized
// delta here can rely on Apply not exceeding what this returned.
//
// It is worth checking. A delta is small but its output need not be: a copy
// command costs a handful of bytes and can copy the whole of origin, so a short delta
// can legitimately ask for an output far larger than either input. That is
// inherent to the format and true of every implementation of it; a caller
// taking deltas from an untrusted peer should decide its own ceiling here.
func OutputSize(delta []byte) (int, error) {
	size, i := decodeInt(delta, 0)
	if i >= len(delta) || delta[i] != '\n' {
		return 0, errSizeTerminator
	}
	if uint64(size) > uint64(maxInt) {
		return 0, errOutputTooLarge
	}
	return int(size), nil
}

// planApply walks the whole command stream without producing any output,
// checking every count against both the source and the delta. It returns the
// output size, the checksum the delta carries, and the index at which the
// commands begin.
//
// Validating up front is what lets [Apply] allocate exactly once, at exactly
// the right size, and only for a delta that has already been shown to produce
// it. delta.c instead writes as it parses, so it can do arbitrary work and
// hold arbitrary memory for a delta that turns out to be malformed a byte
// later.
func planApply(lenSrc int, delta []byte) (size int, sum uint32, body int, err error) {
	limit, i := decodeInt(delta, 0)
	if i >= len(delta) || delta[i] != '\n' {
		return 0, 0, 0, errSizeTerminator
	}
	if uint64(limit) > uint64(maxInt) {
		return 0, 0, 0, errOutputTooLarge
	}
	i++
	body = i

	// total is 64-bit, as it is in delta.c, so that it cannot wrap past the
	// comparisons below. Narrowing it to 32 bits, as the counts themselves
	// are, would let a long enough run of commands wrap past them.
	var total uint64
	for i < len(delta) {
		cnt, j := decodeInt(delta, i)
		if j >= len(delta) {
			break
		}
		op := delta[j]
		i = j + 1

		switch op {
		case '@':
			ofst, k := decodeInt(delta, i)
			if k < len(delta) && delta[k] != ',' {
				return 0, 0, 0, errCopyTerminator
			}
			// May land one past the end, which ends the loop below as an
			// unterminated delta, exactly as delta.c does.
			i = k + 1

			total += uint64(cnt)
			if total > uint64(limit) {
				return 0, 0, 0, errCopyExceedsSize
			}
			// #nosec G115 -- lenSrc is a len() result and cannot be negative
			if uint64(ofst)+uint64(cnt) > uint64(lenSrc) {
				return 0, 0, 0, errCopyPastSource
			}

		case ':':
			total += uint64(cnt)
			if total > uint64(limit) {
				return 0, 0, 0, errInsertExceeds
			}
			// The count is against the bytes still left in the delta, not its
			// full length. Reading it as the full length is what lets an
			// insert run off the end.
			// #nosec G115 -- i is an index and len() cannot be negative
			if uint64(i)+uint64(cnt) > uint64(len(delta)) {
				return 0, 0, 0, errInsertPastDelta
			}
			i += int(cnt)

		case ';':
			if total != uint64(limit) {
				return 0, 0, 0, errSizeMismatch
			}
			return int(limit), cnt, body, nil

		default:
			return 0, 0, 0, errUnknownOperator
		}
	}
	return 0, 0, 0, errUnterminated
}

// execApply replays the commands into out, which planApply has already shown
// to be exactly the right length for them. Every count has been checked, so
// this pass only copies.
func execApply(out, origin, delta []byte, i int) {
	n := 0
	for {
		cnt, j := decodeInt(delta, i)
		op := delta[j]
		i = j + 1

		switch op {
		case '@':
			ofst, k := decodeInt(delta, i)
			i = k + 1
			at := int(ofst)
			n += copy(out[n:], origin[at:at+int(cnt)])
		case ':':
			n += copy(out[n:], delta[i:i+int(cnt)])
			i += int(cnt)
		default: // ';', the only way planApply returns without an error.
			return
		}
	}
}
