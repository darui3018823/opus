# libopus Completion Audit and Priority Report

Date: 2026-06-20  
Repository: `github.com/darui3018823/opus`  
Workspace: `C:\Users\daruks\vsc\Programs\Go\opus`  
Audited branch: `dev/silk-burg-a2nlsf`  
Audited HEAD: `10d89b0 feat(silk): encode mono in-band FEC`  
Reference implementation: bundled libopus 1.6.1

## Purpose

This report records a code- and test-derived assessment of how close this
repository is to being a practical replacement for libopus. It also assigns
priority labels to the remaining API-contract, compatibility, feature,
performance, and documentation work.

For current implementation facts, always re-read
`docs/CURRENT_IMPLEMENTATION.md` before relying on this report. That file is the
repository's authoritative implementation snapshot. This report is an audit and
prioritization document, not a replacement for that snapshot.

## Post-Audit Implementation Update

Updated: 2026-06-20

The original findings below describe audited HEAD `10d89b0` and are retained
as historical evidence. They are not a current missing-feature list. Subsequent
commits completed the requested P3 phases:

- `7740c92 feat(api): add 24-bit PCM encode and decode`
- `e74c90c feat(celt): add phase inversion controls`
- `5841fe4 feat(multistream): add encoder and decoder APIs`
- `c6ea8e3 feat(surround): add standard channel mapping APIs`

Current status of the affected findings:

- P3.1 Multistream and surround: **core API complete**. RFC 6716
  self-delimited packet framing, up to 255 elementary streams, coupled/mono
  mapping, mapping value 255, standard Vorbis layouts through 7.1, discrete
  mapping family 255, LFE-aware rate allocation, and bidirectional libopus
  interoperability tests are present. Full libopus multistream CTL parity and
  surround energy-mask analysis remain follow-up work.
- P3.4 24-bit PCM APIs: **complete** for single-stream and multistream APIs,
  using signed 24-bit PCM values stored in `int32`.
- P3.5 Advanced controls: **partially complete**. Prediction disabling was
  already present; encoder and decoder CELT phase-inversion controls are now
  implemented. DRED, DNN, OSCE, QEXT, and other model/extension controls remain
  separate work.
- P3.2 Projection/ambisonics, P3.3 Ogg Opus, and P3.6 bit-exact CELT encoder
  parity remain open.

The original approximately 68% completion estimate and statements that
multistream, surround, short-frame encoding, repacketization, float32, FEC, or
24-bit APIs are absent are therefore stale. Use
`docs/CURRENT_IMPLEMENTATION.md` for the current code-derived status.

## Executive Summary

Estimated overall completion as a libopus replacement: **approximately 68%**.

| Area | Estimated completion | Assessment |
|---|---:|---|
| Single-stream decoder core | 93% | High confidence and already useful |
| CELT encoder | 78% | Standards-compliant output, incomplete libopus parity |
| SILK/hybrid encoder | 58% | Functional but intentionally limited routing and control |
| Public libopus-compatible API surface | 45% | Major APIs and CTLs remain absent |
| Tests and robustness | 88% | Strong test suite, reference checks, race and fuzz coverage |
| Runtime efficiency | 70% | Real-time capable, but allocation-heavy |

The repository is already a credible Pure Go Opus implementation for:

- standards-compliant single-stream decoding;
- CELT-oriented general audio encoding;
- limited SILK-only speech encoding;
- initial hybrid speech encoding;
- CGO-free normal builds.

It is not yet a drop-in or feature-complete replacement for libopus because
important API contracts and operational features are missing:

- real packet FEC extraction in the public decoder;
- 2.5, 5, and 10 ms public encoding;
- full SILK/hybrid mode and rate-control coverage;
- multistream, surround, projection, and repacketizer APIs;
- much of the encoder/decoder CTL surface;
- float32 and 24-bit public APIs;
- libopus-equivalent defaults and argument behavior.

The immediate engineering priority should be:

1. correct the contracts of APIs that already exist;
2. expose missing core single-stream APIs whose implementation is already
   present or straightforward;
