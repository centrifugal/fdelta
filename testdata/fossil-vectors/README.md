# Fossil delta test vectors

Frozen origin/target/delta triples, used by `vectors_test.go`.

The `delta` file in each directory was **not** produced by any Go code.
`generate-deltas.sh`, kept here as it was, built each one with Fossil itself:

```sh
fossil test-delta-create ${D}/origin ${D}/target ${D}/delta
```

So these are the reference implementation's own output, captured at a point in
time. That is what makes them worth carrying: every other differential test in
this package compares against an implementation running right now, `src/delta.c`
under the `cref` tag, and that cannot catch two implementations drifting
together. These bytes cannot drift.

`Apply` reproduces every target from the stored delta, and `Create` produces a
delta no larger than the stored one. The two encoders are not expected to emit
the same bytes: this one indexes its blocks differently, and reads the hash
window as unsigned where `delta.c` reads it as signed.

## What each one covers

| | origin | target | delta | content |
|---|---:|---:|---:|---|
| `1` | 1757 B | 1755 B | 72 B | prose, small edit — *The Count of Monte Cristo*, public domain |
| `2` | 8214 B | 8214 B | 187 B | a bitmap: binary, and the only vector with bytes above 0x7f |
| `3` | 1061 B | 907 B | 920 B | prose, heavily rewritten — *A Study in Scarlet*, public domain; the delta is larger than the target, the case a caller must fall back on |
| `4` | 58 B | 60 B | 28 B | small JSON, the shape of a publication payload |
| `5` | 4 B | 6 B | 17 B | shorter than the 16-byte hash window, so it takes the literal-only path |

`TestFossilVectors_CoverHighBytes` asserts that the set keeps covering the high
bytes and the short origin, so replacing the fixtures with text-only ones
fails rather than quietly narrowing the coverage.
