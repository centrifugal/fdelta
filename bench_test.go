package fdelta

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// benchCase is one payload pair, named for the shape of change it represents.
type benchCase struct {
	name           string
	origin, target []byte
	delta          []byte
}

// benchCases covers the shapes a publication delta actually takes: one field
// changed, a block rewritten, nothing changed, and nothing in common.
func benchCases() []benchCase {
	rnd := rand.New(rand.NewPCG(1, 2))
	var cases []benchCase
	for _, size := range []int{1 << 10, 4 << 10, 16 << 10, 64 << 10} {
		origin := []byte(jsonDoc(size, 1))
		kb := size >> 10
		for _, c := range []struct {
			name   string
			target []byte
		}{
			{"small-change", []byte(jsonDoc(size, 2))},
			{"chunk-change", chunkChange(origin)},
			{"identical", origin},
			{"unrelated", randomBytes(rnd, size)},
		} {
			bc := benchCase{
				name:   fmt.Sprintf("json-%dkb/%s", kb, c.name),
				origin: origin,
				target: c.target,
			}
			bc.delta = Create(bc.origin, bc.target)
			cases = append(cases, bc)
		}
	}
	return cases
}

func chunkChange(origin []byte) []byte {
	out := append([]byte(nil), origin...)
	at := len(out) / 2
	copy(out[at:], strings.Repeat("Z", min(256, len(out)-at)))
	return out
}

var sink []byte

func BenchmarkCreate(b *testing.B) {
	for _, tc := range benchCases() {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.target)))
			b.ReportAllocs()
			for b.Loop() {
				sink = Create(tc.origin, tc.target)
			}
		})
	}
}

func BenchmarkApply(b *testing.B) {
	for _, tc := range benchCases() {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.target)))
			b.ReportAllocs()
			for b.Loop() {
				out, err := Apply(tc.origin, tc.delta)
				if err != nil {
					b.Fatal(err)
				}
				sink = out
			}
		})
	}
}

func BenchmarkAppendCreate(b *testing.B) {
	tc := benchCases()[8] // json-16kb/small-change
	buf := make([]byte, 0, 1024)
	b.SetBytes(int64(len(tc.target)))
	b.ReportAllocs()
	for b.Loop() {
		buf = AppendCreate(buf[:0], tc.origin, tc.target)
	}
	sink = buf
}

func BenchmarkAppendApply(b *testing.B) {
	tc := benchCases()[8]
	buf := make([]byte, 0, 32<<10)
	b.SetBytes(int64(len(tc.target)))
	b.ReportAllocs()
	for b.Loop() {
		var err error
		buf, err = AppendApply(buf[:0], tc.origin, tc.delta)
		if err != nil {
			b.Fatal(err)
		}
	}
	sink = buf
}

// Rejecting a malformed delta should cost nothing, since a peer can send them
// as fast as it likes.
func BenchmarkApplyReject(b *testing.B) {
	origin := []byte(jsonDoc(16<<10, 1))
	bad := []byte("3~~~~~\n2@0,")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Apply(origin, bad); err == nil {
			b.Fatal("expected an error")
		}
	}
}

func BenchmarkCreateParallel(b *testing.B) {
	tc := benchCases()[8]
	b.SetBytes(int64(len(tc.target)))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sink = Create(tc.origin, tc.target)
		}
	})
}