3. implement real FEC decode and short-frame encode;
4. broaden encoder mode/rate behavior;
5. add ecosystem-level APIs such as multistream and repacketization.

Do not prioritize broad API expansion ahead of correcting existing misleading
or incompatible behavior.

## Evidence Collected

The audit began by reading `docs/CURRENT_IMPLEMENTATION.md`, as required by
`AGENTS.md`, and then checked its claims against code and executable tests.

### Commands executed successfully

```text
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go test -count=1 -tags opusref ./...
go test -count=1 -run TestOfficialVectors -v .
go test -run '^$' -fuzz '^FuzzDecode$' -fuzztime=15s .
go test -run '^$' -fuzz '^FuzzDecodeFloat$' -fuzztime=15s .
go test -run '^$' -bench 'Benchmark(Encode|Decode)$' -benchmem -benchtime=100x .
```

### Verification results

- `go vet ./...`: pass.
- Normal package tests: pass.
- Race detector: pass.
- `opusref` build and libopus 1.6.1 cross-checks: pass.
- Official RFC 8251 vectors: 12/12 pass using actual test-vector data.
- Decoder fuzzing: approximately 63,000 executions in the local short run,
  with no panic or failure.
- Float decoder fuzzing: approximately 67,000 executions in the local short
  run, with no panic or failure.
- Package statement coverage reported during the audit:
  - root package: 87.4%;
  - `internal/celt`: 83.3%;
  - `internal/dsp`: 81.4%;
  - `internal/entcode`: 79.2%;
  - `internal/resampler`: 88.4%;
  - `internal/silk`: 81.2%.

The repository also has CI workflows for tests, race detection, benchmarks, and
nightly fuzzing on amd64 and arm64. The opusref workflow validates decoder
vectors and selected encoder behavior against libopus.

### Local benchmark result

Environment:

- Windows amd64;
- Intel Core i7-11700;
- 20 ms public encode/decode benchmark.

Results:

| Operation | Time | Allocated bytes | Allocations |
|---|---:|---:|---:|
| Encode | approximately 1.76 ms/op | approximately 458 KB/op | 48 allocs/op |
| Decode | approximately 2.17 ms/op | approximately 422 KB/op | 47 allocs/op |

This is comfortably faster than the 20 ms real-time deadline for a single
stream on the measured machine. However, the allocation volume is high enough
to matter for servers, many simultaneous streams, mobile devices, or
latency-sensitive applications.

## Detailed Completion Assessment

## Decoder

### Implemented and well verified

- Mono and stereo output.
- Output sample rates 8, 12, 16, 24, and 48 kHz.
- SILK-only decoding.
- CELT-only decoding.
- Hybrid SILK+CELT reconstruction.
- CELT bandwidth and frame-duration configurations.
- Opus packet count codes 0, 1, 2, and 3.
- SILK 10/20/40/60 ms configurations.
- CELT 2.5/5/10/20 ms decoding.
- Packets up to the Opus 120 ms duration limit.
- Mode transitions and CELT redundancy handling.
- Mono/stereo packet-to-output channel conversion.
- State reset.
- Last decoded packet duration.
- Official RFC 8251 vector compatibility.
- Separate libopus decoder comparison.

### Decoder gaps

#### Public `DecodeFEC` is not FEC decode

`Decoder.DecodeFEC` currently ignores the supplied packet data and invokes CELT
PLC from the fullband 20 ms decoder. It therefore does not extract SILK LBRR
data from the following packet.

This is the most serious public API contract mismatch. The method name promises
packet FEC, while the implementation provides a fixed PLC fallback.

Consequences:

- applications cannot recover the previous lost packet using locally encoded
  mono SILK LBRR;
- callers may believe packet FEC is being used when it is not;
- PLC duration/mode does not necessarily match the lost packet;
- the method conflates two different libopus decoder operations.

Target design:

- add an explicit `DecodePLC(pcm, frameSize)` or equivalent API;
- move concealment behavior to that API;
- make `DecodeFEC` perform actual LBRR extraction;
- until real extraction exists, returning `ErrUnimplemented` is more truthful
  than silently performing PLC.

#### Buffer-too-small state semantics need correction

