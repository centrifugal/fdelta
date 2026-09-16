package fdelta

// hashWindow is the number of bytes the encoder hashes at a time, and so the
// shortest run of identical bytes it can find. It must be a power of two:
// rollingHash.next relies on that to wrap its cursor with a mask.
const hashWindow = 16

// blockHash is the hash of the hashWindow bytes at z[pos:].
//
// The hash is two 16-bit halves: a is the sum of the bytes and b is their sum
// weighted by distance from the end of the window, so that b distinguishes
// windows that a alone does not. delta.c computes both with a running total
// per byte, as hash_once().
//
// Written out here as four independent groups, which is the same value: over
// 16 bytes a is at most 16*255 and b at most (16+15+...+1)*255 = 34680, both
// below 2^16, so the 16-bit arithmetic never actually wraps and the additions
// can be regrouped freely. The running form is instead a chain of 32 additions
// that each depend on the one before. Go does not unroll loops, so the groups
// are spelled out.
func blockHash(z []byte, pos int) uint32 {
	w := (*[hashWindow]byte)(z[pos : pos+hashWindow])

	a0 := uint32(w[0]) + uint32(w[1]) + uint32(w[2]) + uint32(w[3])
	a1 := uint32(w[4]) + uint32(w[5]) + uint32(w[6]) + uint32(w[7])
	a2 := uint32(w[8]) + uint32(w[9]) + uint32(w[10]) + uint32(w[11])
	a3 := uint32(w[12]) + uint32(w[13]) + uint32(w[14]) + uint32(w[15])

	b0 := 16*uint32(w[0]) + 15*uint32(w[1]) + 14*uint32(w[2]) + 13*uint32(w[3])
	b1 := 12*uint32(w[4]) + 11*uint32(w[5]) + 10*uint32(w[6]) + 9*uint32(w[7])
	b2 := 8*uint32(w[8]) + 7*uint32(w[9]) + 6*uint32(w[10]) + 5*uint32(w[11])
	b3 := 4*uint32(w[12]) + 3*uint32(w[13]) + 2*uint32(w[14]) + uint32(w[15])

	return (a0 + a1 + a2 + a3) | (b0+b1+b2+b3)<<16
}

// rollingHash is blockHash over a window that can be advanced one byte at a
// time, for the pass that scans the target. The window is a fixed-size array
// so that advancing it costs no allocation and no bounds checks.
//
// Only the target scan needs this; indexing the source hashes disjoint blocks
// and never advances, so it calls blockHash directly. delta.c splits the two
// the same way, as hash_init() and hash_once().
type rollingHash struct {
	a, b uint16
	i    uint16
	z    [hashWindow]byte
}

func (h *rollingHash) init(z []byte, pos int) {
	hv := blockHash(z, pos)
	h.z = *(*[hashWindow]byte)(z[pos : pos+hashWindow])
	// Splitting the packed hash back into its two halves is the point here,
	// so both narrowings are intended.
	h.a = uint16(hv)       // #nosec G115 -- the low half, by definition
	h.b = uint16(hv >> 16) // #nosec G115 -- the high half, by definition
	h.i = 0
}

// next slides the window forward by one byte, dropping the oldest and taking
// c. Here the 16-bit arithmetic really does wrap, and must.
func (h *rollingHash) next(c byte) {
	old := uint16(h.z[h.i])
	h.z[h.i] = c
	h.i = (h.i + 1) & (hashWindow - 1)
	h.a = h.a - old + uint16(c)
	h.b = h.b - hashWindow*old + h.a
}

func (h *rollingHash) value() uint32 {
	return uint32(h.a) | uint32(h.b)<<16
}

// phi32 is 2^32 divided by the golden ratio: odd, with its bits well spread.
// The usual multiplier for multiply-shift hashing.
const phi32 = 0x9E3779B1

// bucketIndex maps a hash to one of 1<<(32-shift) buckets.
//
// It multiplies and takes the high bits rather than taking a remainder, and
// that difference is the whole point. The format's hash is concentrated: its
// low half is a plain byte sum, so over a 16 byte window it never exceeds
// 4080, and for real data it clusters near the middle of even that range. A
// remainder preserves the clustering, so most buckets stay empty however many
// there are and the chains through the rest grow long. Worse, the modulus is
// the block count, which is a power of two whenever the payload is, and then
// the hash's high half cancels out of the remainder entirely and only the byte
// sum survives.
//
// Multiplying by an odd constant and keeping the top bits folds every input
// bit into the result, so the buckets fill evenly whatever the payload size.
// Measured on a mebibyte of high-entropy data this takes Create from roughly
// 290ms to roughly 18ms, and removes the sensitivity to payload size
// completely; on the small, structured payloads the encoder is actually for it
// costs nothing, because one multiply replaces the two a remainder needed.
//
// delta.c takes the remainder, and has the same collapse. This is the one
// place this package knowingly departs from it. Nothing observable changes:
// the hash is the encoder's private index and never appears in a delta, so
// every delta produced here remains a Fossil delta that delta.c and every
// other implementation read exactly as before.
func bucketIndex(h uint32, shift uint) uint32 {
	return (h * phi32) >> shift
}
