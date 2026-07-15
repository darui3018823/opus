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
