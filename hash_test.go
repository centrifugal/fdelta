package fdelta

import (
	"math"
	"math/rand/v2"
	"testing"
)

// blockHash sums its four groups independently rather than as one running
// total. This checks it against the definition the format gives: a is the sum
// of the window's bytes, b is that sum weighted by distance from the end.
func TestBlockHashMatchesSpecification(t *testing.T) {
	spec := func(w []byte) uint32 {
		var a, b uint16
		for i := range hashWindow {
			a += uint16(w[i])
			b += uint16(hashWindow-i) * uint16(w[i])
		}
		return uint32(a) | uint32(b)<<16
	}

	rnd := rand.New(rand.NewPCG(107, 109))
	buffers := [][]byte{
		make([]byte, hashWindow*4),
		randomBytes(rnd, hashWindow*64),
	}
	allHigh := make([]byte, hashWindow*4)
	for i := range allHigh {
		allHigh[i] = 0xff
	}
	buffers = append(buffers, allHigh)
	for range 200 {
		buffers = append(buffers, randomBytes(rnd, hashWindow*(1+rnd.IntN(8))))
	}

	for _, b := range buffers {
		for pos := 0; pos+hashWindow <= len(b); pos++ {
			if got, want := blockHash(b, pos), spec(b[pos:]); got != want {
				t.Fatalf("blockHash at %d = %#08x, want %#08x", pos, got, want)
			}
		}
	}
}

// The regrouping in blockHash is sound only if a and b really stay below 2^16.
func TestBlockHashHalvesCannotOverflow(t *testing.T) {
	const maxA = hashWindow * 255
	const maxB = hashWindow * (hashWindow + 1) / 2 * 255
	if maxA >= 1<<16 {
		t.Fatalf("the low half can reach %d, which overflows 16 bits", maxA)
	}
	if maxB >= 1<<16 {
		t.Fatalf("the high half can reach %d, which overflows 16 bits", maxB)
	}
}

// Advancing the window one byte at a time must match hashing that window
// directly, or the encoder would look in the wrong bucket.
func TestRollingHashMatchesBlockHash(t *testing.T) {
	rnd := rand.New(rand.NewPCG(113, 127))
	for range 50 {
		b := randomBytes(rnd, 512)
		var h rollingHash
		h.init(b, 0)
		for pos := 0; pos+hashWindow < len(b); pos++ {
			if got, want := h.value(), blockHash(b, pos); got != want {
				t.Fatalf("rolling hash at %d = %#08x, want %#08x", pos, got, want)
			}
			h.next(b[pos+hashWindow])
		}
	}
}

// bucketIndex must stay inside the table it is given, for every hash and every
// table size the encoder can produce.
func TestBucketIndexInRange(t *testing.T) {
	rnd := rand.New(rand.NewPCG(139, 149))
	for bits := range 21 {
		shift := uint(32 - bits)
		size := uint32(1) << bits
		check := func(h uint32) {
			if got := bucketIndex(h, shift); got >= size {
				t.Fatalf("bucketIndex(%#08x, %d) = %d, table has %d buckets", h, shift, got, size)
			}
		}
		check(0)
		check(math.MaxUint32)
		for range 2000 {
			check(rnd.Uint32())
		}
	}
}

// A single bucket is the degenerate case, where the shift is the full width.
func TestBucketIndexSingleBucket(t *testing.T) {
	for _, h := range []uint32{0, 1, math.MaxUint32, 0x9E3779B1} {
		if got := bucketIndex(h, 32); got != 0 {
			t.Fatalf("bucketIndex(%#08x, 32) = %d, want 0", h, got)
		}
	}
}

// The reason bucketIndex exists: the format's hash is concentrated, so
// reducing it with a remainder leaves most buckets empty and the chains
// through the rest long. That is an algorithmic denial of service on
// high-entropy payloads, and it is worst at exactly the power-of-two sizes a
// caller is most likely to hit. This pins the distribution so the collapse
// cannot come back unnoticed.
func TestBucketDistributionOnHighEntropyInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(163, 167))
	for _, n := range []int{64 << 10, 256 << 10, 1 << 20} {
		origin := randomBytes(rnd, n)
		nBlocks := n / hashWindow
		shift := uint(32 - bitsLen32(uint32(nBlocks-1)))
		size := 1 << (32 - shift)

		counts := make([]int, size)
		longest := 0
		for i := 0; i+hashWindow < n; i += hashWindow {
			hv := bucketIndex(blockHash(origin, i), shift)
			counts[hv]++
			longest = max(longest, counts[hv])
		}
		used := 0
		for _, c := range counts {
			if c > 0 {
				used++
			}
		}
		occupancy := 100 * float64(used) / float64(size)

		// A remainder gives about 2.8% occupancy and chains over a hundred
		// long at a mebibyte. Anything near that is the collapse returning.
		if occupancy < 50 {
			t.Errorf("%d byte payload: only %.1f%% of %d buckets used", n, occupancy, size)
		}
		if longest > 12 {
			t.Errorf("%d byte payload: longest chain is %d", n, longest)
		}
		t.Logf("%7d bytes: %6d buckets, %.1f%% used, longest chain %d", n, size, occupancy, longest)
	}
}

func TestCommonPrefixLen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(151, 157))
	for range 5000 {
		n := rnd.IntN(64)
		a := randomBytes(rnd, n)
		b := append([]byte(nil), a...)
		if len(b) > 0 && rnd.IntN(2) == 0 {
			at := rnd.IntN(len(b))
			b[at] ^= 1 << rnd.IntN(8)
		}
		b = b[:rnd.IntN(len(b)+1)]

		want := 0
		for want < len(a) && want < len(b) && a[want] == b[want] {
			want++
		}
		if got := commonPrefixLen(a, b); got != want {
			t.Fatalf("commonPrefixLen(%q, %q) = %d, want %d", a, b, got, want)
		}
	}
}

func TestCommonSuffixLen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(163, 167))
	for range 5000 {
		a := randomBytes(rnd, 1+rnd.IntN(64))
		b := append([]byte(nil), a...)
		if rnd.IntN(2) == 0 {
			at := rnd.IntN(len(b))
			b[at] ^= 1 << rnd.IntN(8)
		}
		n := rnd.IntN(len(a) + 1)

		want := 0
		for want < n && a[len(a)-want-1] == b[len(b)-want-1] {
			want++
		}
		if got := commonSuffixLen(a, len(a), b, len(b), n); got != want {
			t.Fatalf("commonSuffixLen(%q, %q, n=%d) = %d, want %d", a, b, n, got, want)
		}
	}
}

// bitsLen32 mirrors what Create uses to size its table, kept here so the
// distribution test computes the same shift the encoder does.
func bitsLen32(v uint32) int {
	n := 0
	for ; v > 0; v >>= 1 {
		n++
	}
	return n
}

// Multiply-shift hashing needs an odd multiplier: an even one throws away low
// bits of the product and stops the map being injective on the bits the shift
// keeps. A poorly distributed multiplier is caught by the distribution test
// above, but an even one close to a good value is not, so it is asserted
// directly. The exact value is arbitrary and not observable -- an alternate
// odd constant moves thousands of blocks between buckets and still produces
// identical deltas -- but these two properties are not.
func TestBucketMultiplierIsSuitable(t *testing.T) {
	if phi32%2 == 0 {
		t.Fatalf("the multiplier %#x is even", uint32(phi32))
	}
	// It must also occupy the full width, or the high bits the shift keeps are
	// not influenced by the whole input.
	if phi32>>31 == 0 {
		t.Fatalf("the multiplier %#x does not set its top bit", uint32(phi32))
	}
}
