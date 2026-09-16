package fdelta

import (
	"encoding/binary"
	"math/bits"
	"sync"
)

// initialDeltaCap is the capacity Create starts its buffer at. A delta worth
// sending is small; one that is not grows by append like any other slice.
const initialDeltaCap = 128

// u32 narrows a length or offset to the width the format encodes them in.
//
// Every value passed here is a length or an index into one of the inputs, so
// it is never negative. It can only overflow for an input of 4 GiB or more,
// which the format cannot represent at all: see the note on limits in
// [Create]. Such an input yields a delta that [Apply] rejects, rather than one
// that applies to the wrong bytes.
func u32(v int) uint32 {
	return uint32(v) // #nosec G115 -- bounded by input length; see the doc comment
}

// Create returns a delta that turns origin into target.
//
// It never fails and the result is always a valid delta, but it is not always
// a useful one: when the two payloads have little in common the delta is
// larger than target itself. Callers that are deciding whether to send a delta
// or the payload in full should compare len(delta) against len(target) and use
// whichever is smaller.
//
// The result does not alias origin or target.
//
// # Limits
//
// The format encodes every length and offset as an unsigned 32-bit number, so
// neither input may exceed 4 GiB-1. Create does not check this: on a larger
// input the lengths wrap and the delta it returns will be rejected by Apply
// rather than silently applying wrongly, but callers with inputs anywhere near
// that size should not be using this format.
//
// Cost grows with the size of origin and with how little structure it has; see
// the package documentation on payload size before feeding it large,
// high-entropy input from an untrusted source.
//
// Create does not produce the same bytes delta.c would, because it indexes the
// source differently; see bucketIndex. It produces a delta of the same size,
// and every implementation of the format reads it.
func Create(origin, target []byte) []byte {
	return AppendCreate(make([]byte, 0, initialDeltaCap), origin, target)
}

// AppendCreate appends the delta that turns origin into target to dst and
// returns the extended slice, in the manner of [append]. It lets a caller that
// creates many deltas reuse one buffer.
//
// What this saves is the allocation, not time: a delta worth sending is small,
// so [Create]'s own allocation barely registers next to the search. Reach for
// it to keep a hot publication path allocation-free, not to make it faster.
// [AppendApply] is the one where reuse changes the timing.
//
// dst may not overlap origin or target.
func AppendCreate(dst, origin, target []byte) []byte {
	lenOut := len(target)
	lenSrc := len(origin)

	dst = appendInt(dst, u32(lenOut), '\n')

	// With a source this short no copy command can ever pay for itself, so the
	// whole target goes out as one literal.
	if lenSrc <= hashWindow {
		dst = appendInt(dst, u32(lenOut), ':')
		dst = append(dst, target...)
		return appendInt(dst, checksum(target), ';')
	}

	// Index the source: hash every hashWindow-sized block, remembering the
	// most recent block for each hash in landmark and chaining any earlier
	// ones through collide.
	//
	// The table is rounded up to a power of two so that a hash can be reduced
	// to a bucket by multiplying and shifting rather than by a remainder; see
	// bucketIndex for why that matters. It holds at least one slot per block,
	// so at most twice as many as delta.c's table.
	nBlocks := lenSrc / hashWindow
	// bits.Len32 returns between 0 and 32, so the shift is in the same range
	// and never negative.
	shift := uint(32 - bits.Len32(u32(nBlocks-1))) // #nosec G115 -- 0 <= shift <= 32
	s := getScratch(1 << (32 - shift))
	collide := s.collide
	landmark := s.landmark

	for i := 0; i < lenSrc-hashWindow; i += hashWindow {
		hv := bucketIndex(blockHash(origin, i), shift)
		// i is below lenSrc, so the block number is below lenSrc/16. Overflowing
		// an int32 would need a source of 32 GiB, and the format cannot encode
		// past 4 GiB at all; see the note on limits in Create.
		blk := int32(i / hashWindow) // #nosec G115 -- bounded by the source length
		// Block numbers are stored biased by one so that a zeroed table means
		// "nothing here" and resetting it for reuse is a single clear.
		collide[blk] = landmark[hv]
		landmark[hv] = blk + 1
	}

	// Scan the target with a sliding window, emitting a copy command whenever
	// the window's hash leads to a source block that really does match, and
	// literal text for everything in between.
	var h rollingHash
	var base int
	for base+hashWindow < lenOut {
		bestOfst := 0
		bestLitsz := 0
		bestCnt := 0
		h.init(target, base)
		i := 0
		for {
			hv := bucketIndex(h.value(), shift)
			next := landmark[hv]
			// Walking a very long chain costs more than the better match at
			// the end of it is worth, so give up after this many candidates.
			// delta.c examines exactly this many: note that the limit is
			// tested before the decrement, so the body runs 250 times.
			for limit := 250; next != 0 && limit > 0; limit-- {
				iBlock := int(next) - 1
				iSrc := iBlock * hashWindow

				// How far the match runs forward from the block start, less
				// one: the byte at iSrc itself is counted by the +1 in cnt
				// below. Most candidates are hash collisions that differ in
				// their very first byte, so that byte is tested here rather
				// than in a call.
				j := -1
				if origin[iSrc] == target[base+i] {
					j = commonPrefixLen(origin[iSrc:], target[base+i:]) - 1
				}

				// How far it runs backward, at most as far as either side
				// reaches: a match usually starts before the hashed window.
				k := 0
				if maxK := min(iSrc-1, i); maxK > 0 && origin[iSrc-1] == target[base+i-1] {
					k = commonSuffixLen(origin, iSrc, target, base+i, maxK)
				}

				ofst := iSrc - k
				cnt := j + k + 1
				litsz := i - k

				// Take the candidate only if it beats the best so far and is
				// long enough to be worth a copy command at all. delta.c
				// prices the command first and compares after; the two tests
				// are independent, so doing the cheap one first skips three
				// digitCount calls for every candidate that cannot win, which
				// is nearly all of them.
				if cnt > bestCnt && cnt >= digitCount(litsz)+digitCount(cnt)+digitCount(ofst)+3 {
					bestCnt = cnt
					bestOfst = ofst
					bestLitsz = litsz
				}

				next = collide[iBlock]
			}

			if bestCnt > 0 {
				if bestLitsz > 0 {
					dst = appendInt(dst, u32(bestLitsz), ':')
					dst = append(dst, target[base:base+bestLitsz]...)
					base += bestLitsz
				}
				base += bestCnt
				dst = appendInt(dst, u32(bestCnt), '@')
				dst = appendInt(dst, u32(bestOfst), ',')
				break
			}

			// The window reached the end of the target without matching
			// anything: the remainder is all literal.
			if base+i+hashWindow >= lenOut {
				dst = appendInt(dst, u32(lenOut-base), ':')
				dst = append(dst, target[base:]...)
				base = lenOut
				break
			}

			h.next(target[base+i+hashWindow])
			i++
		}
	}

	putScratch(s)

	// The last hashWindow bytes are never scanned, so they are literal too.
	if base < lenOut {
		dst = appendInt(dst, u32(lenOut-base), ':')
		dst = append(dst, target[base:]...)
	}
	return appendInt(dst, checksum(target), ';')
}

