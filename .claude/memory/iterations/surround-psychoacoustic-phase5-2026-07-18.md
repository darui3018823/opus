# Surround Psychoacoustic Phase 5 Decision (2026-07-18)

Status: **Completed; allocation-trim slice adopted**

## Objective

Replace the content-independent surround path with one measured
libopus-derived channel-role decision while preserving mapping, packet
interoperability, loss recovery, duration semantics, and matched bytes.

## Upstream Analysis

The reference was libopus v1.6.1
(`22244de5a79bd1d6d623c32e72bf1954b56235be`), principally
`src/opus_multistream_encoder.c`, `src/opus_encoder.c`, and
`celt/celt_encoder.c`.

For mapping family 1 with more than two channels, libopus performs one
stateful 21-band MDCT analysis over the original interleaved input. Vorbis
channel roles feed left, center, or right masking aggregates; center contributes
to both sides at -0.5 log2 amplitude, and LFE is excluded. The resulting
channel-major signal-to-mask ratios are passed to each elementary encoder.
They independently influence CELT allocation trim, per-band dynamic
allocation, VBR targeting, SILK rate offsets, and LFE-special CELT behavior.

The first production hypothesis deliberately isolated only the mask-slope
contribution to CELT allocation trim. Fixed stream rates, bandwidth, VBR target
size, per-band dynalloc, mode routing, and LFE-special coding were unchanged.

## Permanent Reproducer

`surround_phase5_fixture_test.go` generates deterministic 5.1 and 7.1 inputs
covering:

- correlated front left/right plus an independently detailed center;
- diffuse side/rear content;
- an isolated 53/91 Hz LFE signal;
- one-sided silence and duplicated surround content;
- duplicate and silent decode mappings.

The `opusref` scoreboard uses libopus' surround-family constructor, not the
plain multistream constructor, and counts canonical elementary packet bytes
with `splitMultistreamPackets`.

## Measurement

All cells used 48 kHz, 20 ms, constrained VBR, 18 frames, and the same
aggregate bitrate before and after. Weighted SNR is the fixture's permanent
per-channel delay/gain-aligned diagnostic; the adoption guard separately caps
each active-channel regression.

| Fixture | Aggregate rate | Go child bytes before/after | Weighted SNR before | Weighted SNR after | libopus weighted SNR |
|---|---:|---|---:|---:|---:|
| 5.1 role-rich | 256 kb/s | `[4073 4258 2450 358]` / identical | 3.337 dB | 8.368 dB | 13.718 dB |
| 5.1 silent rear | 192 kb/s | `[3030 3094 1888 290]` / identical | 3.273 dB | 3.294 dB | 8.391 dB |
| 7.1 role-rich | 320 kb/s | `[3696 3864 3861 2264 340]` / identical | 2.531 dB | 6.023 dB | 6.180 dB |
| 7.1 duplicate sides | 256 kb/s | `[2943 3077 3077 1838 290]` / identical | 2.398 dB | 2.402 dB | 6.236 dB |

The 5.1 role-rich center improved from 4.361 to 30.792 dB. The largest
active-channel reduction in that cell was 0.035 dB, below the 0.3 dB guard.
LFE packet bytes and measured SNR were unchanged. The normal-suite regression
test also requires every elementary packet length to match the no-mask
baseline and encoder/decoder final ranges to agree on every frame.

## Adopted Changes

- Added a stateful, transactional 48 kHz CELT surround analyzer with libopus
  channel roles, pre-emphasis, MDCT band energies, spectral spreading, and
  energy-domain mask aggregation.
- Added a private multistream pre-encode policy seam after PCM validation and
  conversion, so every public PCM API and promoted multistream encode path runs
  the analysis.
- Passed each elementary encoder its channel-major energy mask and applied only
  the libopus mask-slope term to the already-coded CELT allocation-trim symbol.
- Reset analyzer overlap/pre-emphasis history with the parent encoder and
  commit analyzer state only after successful multistream packet assembly.
- Added deterministic fixture, matched-byte, final-range, mapping, reset, and
  libopus surround-scoreboard coverage.

Production commits:

- `5b6303b test(surround): add psychoacoustic parity scoreboard`
- `a9c8626 feat(surround): apply channel-role masking trim`
- `c674d04 test(surround): guard masking state and mappings`

## Deferred Independent Decisions

The following libopus consumers were not mixed into this adoption and remain
future measured slices rather than hidden experiment gates:

- per-band surround dynamic-allocation boosts;
- mask-aware CELT VBR target adjustment;
- SILK/hybrid mask-derived rate offsets;
- full LFE CELT behavior (low-band energy cap, transient/TF restrictions,
  spread/allocation policy, and narrow coded spectrum).

The fixed surround rate formula was not made signal-dependent because libopus
also keeps that nominal inter-stream split content-independent.

## Verification

Passed from PowerShell on 2026-07-18:

```powershell
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
go test -count=1 -run '^TestOfficialVectors$' -v .
go test -count=1 -tags opusref -run 'TestOpusSurroundPhase5Scoreboard|TestCGOMultistreamInteroperability' -v .
go test -count=1 -run 'TestSurroundExpertFrameDurationUsesSelectedRateAllocation|TestSurroundDecodeLossContract|TestSurroundDecoderPromotesDecode(FEC|PLC)' -v .
$env:OPUS_REAL_CORPUS = '1'
go test -count=1 -tags opusref -run TestOpusRealCorpusMatchedBitrateScoreboard -v .
```

The official-vector result remained 12/12 and the real-corpus scoreboard
encoded all 140/140 single-stream cells.

## Decision

**Adopt.** The isolated channel-role masking trim produces a large matched-byte
improvement on the target fixtures, preserves LFE and spatial side/rear guards,
and passes all interoperability and common verification gates. Phase 5 is
complete with this measured first psychoacoustic decision; the larger remaining
energy-mask consumers stay explicitly documented as follow-up parity work.
