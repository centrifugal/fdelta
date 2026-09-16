package fdelta_test

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/centrifugal/fdelta"
)

func Example() {
	previous := []byte(`{"user":"alice","status":"online","score":1200,"rank":"gold"}`)
	current := []byte(`{"user":"alice","status":"online","score":1350,"rank":"gold"}`)

	patch := fdelta.Create(previous, current)

	// Send the patch instead of the payload only when it is actually smaller.
	if len(patch) >= len(current) {
		fmt.Println("send the payload in full")
		return
	}

	got, err := fdelta.Apply(previous, patch)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("patch is %d bytes for a %d byte payload, applies correctly: %v\n",
		len(patch), len(current), bytes.Equal(got, current))
	// Output: patch is 32 bytes for a 61 byte payload, applies correctly: true
}

// Create always succeeds, but a delta is not always worth sending: when the
// payloads have little in common it is larger than the payload itself.
func ExampleCreate_notAlwaysSmaller() {
	previous := []byte("the quick brown fox jumps over the lazy dog")
	unrelated := []byte("entirely different content with nothing in common!!")

	patch := fdelta.Create(previous, unrelated)
	fmt.Println("worth sending:", len(patch) < len(unrelated))
	// Output: worth sending: false
}

// Apply separates the two failures a caller has to handle differently.
func ExampleApply_errors() {
	base := []byte("the quick brown fox jumps over the lazy dog, at some length")
	patch := fdelta.Create(base, []byte("the quick brown cat jumps over the lazy dog, at some length"))

	// The base this client holds has drifted from the one the delta was built
	// against, so the delta reconstructs something else and the checksum says so.
	drifted := bytes.Clone(base)
	drifted[0] = 'T'

	_, err := fdelta.Apply(drifted, patch)
	switch {
	case errors.Is(err, fdelta.ErrChecksumMismatch):
		fmt.Println("wrong base: ask for the payload in full")
	case errors.Is(err, fdelta.ErrInvalidDelta):
		fmt.Println("malformed delta: drop the connection")
	case err != nil:
		fmt.Println("other error:", err)
	default:
		fmt.Println("applied")
	}

	_, err = fdelta.Apply(base, []byte("not a delta at all"))
	fmt.Println("malformed:", errors.Is(err, fdelta.ErrInvalidDelta))
	// Output:
	// wrong base: ask for the payload in full
	// malformed: true
}

// A client applying a stream of deltas can reuse two buffers instead of
// allocating a payload per message: the delta is applied into the buffer
// holding the base from before last, which nothing refers to any more.
func ExampleAppendApply_bufferReuse() {
	payloads := [][]byte{
		[]byte(`{"seq":1,"body":"a reasonably long payload that changes slightly"}`),
		[]byte(`{"seq":2,"body":"a reasonably long payload that changes slightly"}`),
		[]byte(`{"seq":3,"body":"a reasonably long payload that changes slightly"}`),
	}

	var buffers [2][]byte
	base := payloads[0]

	for i := 1; i < len(payloads); i++ {
		patch := fdelta.Create(base, payloads[i])

		next := buffers[i%2][:0]
		next, err := fdelta.AppendApply(next, base, patch)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		buffers[i%2] = next
		base = next
		fmt.Printf("seq %d applied, %d bytes\n", i+1, len(base))
	}
	// Output:
	// seq 2 applied, 66 bytes
	// seq 3 applied, 66 bytes
}

// A delta is small but its output need not be, so a peer taking deltas from an
// untrusted source should set its own ceiling before applying one.
func ExampleOutputSize() {
	base := bytes.Repeat([]byte("payload-"), 1024)
	patch := fdelta.Create(base, append(bytes.Clone(base), "tail"...))

	const maxPayload = 4 << 10

	size, err := fdelta.OutputSize(patch)
	if err != nil {
		fmt.Println("malformed delta:", err)
		return
	}
	if size > maxPayload {
		fmt.Printf("refusing a %d byte delta that declares %d bytes of output\n",
			len(patch), size)
		return
	}
	fmt.Println("would apply")
	// Output: refusing a 38 byte delta that declares 8196 bytes of output
}