`Decoder.Decode` currently calls `DecodeFloat` before checking whether the
destination `[]int16` is large enough. If the buffer is too small, it returns
`ErrBufferTooSmall` only after decoder state has advanced.

That means a caller cannot safely resize the buffer and retry the same packet:
the second decode runs from a different codec state.

This is a behavioral correctness issue, not merely an API convenience issue.

Possible fixes:

- inspect packet duration and required output size before decoding;
- provide a packet-duration query and require callers to size the buffer;
- decode transactionally using a state snapshot and restore on buffer failure.

Preflight sizing is preferable because it avoids expensive state copies.

#### Missing decoder APIs and CTLs

The public decoder lacks:

- `DecodeFloat32`;
- explicit PLC with caller-selected duration;
- sample-rate getter;
- final-range getter;
- pitch getter;
- gain setter/getter;
- last-packet-duration CTL-style equivalent;
- packet inspection helpers commonly used before decode;
- decoder-side phase inversion control and newer extension controls.

These are not all equally urgent. Real FEC and explicit PLC are core; advanced
CTLs are secondary.

## Encoder

### Implemented and verified

- Mono and stereo input.
- Input sample rates 8, 12, 16, 24, and 48 kHz.
- CELT encoding with:
  - MDCT and short-block transient handling;
  - TF analysis;
  - PVQ;
  - allocation shaping;
  - stereo/intensity decisions;
  - configurable coded bandwidth;
  - signal-driven bandwidth narrowing;
  - CBR, VBR, and constrained VBR modes;
  - DTX;
  - multi-frame packetization;
  - packet padding.
- Limited SILK-only speech path:
  - low-bitrate VOIP or voice-hinted input;
  - mono and stereo;
  - native 8/12/16 kHz;
  - 24/48 kHz input downsampled to 16 kHz wideband SILK;
  - mono in-band FEC/LBRR generation.
- Initial hybrid speech path:
  - high-bitrate 24/48 kHz voice;
  - SWB/FB configurations;
  - mono and selected stereo cases.
- CELT/SILK/hybrid transition redundancy in both directions.
- libopus 1.6.1 can decode generated CELT, SILK-only, hybrid, transition, and
  selected FEC streams covered by the reference tests.

### Encoder gaps

#### No public 2.5, 5, or 10 ms encoding

The public encoder only accepts exact 20 ms multiples from 20 through 120 ms.
This excludes valid Opus durations 2.5, 5, and 10 ms.

This particularly weakens `ApplicationRestrictedLowDelay`: setting that
application does not make the public encoder capable of libopus-style short
latency packets.

The public constants already expose `FrameSize2_5ms`, `FrameSize5ms`, and
`FrameSize10ms`, which makes the mismatch more visible.

#### SILK/hybrid selection is intentionally narrow

The encoder does not implement libopus-equivalent continuous selection among
SILK-only, hybrid, and CELT-only modes over the full bitrate, sample-rate,
application, signal, channel, and packet-loss matrix.

Current routing is useful but heuristic and constrained:

- SILK-only is selected primarily for low-bitrate voice intent;
- hybrid is selected for high-bitrate 24/48 kHz voice intent;
- general audio is predominantly CELT;
- rate-control behavior is not equivalent to libopus;
- stereo and hybrid LBRR are absent.

Generated packets are standard-compliant. The remaining issue is encoder
coverage, quality, and policy parity, not basic decodability.

#### Encoder bit-exactness

The CELT encoder is not verified as bit-exact with libopus. Bit-exact encoder
output is not required for Opus interoperability, and it should not be treated
as a near-term release blocker.

More important goals are:

- compliant packets;
- stable quality;
- correct rate and mode behavior;
- bounded CPU and memory;
- correct FEC/DTX/transition behavior.

Bit-exact encoding is a low-priority research or validation goal unless a
specific downstream requirement demands it.

## Public API Compatibility

### Existing API surface

The current API is small and understandable, but it is not a Go binding-shaped
equivalent of libopus. It offers constructors, slice-oriented encode/decode,
selected encoder controls, reset, and last-duration access.

### Missing major API families

