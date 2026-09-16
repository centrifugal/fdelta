package fdelta

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/golden.txt from the current encoder")

// These golden files pin the encoder against itself: any
// change to which matches it finds shows up as a diff, whether that change was
// intended or not. What they cannot do is tell right from wrong, so they sit
// alongside the tests that can -- every delta here is also applied back, by
// this package, by the vendored Go reference, and under the cref tag by
// Fossil's own C code.
//
// Regenerate with:
//
//	go test -run TestGolden -update
//
// and read the diff before committing it. A change here is a change in what
// every client receives.
const goldenFile = "testdata/golden.txt"

// goldenCases are fixed, so the file does not depend on -short or on anything
// else that varies between runs.
func goldenCases() []corpusEntry {
	body := strings.Repeat("shared-body-payload-", 64)
	zeros := make([]byte, 8<<10)
	zerosChanged := make([]byte, 8<<10)
	zerosChanged[1000] = 1
	abab := bytes.Repeat([]byte("ab"), 4<<10)
	ababChanged := bytes.Clone(abab)
	ababChanged[len(ababChanged)/2] = 'c'

	s := func(name, origin, target string) corpusEntry {
		return corpusEntry{name, []byte(origin), []byte(target)}
	}
	return []corpusEntry{
		s("both-empty", "", ""),
		s("empty-origin", "", body),
		s("empty-target", body, ""),
		s("origin-below-window", "short", body),
		s("origin-at-window", "0123456789abcdef", body),
		s("origin-one-over-window", "0123456789abcdefg", body),
		s("identical", body, body),
		s("prefix-change", "x"+body, "y"+body),
		s("suffix-change", body+"x", body+"y"),
		s("middle-change", body+"MIDDLE"+body, body+"middle"+body),
		s("insert-in-middle", body, body[:100]+"inserted chunk"+body[100:]),
		s("delete-from-middle", body, body[:100]+body[400:]),
		s("truncated-by-half", body, body[:len(body)/2]),
		s("doubled", body, body+body),
		s("reversed", body, reverseString(body)),
		s("json-1kb", jsonDoc(1<<10, 1), jsonDoc(1<<10, 2)),
		s("json-16kb", jsonDoc(16<<10, 1), jsonDoc(16<<10, 2)),
		s("json-64kb", jsonDoc(64<<10, 1), jsonDoc(64<<10, 2)),
		{"zeros-8kb", zeros, zerosChanged},
		{"abab-8kb", abab, ababChanged},
		{"high-bytes", highBytes(0), highBytes(1)},
		// High-entropy payloads are the ones where the block index actually
		// decides which matches are found, so these pin the bucket reduction
		// itself; the structured cases above come out the same whatever it
		// does. Generated from a fixed sequence so the file is reproducible.
		{"entropy-8kb", lcgBytes(8<<10, 1), lcgBytes(8<<10, 2)},
		{"entropy-64kb-mutated", lcgBytes(64<<10, 3), mutateDeterministic(lcgBytes(64<<10, 3), 7)},
	}
}

// lcgBytes returns n deterministic pseudo-random bytes, so golden files do not
// depend on the implementation of math/rand.
func lcgBytes(n int, seed uint64) []byte {
	b := make([]byte, n)
	x := seed
	for i := range b {
		x = x*6364136223846793005 + 1442695040888963407
		b[i] = byte(x >> 33)
	}
	return b
}

// mutateDeterministic edits src the way a payload changes between two
// publications, without depending on a random source.
func mutateDeterministic(src []byte, seed uint64) []byte {
	out := bytes.Clone(src)
	x := seed
	for range 8 {
		x = x*6364136223846793005 + 1442695040888963407
		at := int(x>>33) % len(out)
		out[at] ^= 0xff
	}
	return append(out, "appended tail"...)
}

func TestGolden(t *testing.T) {
	cases := goldenCases()

	if *updateGolden {
		var b strings.Builder
		b.WriteString("# Deltas this encoder produces, one per line: name, then the delta in hex.\n")
		b.WriteString("# Regenerate with: go test -run TestGolden -update\n")
		for _, tc := range cases {
			fmt.Fprintf(&b, "%s %s\n", tc.name, hex.EncodeToString(Create(tc.origin, tc.target)))
		}
		if err := os.WriteFile(goldenFile, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", goldenFile)
		return
	}

	want := readGolden(t)
	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen[tc.name] = true
			w, ok := want[tc.name]
			if !ok {
				t.Fatalf("no golden entry; run: go test -run TestGolden -update")
			}
			got := Create(tc.origin, tc.target)
			if !bytes.Equal(got, w) {
				t.Fatalf("delta changed\n got  %x\n want %x\nIf this is intended, regenerate with -update and read the diff.", got, w)
			}
			// A golden file only pins the bytes. These prove they are right.
			out, err := Apply(tc.origin, got)
			if err != nil || !bytes.Equal(out, tc.target) {
				t.Fatalf("the golden delta does not reproduce the target: %v", err)
			}
		})
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("golden file has a stale entry %q; regenerate with -update", name)
		}
	}
}

func readGolden(t *testing.T) map[string][]byte {
	t.Helper()
	f, err := os.Open(goldenFile)
	if err != nil {
		t.Fatalf("%v; generate it with: go test -run TestGolden -update", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("closing %s: %v", goldenFile, err)
		}
	}()

	out := map[string][]byte{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, h, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("malformed line in %s: %q", goldenFile, line)
		}
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatalf("malformed hex for %q: %v", name, err)
		}
		out[name] = b
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	return out
}
