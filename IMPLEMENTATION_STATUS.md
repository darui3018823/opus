# Opus Compliance & Action Plan

This document tracks gaps versus the Opus 1.3.1 specification, prioritizes implementation, and defines validation/compatibility work. The library must remain **Pure Go** (no cgo / external binaries).

> **Status update (2026-07-17):** the **decoder** is complete for the
> current public surface and verified — it passes all 12 official RFC 8251
> vectors (RMSE < 0.001) and matches the libopus 1.6.1 reference frame-by-frame
> (`TestCGORef`, `-tags opusref`). The **encoder** has a CELT quality pipeline
> plus a limited low-bitrate SILK-only speech path that emits standard Opus
> packets libopus can decode, including stereo and 24/48 kHz input downsampled
> to WB SILK. It also has an initial hybrid encode path for high-bitrate
> 24/48 kHz voice packets and SILK-only/hybrid LBRR FEC for mono and stereo.
> Public multistream/surround/projection APIs, packet extensions, repacketizer
> operations, Ogg Opus chained-stream reading, and per-link seeking are implemented.
> It is not bit-exact with libopus.
> The remaining work below is primarily encoder mode coverage/bit-exactness,
> PLC/FEC quality parity, and full libopus CTL/rate-control parity. See
> [docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md) for the
> authoritative snapshot.

## Coverage Snapshot
- ✅ RFC6716 framing, TOC parsing, resampler, entropy coder.
- ✅ **Decoder**: SILK / CELT / hybrid reconstruction; 12/12 official RFC 8251 vectors and libopus 1.6.1 parity. Official-vector automation is in-tree (`TestOfficialVectors`, `TestCGORef`).
- ✅ **Encoder**: CELT quality pipeline with transient handling, TF analysis, allocation shaping, stereo/intensity decisions, bandwidth detection, VBR/CVBR, DTX, and multi-frame packetization, plus limited SILK-only speech encode for low-bitrate voice, initial hybrid speech encode for high-bitrate 24/48 kHz voice, and mono/stereo SILK LBRR emission. Output is standard Opus but not bit-exact with libopus.
- ✅ **Packet and container APIs**: packet inspection, repacketizer/padding, packet extensions, multistream/surround, projection/Ambisonics, Ogg Opus reader/writer support, chained-stream reading, and per-link seeking are implemented.
- ✅ **Packet-loss handling**: public `DecodePLC` covers CELT-only, SILK-only, and hybrid streams; `DecodeFEC` recovers SILK-only/hybrid LBRR.
- ⚠️ Full libopus-equivalent mode/rate control, PLC/FEC bit-exactness, arbitrary projection matrix generation, multiplexed Ogg stream demux, and full multistream/surround CTL/psychoacoustic parity remain incomplete.

## Spec Gaps (prioritized)
1. **SILK/hybrid encoder modes**: broaden the current limited SILK-only and hybrid paths toward fuller libopus mode coverage.
2. **Encoder bit-exactness/quality parity**: close remaining gaps versus libopus encoder decisions where useful.
3. **Packet loss concealment / FEC**: improve remaining PLC/FEC quality and bit-exactness edge cases.
4. **Multistream/Surround**: fill in remaining libopus CTL-style behavior and surround analysis parity beyond the implemented core mapping/packet support.
5. **Container support**: multiplexed-stream demux beyond the implemented chained-stream reader and per-link seek APIs.
6. **Compatibility controls**: fuller CTL-style behavior for bitrate/VBR/application/signal options.
7. **Robust malformed-input coverage**: invalid TOCs, malformed size fields, huge padding, and edge-case packet duration validation.

## Validation & Test Automation Plan
- **Official vectors**: Keep replaying the RFC 8251 reference test vectors; assert decoder parity per sample rate (8/12/16/24/48 kHz), mono/stereo, and frame sizes 2.5–60 ms.
- **Matrix coverage**: Table-driven tests for all `(sample rate, channels, application, frame size)` combinations; include hybrid vs CELT-only vs SILK-only paths.
- **Compatibility assertions**: Golden outputs vs libopus, `opusfile`, and Go libs (e.g., `layeh.com/gopus`) for selected vectors; fail on API/behavior divergence.
- **Robustness**: Fuzzers on decoder, packet-extension, multistream, repacketizer/padding, Ogg, and future parser surfaces (invalid TOCs, truncated packets, huge padding, NaNs); keep timeouts and allocation caps.
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
1. Broader SILK/hybrid encoder mode coverage.
2. Remaining SILK/hybrid PLC/FEC quality and continuity edge cases.
3. Remaining multistream/surround/Ogg parity beyond the implemented core APIs.
4. Encoder parity/quality refinements against libopus.
5. Robustness (fuzz + corrupted-frame cases) and documentation pass.
6. Profiling/alloc tuning after correctness is locked.