- Multistream encoder and decoder.
- Surround encoder.
- Projection/ambisonics APIs.
- Repacketizer.
- Packet pad/unpad helpers.
- Public packet parsing/inspection API.
- Ogg Opus container support.
- Float32 encode/decode.
- 24-bit PCM encode/decode.
- Most encoder and decoder CTLs.

Ogg support should probably live in a separate package or repository layer
rather than expanding the codec core directly.

### Existing behavior inconsistent with libopus or public constants

#### Application validation

`NewEncoder` does not reject unsupported application values.
`SetApplication` accepts any integer and does not return an error.

libopus validates applications and restricts changing application after
encoding has begun. A compatible Go API should validate values and document
whether runtime changes are supported.

Changing `SetApplication` to return `error` is a breaking API change. It should
be planned explicitly rather than silently changed.

#### Bitrate constants are not accepted

The package exports:

- `BitrateAuto`;
- `BitrateMax`;

but `SetBitrate` accepts only 6000 through 510000. The constants therefore
advertise unsupported behavior.

Either implement the constants or remove/deprecate them. Implementing them is
preferred for libopus compatibility.

#### Defaults differ from libopus

Current encoder defaults:

- 64,000 bit/s;
- complexity 5;
- CBR.

libopus defaults are conceptually:

- automatic bitrate;
- complexity 9;
- VBR enabled.

Changing defaults affects output size, quality, and existing users. Treat this
as an intentional compatibility change, likely requiring a major-version or
clearly documented migration.

An alternative is to preserve legacy constructor behavior and add a
libopus-compatible constructor/options profile.

#### Version constants are stale

`constants.go` reports version `0.1.0`, while repository releases include
`v1.0.0`, `v1.1.0`, and `v1.1.1`.

Version metadata should have a single source of truth or be generated during
release.

#### Frame and packet constants are inconsistent

- `MaxFrameSize` is 2880 samples at 48 kHz, representing 60 ms.
- The public encoder and decoder now support packets up to 120 ms.
- At 48 kHz, 120 ms is 5760 samples per channel.
- `MaxPacketSize` is 1500 bytes, while a single Opus frame payload is bounded
  by 1275 bytes and API-level maximum packet sizing may depend on framing and
  intended use.

These constants need precise semantics, corrected values, or deprecation.

#### Error values are inconsistently used

The package exports several sentinel errors, but many argument failures return
new string errors without wrapping the matching sentinel:

- unsupported sample rate;
- unsupported channels;
- bad application;
- invalid bitrate;
- invalid complexity;
- invalid bandwidth.

Public errors should be consistently detectable with `errors.Is`.

Some exported errors appear unused and may create a false impression that
corresponding behavior is standardized.

## Performance and Operational Assessment

### Strengths

- No CGO dependency in normal builds.
- Single-stream encode/decode is faster than real time on the measured desktop.
- Race tests pass.
- Decoder fuzzing exists and is automated.
- Tests cover amd64 and arm64 in CI.

### Risks

- Approximately 0.42 to 0.46 MB allocated per 20 ms operation.
- Approximately 47 to 48 allocations per operation.
- Decoder construction pre-creates a large matrix of CELT decoders and SILK
  decoder/resampler variants.
- Slice conversion and append-heavy paths allocate repeatedly.
- High stream counts may cause substantial GC and memory pressure.

Recommended performance work:

- establish stable benchmark baselines by mode, rate, channels, and duration;
- use buffer reuse for intermediate PCM and spectral data;
- avoid rebuilding temporary slices on every frame;
- consider lazy decoder creation where it does not complicate state transfer;
- add `AllocsPerRun` regression guards for core encode/decode paths;
- benchmark long-lived streams, not only isolated calls.

Performance optimization should follow API-contract corrections unless a
specific production deployment is currently blocked.

## Documentation Assessment

`docs/CURRENT_IMPLEMENTATION.md` is the best current status document and was
substantially consistent with the audited code.

`README_ja.md` contains stale claims, including statements that hybrid encoding
is not implemented and that the SILK encoder is mono-only. The current code has
limited stereo SILK and initial hybrid encoding.

Documentation should distinguish:

