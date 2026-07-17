# PLC/FEC Phase 2 Adoption (2026-07-17)

Status: **Adopted**

Last updated: 2026-07-17

Plan: `.claude/plans/post-audit-2026-07-17.md`

## Objective

Close the audited PLC/FEC duration, packed-packet, initial-state, final-range,
and public PCM-format semantic gaps without breaking the existing v1
`DecodeFEC(data, pcm)` signature.

## API Decisions

- `DecodeFECWithDuration` is an additive v1 API. Its `frameSize` is the exact
  missing duration in output samples per channel, matching libopus.
- The original `DecodeFEC` remains available and infers the missing duration
  from the carrier packet's total duration. Its historical CELT-only error is
  retained; the explicit API uses PLC fallback.
- Initial zero PLC, the complete 2.5 ms duration set through 120 ms, packed FEC,
  and PLC/FEC `FinalRange` updates are compatible semantic corrections.
- PLC/FEC int16, signed-24-bit-in-int32, float32, and float64 variants are
  adopted now rather than deferred to v2. Multistream exposes the same set and
  surround inherits it.

## Adopted Behavior

- PLC before the first packet returns exactly the requested amount of zero PCM.
- PLC accepts every positive 2.5 ms multiple through 120 ms and uses CELT
  2.5/5/10/20 ms geometry switching plus SILK minimum-frame handling.
- FEC reads only the first Opus frame in a packed carrier. A longer requested
  loss is recovered as PLC prefix plus FEC suffix; unavailable FEC falls back
  to PLC in the explicit API.
- PLC reports final range zero. SILK-only FEC reports the first recovered frame's
  entropy range, while hybrid FEC reports the CELT PLC RNG state. Multistream
  and surround XOR their elementary final ranges.
- FEC recovery is transactional. Complete CELT, SILK, stereo, resampler, and
  top-level decoder state is staged, and caller PCM is written only after all
  elementary streams succeed.

## Evidence

- Generated normal tests cover initial PLC at all five sample rates, all 48
  2.5 ms duration quanta, packed first-frame FEC, explicit PLC-prefix recovery,
  error state/output atomicity, PCM variants, multistream aggregation, and a
  5.1 surround initial-state contract.
- The `opusref` semantic test compares initial PLC PCM/final range and explicit
  SILK FEC final range with libopus 1.6.1.
- Implementation commits:
  - `d05ab90 feat(decoder): align PLC and FEC loss semantics`
  - `c490039 fix(decoder): make FEC recovery transactional`

## Verification

The Phase 2 completion run used:

```powershell
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
go test -count=1 -run '^TestOfficialVectors$' -v .
```

All commands passed on 2026-07-17. The focused semantic oracle also passed:

```powershell
go test -count=1 -tags opusref -run '^TestCGODecodeLossSemantics$' -v .
```
