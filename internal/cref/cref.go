//go:build cgo && cref

// Package cref exposes Fossil's own src/delta.c to the tests, so that this
// module can be checked against the implementation that defines the format
// rather than only against a second Go implementation.
//
// It builds only under the cref tag, so the module needs no C toolchain for an
// ordinary `go test`. Run the comparison with:
//
//	go test -tags cref ./...
//
// The vendored C source in fossil/ is unmodified; see fossil/delta.c for its
// copyright and license, and shim.c for how it is compiled.
package cref

/*
#cgo CFLAGS: -O2 -I${SRCDIR}
#include <stdlib.h>
#include <string.h>
#include "fossil/delta.h"

unsigned int fdelta_ref_checksum(const char *z, unsigned int n);
int fdelta_ref_digit_count(int v);
unsigned int fdelta_ref_hash_once(const char *z);
*/
import "C"

import (
	"errors"
	"unsafe"
)

// ErrRejected reports that the reference implementation returned -1, its only
// way of saying that a delta is malformed or was built for a different source.
var ErrRejected = errors.New("cref: the reference implementation rejected the delta")

// ErrTooLarge reports that a delta declares an output larger than this harness
// is willing to allocate.
//
// delta_apply writes into a buffer its caller must size in advance from
// delta_output_size, which reports whatever the delta claims, before any of it
// has been checked. So a delta claiming a four gigabyte output costs four
// gigabytes to find out it is malformed. That is a property of the reference
// interface rather than a defect in this harness, and it is the behaviour
// fdelta.Apply avoids by validating before it allocates. Comparisons simply
// skip these.
var ErrTooLarge = errors.New("cref: declared output size exceeds the harness limit")

// maxHarnessOutput bounds what Apply will allocate on a delta's say-so.
const maxHarnessOutput = 1 << 26 // 64 MiB

// MaxDigitCount is one past the largest value DigitCount may be called with.
//
// delta.c's digit_count walks a threshold upward by six bits at a time in a
// signed int: for v >= 2^30 that threshold overflows to zero on the next step
// and the loop never terminates. Fossil never reaches it because the values
// come from file offsets within a delta it just built, but it does mean the
// reference encoder does not terminate on payloads of a gibibyte or more.
// fdelta.digitCount is total over every int.
const MaxDigitCount = 1 << 30

// buffer copies b into C memory so that the reference always sees a
// suitably aligned, non-nil pointer, as it would inside Fossil. Passing Go
// memory directly would work for the pointer rules but not for delta.c's
// four-byte alignment assertion in checksum().
type buffer struct {
	p *C.char
	n C.uint
}

func newBuffer(b []byte) buffer {
	// One spare byte, so a zero length still yields a valid pointer and so
	// delta_apply can write its trailing NUL.
	p := C.malloc(C.size_t(len(b)) + 1)
	if p == nil {
		panic("cref: out of memory")
	}
	if len(b) > 0 {
		C.memcpy(p, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	}
	return buffer{p: (*C.char)(p), n: C.uint(len(b))}
}

func (b buffer) free() { C.free(unsafe.Pointer(b.p)) }

func (b buffer) bytes(n int) []byte {
	return C.GoBytes(unsafe.Pointer(b.p), C.int(n))
}

// Create returns the delta that delta_create() produces from origin to target.
func Create(origin, target []byte) []byte {
	src := newBuffer(origin)
	defer src.free()
	out := newBuffer(target)
	defer out.free()

	// delta_create's contract: the buffer must be at least 60 bytes longer
	// than the target.
	dst := C.malloc(C.size_t(len(target)) + 128)
	if dst == nil {
		panic("cref: out of memory")
	}
	defer C.free(dst)

	n := C.delta_create(src.p, src.n, out.p, out.n, (*C.char)(dst))
	if n < 0 {
		panic("cref: delta_create failed")
	}
	return C.GoBytes(dst, n)
}

// Apply returns the bytes that delta_apply() produces from origin, or
// ErrRejected if the reference refuses the delta.
//
// It is compiled with FOSSIL_ENABLE_DELTA_CKSUM_TEST, so it verifies the
// checksum as this package does.
func Apply(origin, delta []byte) ([]byte, error) {
	size := OutputSize(delta)
	if size < 0 {
		return nil, ErrRejected
	}
	if size > maxHarnessOutput {
		return nil, ErrTooLarge
	}

	src := newBuffer(origin)
	defer src.free()
	d := newBuffer(delta)
	defer d.free()

	// delta_apply writes a NUL past the output, so one spare byte is required.
	dst := C.malloc(C.size_t(size) + 1)
	if dst == nil {
		panic("cref: out of memory")
	}
	defer C.free(dst)

	n := C.delta_apply(src.p, C.int(src.n), d.p, C.int(d.n), (*C.char)(dst))
	if n < 0 {
		return nil, ErrRejected
	}
	return C.GoBytes(dst, n), nil
}

// OutputSize returns what delta_output_size() reports, or -1.
func OutputSize(delta []byte) int {
	d := newBuffer(delta)
	defer d.free()
	return int(C.delta_output_size(d.p, C.int(d.n)))
}

// Checksum returns the value delta.c's static checksum() computes.
func Checksum(b []byte) uint32 {
	buf := newBuffer(b)
	defer buf.free()
	return uint32(C.fdelta_ref_checksum(buf.p, buf.n))
}

// DigitCount returns what delta.c's static digit_count() computes.
//
// v must be below [MaxDigitCount]; above that the reference does not
// terminate, so calling it would hang the test binary rather than fail it.
func DigitCount(v int) int {
	if v >= MaxDigitCount {
		panic("cref: digit_count does not terminate for this value")
	}
	return int(C.fdelta_ref_digit_count(C.int(v)))
}

// HashOnce returns delta.c's hash of the first 16 bytes of b.
//
// Note that delta.c reads the window through a plain char, which is signed on
// the usual platforms, so this differs from any implementation that treats the
// bytes as unsigned whenever a byte is above 0x7f. The hash never leaves the
// encoder, so implementations disagreeing here is not a compatibility problem;
// it is only a reason not to compare hashes on arbitrary input.
func HashOnce(b []byte) uint32 {
	if len(b) < 16 {
		panic("cref: HashOnce needs at least 16 bytes")
	}
	buf := newBuffer(b)
	defer buf.free()
	return uint32(C.fdelta_ref_hash_once(buf.p))
}
