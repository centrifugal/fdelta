package fdelta

import (
	"fmt"
	"testing"
)

// BenchmarkCreateAdversarial measures high-entropy payloads, where the encoder
// finds nothing to copy and the block index does all the work.
//
// This is where the format's own hash collapses if it is reduced to a bucket
// with a remainder, as delta.c does: the cost there rises superlinearly and
// jumps whenever the payload size is a power of two. bucketIndex removes that,
// and the pow2/plus16 pairs below are what shows it -- the two should stay
// within the same order, and ns/byte should stay roughly flat across sizes.
// TestBucketDistributionOnHighEntropyInput asserts the underlying property;
// this keeps the cost itself visible.
//
// Run it with:
//
//	go test -bench BenchmarkCreateAdversarial -benchtime 1x
func BenchmarkCreateAdversarial(b *testing.B) {
	for _, n := range []int{16 << 10, 64 << 10, 256 << 10, 1 << 20} {
		// A power-of-two size makes the hash's high half cancel out of the
		// bucket calculation, which is the worst case.
		b.Run(fmt.Sprintf("random-%dkb-pow2", n>>10), func(b *testing.B) {
			origin, target := adversarialPair(n)
			b.SetBytes(int64(n))
			b.ReportAllocs()
			for b.Loop() {
				sink = Create(origin, target)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/byte")
		})
		// Sixteen bytes more, so the bucket count is odd and the high half
		// contributes. Same work, far less of it.
		b.Run(fmt.Sprintf("random-%dkb-plus16", n>>10), func(b *testing.B) {
			origin, target := adversarialPair(n + 16)
			b.SetBytes(int64(n + 16))
			b.ReportAllocs()
			for b.Loop() {
				sink = Create(origin, target)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n+16), "ns/byte")
		})
	}
}

func adversarialPair(n int) (origin, target []byte) {
	// Deterministic, so the benchmark is comparable between runs. A simple
	// linear congruential sequence is enough: what matters is that the bytes
	// have no structure the encoder can index.
	gen := func(seed uint64) []byte {
		b := make([]byte, n)
		x := seed
		for i := range b {
			x = x*6364136223846793005 + 1442695040888963407
			b[i] = byte(x >> 33)
		}
		return b
	}
	return gen(1), gen(2)
}
