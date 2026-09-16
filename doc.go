// Package fdelta implements the Fossil delta encoding: a compact binary diff
// between two byte slices.
//
// Instead of storing or transmitting the whole of something, store or transmit
// only what changed relative to a version the other side already has. [Create]
// builds the diff and [Apply] reconstructs the new bytes from the old ones
// plus the diff. A delta is self-describing and carries a checksum of the
// result, so [Apply] reports an error rather than returning wrong data when
// the delta is corrupt or was built against different base bytes.
//
// The format is specified at
// https://fossil-scm.org/home/doc/tip/www/delta_format.wiki and deltas are
// portable between every implementation of it.
//
//	patch := fdelta.Create(oldData, newData)
//	// ... send patch ...
//	newData, err := fdelta.Apply(oldData, patch)
//
// Create always succeeds and always returns a valid delta, but a delta is not
// always worth sending: for unrelated payloads it is larger than the new
// payload itself. Callers are expected to compare and fall back to sending the
// payload in full.
//
// # Relationship to Fossil
//
// The format comes from Fossil, whose src/delta.c is the reference
// implementation. See
// https://fossil-scm.org/home/doc/tip/www/delta_format.wiki for the format and
// https://fossil-scm.org/home/doc/tip/www/delta_encoder_algorithm.wiki for the
// encoder.
//
// This package follows it in everything a delta expresses: the command stream,
// the integer encoding, the checksum, and what Apply accepts. Deltas are
// portable in both directions between every implementation of the format.
//
// Create does not emit the byte-for-byte delta the reference would choose. It
// reduces a block hash to a bucket by multiplying and taking the high bits
// rather than by a remainder, so that its index does not collapse on
// high-entropy payloads; see bucketIndex. That hash is the encoder's private
// index and never appears in a delta, so the result is an ordinary Fossil
// delta of the same size: measured across structured, textual and binary
// payloads the totals agree with the reference to within a fraction of a
// percent.
//
// # Payload size and untrusted input
//
// [Apply] is hardened against hostile deltas: it validates one completely
// before allocating, never panics, and reports [ErrChecksumMismatch] rather
// than returning data built from the wrong source. Two things are worth
// knowing beyond that.
//
// First, a delta is small but its output need not be. A copy command costs
// four bytes and can copy the whole of origin, so a short delta can
// legitimately declare an output far larger than either input. That is
// inherent to the format. Use [OutputSize] to impose your own ceiling before
// calling Apply; Apply is guaranteed not to exceed what OutputSize reported.
//
// Second, [Create]'s cost rises with the size of origin, and more steeply on
// payloads with no structure to find. It is close to linear -- a mebibyte of
// random bytes costs roughly 18ms on a current machine, against roughly 0.3ms
// for 64 kibibytes -- and, unlike the algorithm as delta.c implements it, does
// not depend on the payload happening to be a power of two in size. See
// bucketIndex for what that took and why the reference does not do it.
//
// Delta encoding high-entropy data is pointless regardless, since the delta
// comes out larger than the payload and the caller discards it. If a remote
// peer can influence what you diff, decide a maximum payload size and send
// anything larger in full without calling Create at all: that bounds the cost
// exactly, where relying on the encoder's own speed only bounds it roughly.
//
// # Concurrency
//
// All functions are safe for concurrent use and hold no state between calls
// beyond an internal pool of scratch buffers.
package fdelta
