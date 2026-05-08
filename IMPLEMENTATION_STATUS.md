# Opus 1.3.1 Compliance & Action Plan

This document tracks gaps versus the Opus 1.3.1 specification, prioritizes implementation, and defines validation/compatibility work. The library must remain **Pure Go** (no cgo / external binaries).

## Current Coverage Snapshot
- ✅ RFC6716 framing, TOC parsing, basic CELT/SILK scaffolding, resampler, entropy coder scaffolding
- ⚠️ Bit-exact Opus 1.3.1 behavior is **not yet verified**; several mandatory features are incomplete.
- ❌ No official 1.3.1 test-vector automation in-tree yet.

## Spec Gaps (prioritized)
1. **Range coder parity**: symbol/uint ICDF paths must match libopus bit-exactly (highest priority for decoder correctness).
2. **CELT details**: band energy/fine energy decoding, PVQ cwrs tables, stereo coupling, anti-collapse, post-filter; transient handling; spreads.
3. **SILK details**: NLSF decode, pitch/LPC synthesis, PLC/FEC/DTX behaviors, VBR/bitrate controls alignment.
4. **Multistream/Surround**: mapping family 1/255 packing, channel coupling matrices, LFE handling.
5. **Packet loss concealment**: DecodePLC behavior parity across frame sizes and bandwidths.
6. **120 ms packets & padding/trimming**: long frames, padding bit semantics, in-band gain.
7. **Pre-skip/seek/TOC edge cases**: invalid TOCs, malformed size fields, extreme small/large packets.

## Validation & Test Automation Plan
- **Official vectors**: Add harness to replay Opus 1.3.1 reference test vectors; assert bit-exact outputs per sample rate (8/12/16/24/48 kHz), mono/stereo, frame sizes 2.5–60 ms (and 120 ms once implemented).
- **Matrix coverage**: Table-driven tests for all `(sample rate, channels, application, frame size)` combinations; include hybrid vs CELT-only vs SILK-only paths.
- **Compatibility assertions**: Golden outputs vs libopus, `opusfile`, and Go libs (e.g., `layeh.com/gopus`) for selected vectors; fail on API/behavior divergence.
- **Robustness**: Fuzzers on decoder, parser, and resampler (invalid TOCs, truncated packets, huge padding, NaNs); keep timeouts and allocation caps.
- **Broken inputs**: Targeted tests for corrupted frames, length mismatches, empty and oversized packets.

## Compatibility Table (to build)
- **API shape**: Map public functions/consts against libopus, opusfile, and Go ecosystems; document deviations and shims.
- **Behavior**: Bitrate control, VBR/CBR toggle effects, DTX/FEC flags, PLC outputs, error codes.
- **Artifacts**: Markdown table plus automated assertions in tests for a selected subset of APIs.

## Performance & Efficiency
- **Profiling**: Benchmarks for encoder/decoder hot paths (MDCT/IMDCT, PVQ, range coder). Track allocations; guard against regressions.
- **GC/alloc control**: Prefer stack/pooled buffers; avoid per-frame heap growth. No cgo or external binaries allowed.

## Documentation Tasks
- GoDoc for all public APIs with parameter/behavior notes.
- FAQ + spec-diff table: explicitly list known divergences until closed.
- Call out Pure Go guarantee and test vector expectations.

## Critiques / Follow-up
- Previous external reviews flagged “empty/incomplete” coverage. Concrete actions: ship the above spec gap list, publish compatibility tables, and add bit-exact vector tests to demonstrate completeness.

## Work Sequencing (issue/PR split)
1. Range coder parity + vector harness (foundational).
2. CELT decode parity (energies/PVQ/stereo) then encode parity.
3. SILK decode/encode completion (PLC/FEC/DTX).
4. Multistream/surround and 120 ms/padding edge cases.
5. Robustness (fuzz + corrupted-frame cases) and documentation pass.
6. Profiling/alloc tuning after correctness is locked.