- standards compliance;
- official decoder vector compatibility;
- encoder output decodability by libopus;
- bit-exactness;
- quality equivalence;
- API equivalence;
- feature completeness.

Passing decoder vectors must not be presented as proof of complete libopus
replacement.

## Priority Label Definitions

Use these labels consistently:

- `priority/P0`: public contract violation, state corruption, silent semantic
  mismatch, or release metadata that materially misleads users.
- `priority/P1`: core single-stream compatibility or functionality required for
  common Opus use.
- `priority/P2`: valuable compatibility, performance, quality, or tooling work
  that does not block basic correct use.
- `priority/P3`: specialized, ecosystem-level, experimental, or research work.

Priority is separate from area and change-risk labels.

Recommended orthogonal labels:

- `area/api`
- `area/decoder`
- `area/encoder`
- `area/fec-plc`
- `area/packet`
- `area/performance`
- `area/docs`
- `area/multistream`
- `compat/libopus`
- `breaking-change`
- `behavior-change`
- `needs-design`
- `good-first-issue`

## P0: Correct Existing Public Contracts

### P0.1 Prevent decoder state advancement on `ErrBufferTooSmall`

Labels:

- `priority/P0`
- `area/decoder`
- `area/api`
- `compat/libopus`

Acceptance criteria:

- required output size is checked before mutating decoder state;
- retrying the same packet with a larger buffer produces the same output as a
  first successful decode on an equivalent decoder;
- regression test covers stateful consecutive packets, not only silence.

### P0.2 Separate PLC from FEC

Labels:

- `priority/P0`
- `area/fec-plc`
- `area/api`
- `breaking-change`
- `needs-design`

Acceptance criteria:

- a public PLC method has explicit duration semantics;
- `DecodeFEC` no longer silently performs CELT PLC;
- if FEC extraction is not implemented in the same change, `DecodeFEC` returns
  a clear sentinel error;
- README and current implementation snapshot describe exact behavior.

### P0.3 Validate encoder application values

Labels:

- `priority/P0`
- `area/api`
- `area/encoder`
- `compat/libopus`

Acceptance criteria:

- constructor rejects invalid application values with a sentinel-wrapped error;
- setter behavior is explicitly designed;
- invalid values cannot silently alter routing heuristics;
- tests cover all valid and representative invalid values.

Changing `SetApplication` to return an error is breaking and should carry the
`breaking-change` label.

### P0.4 Synchronize version metadata

Labels:

- `priority/P0`
- `area/api`
- `area/docs`

Acceptance criteria:

- public version matches the released module version;
- release process prevents manual drift;
- README and generated docs report the same version.

### P0.5 Correct or deprecate maximum-size constants

Labels:

- `priority/P0`
- `area/api`
- `area/packet`

Acceptance criteria:

- `MaxFrameSize` reflects 120 ms support or is replaced by a clearly named
  per-duration/per-rate helper;
- `MaxPacketSize` semantics are documented precisely;
- tests prevent future divergence between constants and accepted packet sizes.

## P1: Core Single-Stream Compatibility

### P1.1 Implement 2.5/5/10 ms public encoding

Labels:

- `priority/P1`
- `area/encoder`
- `compat/libopus`

Reasons:

- valid Opus frame durations;
- required for credible restricted-low-delay behavior;
- public constants already advertise these durations.

Acceptance criteria:

- mono/stereo CELT packets for 2.5, 5, and 10 ms;
- all supported input sample rates where valid;
- CBR/VBR, bandwidth, DTX, reset, and transition behavior covered;
- libopus decodes generated packets;
- packet duration helpers report correct values.

### P1.2 Implement actual mono SILK LBRR decode

Labels:

- `priority/P1`
- `area/decoder`
- `area/fec-plc`
- `compat/libopus`

Reasons:

- encoder already emits mono SILK LBRR;
- common VoIP loss-recovery feature;
- completes a currently asymmetric capability.

Acceptance criteria:

- recover dropped 20/40/60 ms mono SILK packets from the following packet;
- match expected duration and state behavior;
- cross-check against libopus `decode_fec=1`;
- preserve normal decode alignment after FEC decode.

