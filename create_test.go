package fdelta

import (
	"bytes"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
)

// Whatever Create produces has to be a delta this package reads back to
// exactly the target. That it is also a delta Fossil reads is checked under
// the cref tag, against src/delta.c itself, and by the frozen vectors in
// testdata/fossil-vectors, which Fossil produced. TestGolden pins which delta
// comes out; these prove it is a valid one.

func checkRoundTrip(t *testing.T, origin, target []byte) {
	t.Helper()
	got, err := Apply(origin, Create(origin, target))
	if err != nil || !bytes.Equal(got, target) {
		t.Fatalf("round trip: err=%v\norigin %q\ntarget %q", err, origin, target)
	}
}

func TestCreateRoundTripsEverything(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			checkRoundTrip(t, tc.origin, tc.target)
		})
	}
}

func TestCreateAppliesEverywhere_Mutations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(7, 11))
	for range 500 {
		origin := randomBytes(rnd, rnd.IntN(4096))
		checkRoundTrip(t, origin, mutate(rnd, origin))
	}
}

// Unrelated inputs make Create fall back to literal text and reach the paths a
// mutated copy rarely does.
func TestCreateAppliesEverywhere_Unrelated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(13, 17))
	for range 200 {
		checkRoundTrip(t, randomBytes(rnd, rnd.IntN(2048)), randomBytes(rnd, rnd.IntN(2048)))
	}
}

// Every length from zero to well past the hash window, so the boundaries
// around lenSrc <= hashWindow, the single-bucket table and the unscanned tail
// are all covered.
func TestCreateAppliesEverywhere_EveryShortLength(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(19, 23))
	for n := range 200 {
		for m := range 40 {
			checkRoundTrip(t, randomBytes(rnd, n), randomBytes(rnd, m))
		}
	}
}

func TestAppendCreate(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			want := Create(tc.origin, tc.target)

			// Appending to nil matches Create.
			if got := AppendCreate(nil, tc.origin, tc.target); !bytes.Equal(got, want) {
				t.Fatalf("AppendCreate(nil) = %q, want %q", got, want)
			}

			// Appending to existing content leaves it intact.
			prefix := []byte("keep me")
			got := AppendCreate(slices.Clone(prefix), tc.origin, tc.target)
			if !bytes.HasPrefix(got, prefix) {
				t.Fatalf("AppendCreate overwrote the prefix: %q", got)
			}
			if !bytes.Equal(got[len(prefix):], want) {
				t.Fatalf("AppendCreate appended %q, want %q", got[len(prefix):], want)
			}
		})
	}
}

// Reusing one buffer across many calls must give the same bytes as a fresh
// call every time, which is the whole point of AppendCreate.
func TestAppendCreateReusesBuffer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(29, 31))
	buf := make([]byte, 0, 64)
	for range 200 {
		origin := randomBytes(rnd, rnd.IntN(2048))
		target := mutate(rnd, origin)
		buf = AppendCreate(buf[:0], origin, target)
		if want := Create(origin, target); !bytes.Equal(buf, want) {
			t.Fatalf("reused buffer gave %q, want %q", buf, want)
		}
	}
}

// Create must not retain or expose its scratch tables, so deltas created
// concurrently must be identical to ones created alone. Sizes are mixed so the
// pool hands the same buffer back at different lengths.
func TestCreateConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(37, 41))
	type pair struct{ origin, target, want []byte }
	var pairs []pair
	for _, n := range []int{17, 64, 1024, 16 << 10, 40 << 10} {
		origin := randomBytes(rnd, n)
		target := mutate(rnd, origin)
		pairs = append(pairs, pair{origin, target, Create(origin, target)})
	}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				for _, p := range pairs {
					if got := Create(p.origin, p.target); !bytes.Equal(got, p.want) {
						t.Errorf("concurrent Create mismatch for %d byte origin", len(p.origin))
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// A source too large to pool must still work, and must not disturb the calls
// around it.
func TestCreateOversizeScratch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping an exhaustive differential test in short mode")
	}
	rnd := rand.New(rand.NewPCG(43, 47))
	big := randomBytes(rnd, (maxPooledHash+16)*hashWindow)
	bigTarget := mutate(rnd, big)
	out, err := Apply(big, Create(big, bigTarget))
	if err != nil || !bytes.Equal(out, bigTarget) {
		t.Fatalf("oversize source produced a delta that does not apply: %v", err)
	}

	// A normal sized call after it must not pick up anything from that one.
	small := randomBytes(rnd, 4096)
	smallTarget := mutate(rnd, small)
	want := Create(small, smallTarget)
	Create(big, bigTarget)
	if got := Create(small, smallTarget); !bytes.Equal(got, want) {
		t.Fatal("a call after an oversize source produced a different delta")
	}
}

func TestCreateDoesNotModifyInputs(t *testing.T) {
	for _, tc := range corpus() {
		t.Run(tc.name, func(t *testing.T) {
			origin, target := slices.Clone(tc.origin), slices.Clone(tc.target)
			Create(origin, target)
			if !bytes.Equal(origin, tc.origin) {
				t.Error("Create modified origin")
			}
			if !bytes.Equal(target, tc.target) {
				t.Error("Create modified target")
			}
		})
	}
}

func TestCreateAllocations(t *testing.T) {
	origin := []byte(jsonDoc(16<<10, 1))
	target := []byte(jsonDoc(16<<10, 2))
	Create(origin, target) // prime the pool

	got := testing.AllocsPerRun(100, func() { Create(origin, target) })
	// One allocation, for the delta itself: the hash tables come from the pool.
	if got > 1 {
		t.Fatalf("Create made %v allocations per call, want at most 1", got)
	}
}
