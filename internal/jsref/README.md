# JavaScript decoder, for interoperability testing

`fossil-delta.mjs` is an unmodified copy of the published build of
[fossil-delta-js](https://github.com/dchest/fossil-delta-js), BSD licensed,
copyright Dmitry Chestnykh and D. Richard Hipp. It is test-only: nothing in
the package imports it, and it ships in no binary.

It is here because it is the decoder that actually runs in browsers.
Centrifugo's JavaScript SDK vendors `applyDelta` from this project into its own
`src/fossil.ts`, so a delta produced by a Go server is read by this code on the
other side of the connection. Checking against Fossil's `src/delta.c` proves
conformance to the reference; checking against this proves the deployed path
works.

`apply.mjs` is the harness: it reads origin/target/delta triples as JSON on
stdin, applies each one, and reports any that throw or produce the wrong bytes.
`jsref_test.go` in the package root drives it.

Run it with:

    make interop-js

It needs `node` on PATH and is behind the `jsref` build tag, so an ordinary
`go test` neither needs nor runs it.