### P1.3 Support `BitrateAuto` and `BitrateMax`

Labels:

- `priority/P1`
- `area/api`
- `area/encoder`
- `compat/libopus`

Acceptance criteria:

- exported constants are accepted;
- resulting rate policy is documented and tested;
- getters return meaningful values.

### P1.4 Decide and implement libopus-compatible defaults

Labels:

- `priority/P1`
- `area/api`
- `area/encoder`
- `behavior-change`
- `needs-design`

Decision required:

- change existing constructor defaults;
- add an options constructor or compatibility profile;
- defer to a major version.

Avoid changing defaults in an incidental patch.

### P1.5 Add float32 encode/decode

Labels:

- `priority/P1`
- `area/api`
- `area/encoder`
- `area/decoder`

Reasons:

- libopus float API uses float32;
- avoids conversion in common Go audio pipelines;
- current float64-only API is unusual for PCM interfaces.

### P1.6 Add public packet inspection helpers

Labels:

- `priority/P1`
- `area/api`
- `area/packet`

Candidate helpers:

- packet frame count;
- samples per frame;
- total packet samples/duration;
- bandwidth;
- channel count;
- mode/config.

These helpers also support preflight destination sizing and proper PLC/FEC
integration.

### P1.7 Standardize sentinel errors

Labels:

- `priority/P1`
- `area/api`
- `good-first-issue`

Acceptance criteria:

- public argument and packet errors wrap stable sentinels;
- `errors.Is` tests cover every exported sentinel intended for callers;
- unused or misleading errors are removed or documented.

## P2: Broader Compatibility and Production Quality

### P2.1 Expose common getters and CTLs

Labels:

- `priority/P2`
- `area/api`
- `compat/libopus`

Candidates:

- sample rate;
- final range;
- lookahead;
- pitch;
- gain;
- VBR constraint;
- maximum bandwidth;
- in-DTX state.

### P2.2 Force-channel, LSB-depth, gain, and prediction controls

Labels:

- `priority/P2`
- `area/api`
- `area/encoder`
- `compat/libopus`

### P2.3 Repacketizer and packet pad/unpad

Labels:

- `priority/P2`
- `area/api`
- `area/packet`

The repository already has internal packet splitting/packing logic, so a
carefully designed public repacketizer may reuse proven primitives.

### P2.4 Reduce allocations and memory traffic

Labels:

- `priority/P2`
- `area/performance`

Initial targets:

- materially reduce the roughly 420-460 KB per packet allocation;
- lower allocations per operation;
- preserve race safety and state correctness;
- add benchmark regression thresholds.

### P2.5 Stereo and hybrid LBRR

Labels:

- `priority/P2`
- `area/encoder`
- `area/decoder`
- `area/fec-plc`

### P2.6 Broaden SILK/hybrid mode and rate selection

Labels:

- `priority/P2`
- `area/encoder`
- `compat/libopus`

This should be split into measurable slices:

- mode boundary parity;
- bandwidth parity;
- channel decisions;
- VBR/CVBR rate behavior;
- packet-loss-driven FEC decisions;
- transition behavior;
- quality A/B thresholds.

### P2.7 Document concurrency and state ownership

Labels:

- `priority/P2`
- `area/api`
- `area/docs`

Document whether encoder/decoder instances are safe for concurrent use. The
expected answer is likely no without external synchronization, but it should be
explicit.

## P3: Ecosystem, Specialized, and Research Work

### P3.1 Multistream and surround

Status after audit: **core implementation complete** in `5841fe4` and
`c6ea8e3`; see the post-audit update above for remaining parity work.

Labels:

- `priority/P3`
- `area/multistream`
- `area/api`

Important for a complete libopus replacement, but not required for the core
single-stream release path.

### P3.2 Projection and ambisonics

Labels:

- `priority/P3`
- `area/multistream`
- `area/api`

### P3.3 Ogg Opus container support

Labels:

- `priority/P3`
- `area/api`

Prefer a separate package boundary. Codec and container concerns should remain
independent.

### P3.4 24-bit PCM APIs

Status after audit: **complete** in `7740c92`.

