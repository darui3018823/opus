# Phase 1 Iteration Log

## Iteration 1: multistream and surround DecodeFEC

- Branch: `codex/feature-gaps`
- Production commit: `127ae27` (`feat(multistream): add FEC decoding`)
- Test support commit: `e7687db` (`test(opusref): add multistream FEC controls`)
- Interoperability commit: `cbe89b9`
  (`test(multistream): cross-check FEC with libopus`)
- Change: add `(*MultistreamDecoder).DecodeFEC`; `SurroundDecoder` exposes it
  through embedding. Self-delimited packets are split once, each SILK/hybrid
  stream uses its existing decoder FEC path, and CELT current/previous modes
  use PLC as libopus does. Mapping 255 and duplicate mappings are preserved,
  and caller PCM is copied only after every stream succeeds.
- Tests: Go SILK/LBRR round trip, mixed SILK/CELT fallback, CELT-to-SILK
  previous-mode fallback, output preservation on error, promoted surround API,
  and libopus multistream encode to Go FEC decode. The deterministic libopus
  fixture produced sample-identical Go/libopus FEC output (`+Inf dB` SNR).
- Validation: `go vet ./...`, `go test -count=1 ./...`, and
  `go test -count=1 -tags opusref ./...` all passed on 2026-07-16.
- Decision: adopted. The feature adds only a new public method and test-only
  libopus wrappers; existing normal decode and packet framing paths are
  unchanged.

## Iteration 2: multistream and surround DecodePLC

- Branch: `codex/feature-gaps`
- Production/test commit: `e7e8e84` (`feat(multistream): add PLC decoding`)
- Change: add `(*MultistreamDecoder).DecodePLC`; `SurroundDecoder` exposes it
  through embedding. The method validates frame size and output capacity before
  touching child state, conceals every elementary stream through its existing
  decoder, applies duplicate/silent mappings, and copies caller PCM only after
  all children succeed.
- Tests: mono SILK and coupled CELT concealment, mixed coupled-CELT/mono-SILK
  repeated loss with non-zero monotonic energy decay, mapping 255 and duplicate
  channels, normal-decode recovery continuity, validation state preservation,
  child failure output preservation, and promoted surround API.
- Validation: `go vet ./...`, `go test -count=1 ./...`, and
  `go test -count=1 -tags opusref ./...` all passed on 2026-07-16.
- Decision: adopted. The method adds a public aggregation path over existing
  PLC implementations without changing normal packet decode behavior.
