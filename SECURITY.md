# Security

## Reporting

Report suspected vulnerabilities privately through GitHub's
[security advisory form](https://github.com/centrifugal/fdelta/security/advisories/new),
or by email to the address listed on https://centrifugal.dev. Please do not
open a public issue for a suspected vulnerability.

## Threat model

`Apply` is designed to be called on deltas from an untrusted peer. It is
expected to:

- never panic, for any input;
- never return output alongside an error, and never return partial output;
- never allocate on behalf of a delta it has not first validated;
- report `ErrChecksumMismatch` whenever the output does not match the checksum
  the delta carries.

A failure of any of those is a vulnerability in this package. The test suite
fuzzes `Apply` against these properties continuously, and the differential
fuzzers additionally require that anything Fossil's `delta.c` accepts is
accepted here to the same bytes.

`Create` is designed to be called on data you already hold. It is total and
never panics, but see the two limits below.

## Known limits, by design

These are properties of the Fossil delta format and are present in every
implementation of it, including Fossil's own. They are documented rather than
fixed because fixing them would mean no longer implementing the format.

**Output amplification.** A copy command costs about seven bytes and can copy
the whole of `origin`, so a delta that is itself tiny can legitimately demand
an enormous output, and `Apply` will produce it. This is the most likely way to
get hurt by this package, so it is worth stating with numbers. Measured against
a 1 MiB origin:

| delta | declares | ratio |
|---:|---:|---:|
| 3,508 B | 524,288,000 B | 149,455x |
| 14,009 B | 2,097,152,000 B | 149,700x |

The second took 406 ms and 2 GB of heap before failing its checksum. The
ceiling per call is 4 GiB, because the declared size is a 32-bit number, and
the ratio grows with the size of `origin`.

Validation does not help here: the delta is *well formed*, and really does
produce what it claims. A malformed delta is rejected without allocating (about
67 million rejections per second on one core); a well formed one costs what it
asks for.

Call `OutputSize` and impose your own ceiling before `Apply`. It reads the
header alone, allocates nothing, and `Apply` is guaranteed not to exceed what
it reported:

```go
if n, err := fdelta.OutputSize(delta); err != nil || n > maxPayload {
    // refuse, having done no work
}
```

**The checksum is not authentication.** It binds the output to the claim the
delta makes about it, which catches corruption and a wrong `origin`. Whoever
supplies the delta chooses the output and can supply a correct checksum for
whatever they chose. `ErrChecksumMismatch` means "this did not reconstruct what
the sender said it would", not "this came from someone entitled to send it". If
the deltas you apply need to be trusted, that has to come from elsewhere.

**The checksum does not catch every wrong source.** It is a plain sum of
big-endian 32-bit words, fixed by the format. That catches random corruption
and most cases of a delta applied to a base that has drifted from the one it
was built against, but not a drift that leaves the sum unchanged: two values
swapped at a distance that is a multiple of four bytes, for example, apply
without error to the wrong output. This only matters once a base has already
drifted, which is a bug somewhere else; with the right base the output is
always right. A protocol that must detect drift reliably should do so itself,
by tracking each payload's position in its stream or by carrying a stronger
hash alongside the delta, and treat the checksum as a backstop.

**Memory retained between calls.** `Create` pools its index tables, up to about
512 KB per entry, and `sync.Pool` keeps roughly one entry per P. On a machine
with many cores that is tens of megabytes held between garbage collections.

**`Create` cost grows with payload size.** It is close to linear — roughly
18ms for a mebibyte of random bytes against roughly 0.3ms for 64 kibibytes —
but it is not free, and high-entropy payloads are the expensive case because
the encoder finds nothing to copy and scans every byte.

This package does **not** share the algorithmic denial of service that the
reference has here. `delta.c` reduces its block hash to a bucket with a
remainder, which leaves the index collapsed on high-entropy input and far worse
when the payload size is a power of two: a mebibyte of random bytes costs it
roughly 292ms against 13ms for 1,000,000 bytes, a 22x penalty for a 4.8%
change in size. This package multiplies and takes the high bits instead, so
that dependence is gone. See `bucketIndex` for the detail.

If a remote peer can influence the size and content of payloads you diff, still
set a maximum payload size and send anything larger in full without calling
`Create`. That bounds the cost exactly, where relying on the encoder's speed
only bounds it roughly. Delta encoding high-entropy data is pointless anyway,
since the delta comes out larger than the payload.

**32-bit format limits.** Every length and offset in a delta is an unsigned
32-bit number, so inputs must be below 4 GiB. Given a larger input, `Create`
returns a delta that declares an empty output and is never terminated, which
`Apply` and every other implementation reject.

## Supported versions

Fixes are released against the latest minor version.
