//go:build jsref

package fdelta

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"testing"
)

// Interoperability with the JavaScript decoder that runs in browsers.
//
// Checking against Fossil's src/delta.c under the cref tag proves conformance
// to the reference implementation. This proves something different and, for a
// server whose clients are browsers, more immediately useful: that the deltas
// this encoder produces are read correctly by the code on the other side of
// the connection. Centrifugo's JavaScript SDK vendors applyDelta from the
// project in internal/jsref, so that is the decoder in question.
//
// It needs node on PATH. Run with:
//
//	make interop-js

type jsCase struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
	Target string `json:"target"`
	Delta  string `json:"delta"`
}

type jsResult struct {
	Total    int      `json:"total"`
	Failures []string `json:"failures"`
}

func runJS(t *testing.T, cases []jsCase) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH")
	}

	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("node", "internal/jsref/apply.mjs")
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	runErr := cmd.Run()

	var res jsResult
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &res); err != nil {
		t.Fatalf("running the decoder: %v\nstdout %q\nstderr %q", runErr, out.String(), errb.String())
	}
	if res.Total != len(cases) {
		t.Fatalf("the decoder saw %d cases, sent %d", res.Total, len(cases))
	}
	for _, f := range res.Failures {
		t.Error("javascript decoder: " + f)
	}
	t.Logf("%d deltas applied correctly by the JavaScript decoder", res.Total)
}

func jsCaseFrom(name string, origin, target []byte) jsCase {
	return jsCase{
		Name:   name,
		Origin: hex.EncodeToString(origin),
		Target: hex.EncodeToString(target),
		Delta:  hex.EncodeToString(Create(origin, target)),
	}
}

// Every delta this encoder produces for the test corpus, the golden cases and
// a spread of random payloads must be readable by the JavaScript decoder.
func TestJSRef_OurDeltasApplyInJavaScript(t *testing.T) {
	var cases []jsCase
	for _, tc := range corpus() {
		cases = append(cases, jsCaseFrom("corpus/"+tc.name, tc.origin, tc.target))
	}
	for _, tc := range goldenCases() {
		cases = append(cases, jsCaseFrom("golden/"+tc.name, tc.origin, tc.target))
	}

	rnd := rand.New(rand.NewPCG(31, 37))
	for i := range 300 {
		origin := randomBytes(rnd, rnd.IntN(6000))
		cases = append(cases, jsCaseFrom("mutated/"+itoa(i), origin, mutate(rnd, origin)))
	}
	// The payload shapes a publication actually carries, at realistic sizes,
	// including the high-entropy case the block index handles differently from
	// the reference.
	for _, n := range []int{1 << 10, 16 << 10, 64 << 10} {
		cases = append(cases,
			jsCaseFrom("json/"+itoa(n), []byte(jsonDoc(n, 1)), []byte(jsonDoc(n, 2))),
			jsCaseFrom("entropy/"+itoa(n), randomBytes(rnd, n), randomBytes(rnd, n)))
	}

	runJS(t, cases)
}

// Deltas built from arbitrary valid commands, which this encoder would never
// emit, must also read correctly there.
func TestJSRef_ArbitraryValidDeltas(t *testing.T) {
	rnd := rand.New(rand.NewPCG(41, 43))
	var cases []jsCase
	for _, originLen := range []int{0, 1, 16, 17, 256, 4096} {
		origin := randomBytes(rnd, originLen)
		for _, commands := range []int{0, 1, 3, 20, 200} {
			for range 5 {
				delta, want := buildDelta(rnd, origin, commands)
				cases = append(cases, jsCase{
					Name:   "arbitrary/" + itoa(originLen) + "/" + itoa(commands),
					Origin: hex.EncodeToString(origin),
					Target: hex.EncodeToString(want),
					Delta:  hex.EncodeToString(delta),
				})
			}
		}
	}
	runJS(t, cases)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