Labels:

- `priority/P3`
- `area/api`

### P3.5 Advanced controls

Status after audit: **phase inversion complete** in `e74c90c`; prediction
disabling was already implemented. Neural/model-dependent extension controls
remain open.

Labels:

- `priority/P3`
- `area/api`

Examples:

- phase inversion;
- prediction disabling;
- newer DRED, DNN, OSCE, QEXT, and extension controls.

### P3.6 Bit-exact CELT encoder parity

Labels:

- `priority/P3`
- `area/encoder`
- `compat/libopus`

Treat as research/validation unless required by a concrete integration.
Interoperability does not require identical encoder bitstreams.

## Recommended Work Sequence

### Phase A: Public contract repair

Keep this phase narrowly focused:

1. destination buffer preflight and decoder-state safety;
2. PLC/FEC API separation;
3. application validation;
4. version and maximum-size constants;
5. sentinel error consistency;
6. README and status-document synchronization.

This phase may contain breaking API decisions. Resolve them deliberately before
opening additional public surface.

### Phase B: Core missing single-stream behavior

1. public packet inspection;
2. explicit PLC;
3. real mono SILK LBRR decode;
4. 2.5/5/10 ms encoding;
5. `BitrateAuto`/`BitrateMax`;
6. float32 APIs.

### Phase C: Encoder parity and operational quality

1. libopus-like default/profile design;
2. broader SILK/hybrid routing;
3. stereo/hybrid FEC;
4. allocation reduction;
5. additional common CTLs;
6. real speech/music corpus quality and loss testing.

### Phase D: Full library ecosystem

1. repacketizer and packet operations;
2. multistream;
3. surround;
4. projection;
5. optional Ogg integration package;
6. specialized/modern controls.

## Release Readiness Interpretation

### Ready today

- Pure Go Opus decoder for normal single-stream playback and processing.
- CELT-focused encoding where generated packets are validated by libopus.
- Controlled use of limited SILK/hybrid speech encoding.
- Development and experimentation with Opus internals.

### Ready with explicit caveats

- VoIP experiments where packet FEC decode is handled by libopus or another
  receiver.
- Server-side encoding where allocation overhead has been measured and accepted.
- Restricted feature sets documented by the application.

### Not ready as a claim

- drop-in libopus replacement;
- complete WebRTC-grade codec behavior;
- complete low-delay encoder;
- full packet-loss recovery;
- multistream/surround codec stack;
- API-compatible libopus substitute.

## Definition of a Credible “libopus-Compatible Core” Milestone

A practical milestone before multistream work should require all of:

- all existing P0 contract issues resolved;
- 2.5/5/10/20/40/60/80/100/120 ms public encoding where Opus permits;
- real PLC and mono FEC APIs with correct state behavior;
- common packet inspection helpers;
- float32 APIs;
- supported `BitrateAuto` and `BitrateMax`;
- explicit and tested default behavior;
- official decoder vectors passing;
- libopus cross-decode tests passing across modes and durations;
- race tests and decoder fuzz tests passing;
- allocation baselines documented;
- no stale public constants or contradictory README status claims.

That milestone would still exclude multistream, surround, projection, and
bit-exact encoder output, but it would be a defensible high-quality
single-stream Opus library.

## Final Audit Judgment

The strongest part of the repository is the decoder: it has unusually good
evidence for a Pure Go implementation, including all official RFC 8251 vectors,
libopus comparison, race testing, and fuzzing.

The encoder is beyond a prototype because libopus decodes its output and the
repository contains substantial CELT, SILK, hybrid, transition, quality, and
rate-control work. Its remaining limitation is breadth and parity, especially
short frames, full SILK/hybrid policy, and FEC symmetry.

The highest-risk gap is now the public contract layer. Existing names,
constants, defaults, and error behavior occasionally promise more compatibility
than the implementation provides. Correcting those contracts should precede
opening a large number of new APIs.

The recommended immediate direction is therefore:

> Fix existing API semantics first, then expose and implement the missing
> single-stream core APIs, then broaden encoder parity, and only afterward
> pursue multistream and ecosystem completeness.