// commonPrefixLen returns how many leading bytes a and b share.
func commonPrefixLen(a, b []byte) int {
	n := min(len(a), len(b))
	i := 0
	for ; i+8 <= n; i += 8 {
		// The decoder fixes how bytes map to bit positions, so which byte the
		// first difference falls in does not depend on the machine's own byte
		// order.
		x := binary.LittleEndian.Uint64(a[i:])
		y := binary.LittleEndian.Uint64(b[i:])
		if x != y {
			return i + bits.TrailingZeros64(x^y)/8
		}
	}
	for ; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// commonSuffixLen returns how many of the n bytes ending at a[ai] and b[bi],
// both exclusive, match, walking backwards.
//
// It takes indices rather than slices because re-slicing both inputs for every
// candidate costs more than the comparison itself does when the candidate
// fails immediately, which is the common case.
func commonSuffixLen(a []byte, ai int, b []byte, bi, n int) int {
	i := 0
	for ; i+8 <= n; i += 8 {
		x := binary.LittleEndian.Uint64(a[ai-i-8:])
		y := binary.LittleEndian.Uint64(b[bi-i-8:])
		if x != y {
			return i + bits.LeadingZeros64(x^y)/8
		}
	}
	for ; i < n; i++ {
		if a[ai-i-1] != b[bi-i-1] {
			return i
		}
	}
	return n
}

// scratch holds the tables that index the source. They are the bulk of what
// Create would otherwise allocate, and it neither keeps nor hands out any
// reference to them, so they can be pooled.
type scratch struct {
	// collide[b] is the previous block with the same hash as block b, biased
	// by one. It never needs clearing: every entry reachable along a chain was
	// written by the same call that reads it.
	collide []int32
	// landmark[h] is the most recent block hashing to h, biased by one, so
	// that zero means empty and resetting is a single clear.
	landmark []int32
}

// maxPooledHash caps the table size kept in the pool, so that one unusually
// large payload does not pin a large buffer per P until the next collection.
// It corresponds to a source of 1 MiB.
const maxPooledHash = 1 << 16

// getScratch returns tables of at least n entries, where n is the bucket count
// and is never below the block count, so both tables are long enough for
// either index.

var scratchPool sync.Pool

func getScratch(n int) *scratch {
	if s, _ := scratchPool.Get().(*scratch); s != nil {
		if cap(s.collide) >= n {
			s.collide = s.collide[:n]
			s.landmark = s.landmark[:n]
			clear(s.landmark)
			return s
		}
		if n <= maxPooledHash {
			s.collide = make([]int32, n)
			s.landmark = make([]int32, n)
			return s
		}
	}
	return &scratch{
		collide:  make([]int32, n),
		landmark: make([]int32, n),
	}
}

func putScratch(s *scratch) {
	if cap(s.collide) > maxPooledHash {
		return
	}
	scratchPool.Put(s)
}
