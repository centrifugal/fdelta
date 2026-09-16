package fdelta

import "encoding/binary"

// checksum is the value a delta carries in its trailing command: the input
// read as big-endian uint32 words, zero padded to a multiple of four bytes,
// summed modulo 2^32.
//
// delta.c spells it exactly this way wherever a byte swap is available. Its
// portable fallback instead keeps one running sum per byte lane and shifts
// them into place at the very end, which produces the same value because the
// shifts distribute over addition modulo 2^32.
func checksum(b []byte) uint32 {
	var sum uint32
	n := len(b)
	i := 0
	// Four words per iteration: the sum is a single dependency chain, so the
	// loop is unrolled to keep more than one load in flight.
	for ; i+16 <= n; i += 16 {
		// An array pointer rather than a slice, so the four reads are known to
		// be in range without a bounds check on each.
		w := (*[16]byte)(b[i : i+16])
		sum += binary.BigEndian.Uint32(w[0:4]) + binary.BigEndian.Uint32(w[4:8]) +
			binary.BigEndian.Uint32(w[8:12]) + binary.BigEndian.Uint32(w[12:16])
	}
	for ; i+4 <= n; i += 4 {
		sum += binary.BigEndian.Uint32(b[i : i+4 : i+4])
	}
	switch n - i {
	case 3:
		sum += uint32(b[i])<<24 | uint32(b[i+1])<<16 | uint32(b[i+2])<<8
	case 2:
		sum += uint32(b[i])<<24 | uint32(b[i+1])<<16
	case 1:
		sum += uint32(b[i]) << 24
	}
	return sum
}
