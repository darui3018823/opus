# Multistream Aggregate CTL Iteration

Date: 2026-07-21
Status: Complete — adopted

## Objective

Close the public convenience gap between the Go multistream API and libopus
1.6.1 aggregate encoder/decoder CTLs without changing packet syntax or child
codec behavior.

## Reference Semantics

libopus broadcasts supported multistream SET CTLs to every elementary codec.
Most scalar GET CTLs query the first elementary codec, while bitrate is summed
and final range is XORed. The Go API already implemented aggregate bitrate,
final range, VBR/CVBR setters, complexity, expert duration, and direct child
access, but omitted most matching convenience methods.

## Adopted Surface

Encoder broadcasts and first-stream getters now cover application, signal,
VBR/CVBR, complexity, DTX, in-band FEC, packet-loss percentage, LSB depth,
prediction disabling, phase-inversion disabling, max/forced bandwidth, and
lookahead. Decoder broadcasts/getters now cover output gain and phase inversion,
with first-stream bandwidth and last-packet-duration getters.

`SurroundEncoder` and `SurroundDecoder` receive these methods through their
embedded multistream types. Projection retains its explicit wrapper surface and
per-stream access, so generic projection convenience remains partial.

## Tests

The aggregate tests use mixed coupled/mono layouts. They verify every child
receives each broadcast, scalar getters reflect the first child as libopus does,
and rejected gain/LSB-depth setters leave every child unchanged.

## Verification

```text
go test -count=1 -run '^TestMultistreamAggregate(Encoder|Decoder)Controls$' .
go test -count=1 ./...
go test -count=1 -tags opusref ./...
go vet ./...
```

The full normal and `opusref` suites pass.
