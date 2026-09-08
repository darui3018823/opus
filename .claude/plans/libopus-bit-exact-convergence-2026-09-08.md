# libopus Bit-Exact Convergence Plan

Last updated: 2026-09-08
Status: Active

## Objective

Continuously move the Pure Go codec toward the checked-in libopus reference,
with bit-exact behavior as the long-term target. Work in small, reviewable
slices: read the corresponding C implementation, port its arithmetic and state
transitions faithfully, run reference and regression tests, record the
remaining mismatch, and repeat.

## Scope

- Use the local `libopus/` source tree as the implementation reference for each
  slice. Name the C files and functions in tests or commit messages when useful.
- Prefer exact integer, rounding, saturation, entropy, and state-transition
  semantics over approximations that merely produce similar audio.
- Build hard oracles for deterministic behavior: accepted packet structure,
  decoded duration, integer PCM, final range, encoder packet bytes, and state
  after reset or mode transitions.
- Keep normal builds Pure Go. The `opusref` build tag may use CGO only for test
  oracles and diagnostics.
- Preserve RFC 6716 interoperability and all currently passing public API,
  official-vector, fuzz, race, and quality gates while convergence proceeds.
- Defer libopus 1.6.1 neural/AI features (including DRED, OSCE, and DNN-backed
  processing) until core codec convergence is exhausted. At that final stage,
  implement only features whose models, weights, licenses, runtime cost, and
  Pure Go integration are practical and independently testable.

The objective is intentionally broader than one release-sized phase. A slice
is complete only when its evidence is committed; the plan remains active while
any libopus/Go mismatch remains.

## Permanent Work Loop

1. Select the smallest observable mismatch and identify the exact libopus C
   call path that defines it.
2. Create a child experiment branch from this convergence branch. Add or
   tighten an oracle that fails on the mismatch and reports its first
   divergent packet, sample, symbol, range, or state value.
3. Port the relevant C arithmetic and state transitions to Go without unrelated
   policy changes.
4. Run the narrow oracle, package tests, the normal suite, and the applicable
   `opusref` comparison.
5. Commit the verified slice using Conventional Commits, update the mismatch
   inventory, merge the accepted commits back into the convergence branch, and
   return to step 1. Delete or retain rejected experiment branches as useful
   evidence, but do not mix their production changes into accepted slices.

If a bit-exact slice requires a large fixed-point rewrite, first commit the
stage-level trace/oracle that localizes the mismatch. This keeps later ports
measurable and prevents a superficially close waveform from hiding an earlier
state or entropy divergence.

## Initial Mismatch Inventory

| Area | Current evidence | Next exactness gate |
|---|---|---|
| Entropy coder | Documented and tested as matching libopus range coding | Retain final-range and symbol traces |
| Decoder framing/range | Using the last constituent frame range removed 3680 mismatches; vectors 01-07 and 11 are exact, with only 1/1/13/12 mismatches in vectors 08/09/10/12 | Localize vector 08 packet 4, the first remaining mismatch |
| int16 decode conversion | Conversion semantics match `FLOAT2INT16`; direct `opus_decode` comparison now reports exact-sample percentage, first/max LSB delta, and range mismatch count | Raise mode-specific exact-sample coverage after range alignment |
| CELT synthesis | Float implementation is waveform-compatible, not sample-exact | Add per-stage/int16 delta localization before fixed-point ports |
| SILK synthesis/PLC | Parameter/range coverage exists; PLC is explicitly non-bit-exact | Compare fixed-point state and PCM on deterministic packet sequences |
| Encoder | Packets interoperate but are not byte-identical | Add mode-specific byte and first-symbol divergence traces |
| Mode/rate policy | Known partial parity in `docs/MODE_RATE_POLICY_DIFF.md` | Compare decisions and state over identical PCM/control sequences |

## Acceptance Criteria Per Slice

- The relevant libopus C source and semantics are identified.
- A deterministic test demonstrates the old mismatch or guards the newly exact
  behavior.
- The implementation matches the selected C behavior, including rounding,
  saturation, overflow width, and state update order.
- Narrow tests, `go test -count=1 ./...`, and applicable `opusref` tests pass.
- The change is committed independently with a Conventional Commit message.

## Verification

```text
go test -count=1 ./...
go vet ./...
go test -count=1 -tags opusref ./...
go test -race -count=1 ./...
go test -count=1 -run '^TestOfficialVectors$' -v .
```

Each slice should also name and run a narrower command that proves its specific
oracle. Expensive full gates may be checkpointed after a sequence of small
commits, but no mismatch is considered removed until the applicable reference
test passes.

## Completed Slices

### 2026-09-08: int16 output conversion

- Reference: `libopus/celt/float_cast.h::FLOAT2INT16`,
  `libopus/celt/mathops.c::celt_float2int16_c`, and
  `libopus/src/opus_decoder.c::opus_decode`.
- Replaced zero-direction truncation with float32-domain scaling, saturation,
  and nearest-even rounding.
- Unified single-stream and multistream normal decode, PLC, and FEC int16
  conversions on the same helper.
- Verified the focused rounding oracle, `go vet ./...`, normal and `opusref`
  suites, the full race suite, and all 12 official vectors.

### 2026-09-08: libopus int16 convergence oracle

- Added a CGO wrapper for libopus `opus_decode`, retaining `opus_decode_float`
  for quality comparisons but no longer using mixed output APIs for the
  bit-exact scoreboard.
- `TestCGORef` now checks libopus `OPUS_GET_FINAL_RANGE` against every official
  `.bit` record, compares int16 samples directly, and reports exact-sample
  coverage, first/max LSB delta, and Go final-range mismatch count.
- The zero-mismatch range baselines for vectors 02-07 are hard regression
  gates; nonzero vector baselines may decrease but may not increase.
- The first remaining entropy mismatch is vector 01 packet 0: Go `0aa13300`,
  libopus/bitstream `16230400`. This is the next convergence slice.
- Verified with `go test -count=1 -tags opusref -run '^TestCGORef$' -v .`,
  `go vet ./...`, and the complete normal and `opusref` suites.

### 2026-09-08: single-stream multi-frame final range

- Reference: `libopus/src/opus_decoder.c::opus_decode_native` and
  `opus_decode_frame`. The outer packet loop overwrites `rangeFinal` after each
  constituent frame; it does not XOR frame ranges from one stream.
- Added a focused oracle for vector 01 packet 0. All three CELT frame ranges
  already matched libopus independently; only the Go packet aggregation was
  wrong (`0aa13300` instead of the last frame's `16230400`).
- Applied last-frame replacement to CELT-only, SILK-only, and hybrid decode.
  Multistream continues to XOR the final ranges of its elementary streams.
- Official-vector range mismatches fell from 3707 total to 27: vector 08 has
  1, vector 09 has 1, vector 10 has 13, vector 12 has 12, and all other vectors
  have zero.
- Verified the focused oracle, complete normal and `opusref` suites,
  `go vet ./...`, and the complete race suite.
