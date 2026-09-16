package fdelta

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// largeDoc is the size of the biggest payload in the corpus, reduced in short
// mode so the emulated CI jobs stay quick.
func largeDoc() int {
	if testing.Short() {
		return 8 << 10
	}
	return 64 << 10
}

// corpusEntry is one origin/target pair the differential tests run over.
type corpusEntry struct {
	name           string
	origin, target []byte
}

// corpus returns pairs chosen to reach every branch of Create: sources below,
// at and above the hash window; targets that share everything, a prefix, a
// suffix, a middle, or nothing; and inputs repetitive enough to build the long
// collision chains where the candidate limit bites.
//
// The repetitive entries are quadratic in the reference implementation, which
// the differential tests run for every pair, so short mode shrinks them. Short
// mode is what the emulated 32-bit and big-endian CI jobs use: those need the
// branch coverage, not the payload sizes.
func corpus() []corpusEntry {
	repeat := 8 << 10
	if testing.Short() {
		repeat = 1 << 10
	}
	body := strings.Repeat("shared-body-payload-", 64)
	short := strings.Repeat("ab", 40)
	zeros := make([]byte, repeat*4)
	zerosChanged := make([]byte, repeat*4)
	zerosChanged[1000] = 1
	abab := bytes.Repeat([]byte("ab"), repeat)
	ababChanged := bytes.Repeat([]byte("ab"), repeat)
	ababChanged[len(ababChanged)/2] = 'c'

	s := func(name, origin, target string) corpusEntry {
		return corpusEntry{name, []byte(origin), []byte(target)}
	}
	return []corpusEntry{
		s("both empty", "", ""),
		s("empty origin", "", body),
		s("empty target", body, ""),
		s("single byte", "a", "b"),
		s("origin below hash window", "short", body),
		s("origin at hash window", "0123456789abcdef", body),
		s("origin one over hash window", "0123456789abcdefg", body),
		s("target below hash window", body, "tiny"),
		s("identical", body, body),
		s("prefix change", "x"+body, "y"+body),
		s("suffix change", body+"x", body+"y"),
		s("middle change", body+"MIDDLE"+body, body+"middle"+body),
		s("insert in middle", body, body[:100]+"inserted chunk"+body[100:]),
		s("delete from middle", body, body[:100]+body[400:]),
		s("truncated by half", body, body[:len(body)/2]),
		s("doubled", body, body+body),
		s("reversed", body, reverseString(body)),
		s("short repetitive", short, short[:40]+"X"+short[41:]),
		s("json 1kb", jsonDoc(1<<10, 1), jsonDoc(1<<10, 2)),
		s("json 16kb", jsonDoc(16<<10, 1), jsonDoc(16<<10, 2)),
		s("json large", jsonDoc(largeDoc(), 1), jsonDoc(largeDoc(), 2)),
		{"long run of zeros", zeros, zerosChanged},
		{"alternating pair", abab, ababChanged},
		{"high bytes", highBytes(0), highBytes(1)},
	}
}

// highBytes builds a payload of bytes above 0x7f, which the hash and the
// integer decoder both treat differently from ASCII.
func highBytes(seed int) []byte {
	b := make([]byte, 4096)
	for i := range b {
		b[i] = byte(0x80 + (i+seed*7)%0x7f)
	}
	return b
}

func reverseString(s string) string {
	b := []byte(s)
	slicesReverse(b)
	return string(b)
}

func slicesReverse(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}

func randomBytes(rnd *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rnd.IntN(256))
	}
	return b
}

// mutate returns a copy of src with a few random edits: the shape of change a
// publication delta normally sees.
func mutate(rnd *rand.Rand, src []byte) []byte {
	out := bytes.Clone(src)
	for range 1 + rnd.IntN(4) {
		if len(out) == 0 {
			out = append(out, byte(rnd.IntN(256)))
			continue
		}
		switch rnd.IntN(3) {
		case 0:
			out[rnd.IntN(len(out))] = byte(rnd.IntN(256))
		case 1:
			at := rnd.IntN(len(out))
			out = append(out[:at], append(randomBytes(rnd, 1+rnd.IntN(32)), out[at:]...)...)
		case 2:
			at := rnd.IntN(len(out))
			n := min(1+rnd.IntN(32), len(out)-at)
			out = append(out[:at], out[at+n:]...)
		}
	}
	return out
}

// jsonDoc builds a JSON document of about n bytes whose only varying part is a
// single field, the typical shape of a delta-compressed publication.
func jsonDoc(n, version int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"version":%05d,"items":[`, version)
	for i := 0; b.Len() < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":000,"name":"item-name-here","value":1234.5678,"ok":true}`)
	}
	b.WriteString(`]}`)
	return b.String()
}
