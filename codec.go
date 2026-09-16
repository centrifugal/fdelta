package fdelta

import "math/bits"

// The format writes every number in a base-64 alphabet of its own, unrelated
// to RFC 4648: the digits ascend by ASCII value so that the encoding sorts the
// same way the numbers do.
const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz~"

// maxIntDigits is how many digits a uint32 can take: 32 bits at 6 bits each.
const maxIntDigits = 6

var encodeDigit = func() (t [64]byte) {
	copy(t[:], alphabet)
	return t
}()

// decodeDigit maps a byte to its digit value, or -1 when it is not a digit and
// therefore ends the number.
//
// The table covers all 256 byte values, as delta.c's does. Indexing a
// 128-entry table with b&0x7f instead would make byte 0x80|c decode as the
// digit c, so a delta holding high bytes where a number belongs would parse
// differently here than anywhere else. Valid deltas only ever put ASCII digits
// in that position, so it affects malformed input only.
var decodeDigit = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		t[alphabet[i]] = int8(i)
	}
	return t
}()

// appendInt appends v in the delta format's base-64, most significant digit
// first, and then the command byte c. Numbers are always followed by a command
// byte, so the two are written together into one stack buffer.
func appendInt(dst []byte, v uint32, c byte) []byte {
	var buf [maxIntDigits + 1]byte
	i := len(buf) - 1
	buf[i] = c
	if v == 0 {
		i--
		buf[i] = '0'
	} else {
		for v > 0 {
			i--
			buf[i] = encodeDigit[v&0x3f]
			v >>= 6
		}
	}
	return append(dst, buf[i:]...)
}

// decodeInt reads the number starting at b[i] and returns it along with the
// index of the first byte that is not a digit.
//
// Like delta.c, it accepts an empty number, returning zero without consuming
// anything, and it lets a very long run of digits wrap around rather than
// treating that as an error: every value it can produce is bounds checked by
// the caller before it is used.
func decodeInt(b []byte, i int) (uint32, int) {
	var v uint32
	for ; i < len(b); i++ {
		d := decodeDigit[b[i]]
		if d < 0 {
			break
		}
		v = v<<6 + uint32(d)
	}
	return v, i
}

// digitCount is the number of digits appendInt writes for v, which the encoder
// needs in order to price a copy command before committing to it.
//
// It measures v at its full width rather than narrowing to uint32 first. The
// encoder never reaches a value that does not fit, but narrowing first answers
// zero for v == 1<<32 rather than six, and a sizing function that can return a
// smaller answer than the truth is the wrong shape to leave lying around.
func digitCount(v int) int {
	if v <= 0 {
		return 1
	}
	return (bits.Len(uint(v)) + 5) / 6
}
