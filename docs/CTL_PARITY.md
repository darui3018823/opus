# CTL and Helper Parity Matrix

Last reviewed: 2026-07-21

Baseline: libopus 1.6.1 public headers:

- `include/opus_defines.h`
- `include/opus.h`
- `include/opus_multistream.h`
- `include/opus_projection.h`

The matrix separates three questions that were previously collapsed into one
status: whether a public Go surface exists, whether its behavior is equivalent,
and what evidence supports that assessment. `Present` does not by itself mean
semantic parity. Extension processing excluded by `docs/LIBOPUS_SCOPE.md` is
marked `Out of scope` rather than treated as core parity.

## Generic CTLs

| libopus CTL | Surface | Semantics | Evidence / note |
|---|---:|---:|---|
| `OPUS_RESET_STATE` | Present | Equivalent | Encoder/decoder/multistream/surround/projection reset methods; reset and sequence tests cover retained configuration versus cleared history |
| `OPUS_GET_FINAL_RANGE` | Present | Equivalent | `FinalRange` getters; codec, PLC/FEC, multistream, and predictive digest tests |
| `OPUS_GET_SAMPLE_RATE` | Present | Equivalent | `SampleRate` getters; constructor/control tests |
| `OPUS_GET_BANDWIDTH` | Present | Equivalent | Encoder and decoder `Bandwidth`/`GetBandwidth`; `packet_test.go` and bandwidth tests |
| `OPUS_SET_PHASE_INVERSION_DISABLED` | Present | Equivalent | `SetPhaseInversionDisabled`; `phase_inversion_test.go` |
| `OPUS_GET_PHASE_INVERSION_DISABLED` | Present | Equivalent | `PhaseInversionDisabled`; `phase_inversion_test.go` |
| `OPUS_GET_IN_DTX` | Present | Equivalent | `InDTX`; DTX and silence-carrier tests |

## Encoder CTLs

| libopus CTL | Surface | Semantics | Evidence / note |
|---|---:|---:|---|
| `OPUS_SET_APPLICATION` / `OPUS_GET_APPLICATION` | Present | Partial | `SetApplication`, `Application`; state tests pass, but mode policy is narrower than libopus |
| `OPUS_SET_BITRATE` / `OPUS_GET_BITRATE` | Present | Partial | `SetBitrate`, `Bitrate`, `EffectiveBitrate`; bitrate boundary tests cover auto/max, high-rate clamp, and per-frame limits. Positive requests below 6000 bit/s are unsupported |
| `OPUS_SET_MAX_BANDWIDTH` / `OPUS_GET_MAX_BANDWIDTH` | Present | Equivalent | `SetMaxBandwidth`, `MaxBandwidth`; bandwidth selection and round-trip tests |
| `OPUS_SET_BANDWIDTH` / `OPUS_GET_BANDWIDTH` | Present | Equivalent | `SetBandwidth`, `Bandwidth`, `GetBandwidth`; bandwidth selection and libopus reference tests |
| `OPUS_SET_COMPLEXITY` / `OPUS_GET_COMPLEXITY` | Present | Equivalent | `SetComplexity`, `Complexity`; control tests cover accepted range and getter state |
| `OPUS_SET_INBAND_FEC` / `OPUS_GET_INBAND_FEC` | Present | Equivalent | `SetInbandFEC`, `InbandFEC`; SILK/hybrid LBRR and silence-carrier interoperability tests |
| `OPUS_SET_PACKET_LOSS_PERC` / `OPUS_GET_PACKET_LOSS_PERC` | Present | Partial | `SetPacketLossPerc`, `PacketLossPerc`; codec tuning is wired, but the void Go setter clamps out-of-range values instead of returning libopus's `OPUS_BAD_ARG` |
| `OPUS_SET_DTX` / `OPUS_GET_DTX` | Present | Equivalent | `SetDTX`, `DTX`; DTX, digital-silence, and pending-LBRR tests |
| `OPUS_SET_VBR` / `OPUS_GET_VBR` | Present | Partial | `SetVBR`, `VBR`; CELT and non-redundant hybrid sizing are wired, while broader SILK/hybrid policy is narrower |
| `OPUS_SET_VBR_CONSTRAINT` / `OPUS_GET_VBR_CONSTRAINT` | Present | Partial | `SetVBRConstraint`, `VBRConstraint`; corpus byte-total evidence covers CELT, not full predictive CVBR policy |
| `OPUS_SET_FORCE_CHANNELS` / `OPUS_GET_FORCE_CHANNELS` | Present | Equivalent | `SetForceChannels`, `ForceChannels`; forced-mono packet and state-propagation tests |
| `OPUS_SET_SIGNAL` / `OPUS_GET_SIGNAL` | Present | Partial | `SetSignalType`, `SignalType`; request state is independent from Application, but mode and quality policy is narrower |
| `OPUS_GET_LOOKAHEAD` | Present | Partial | `Lookahead` currently returns `sampleRate/400` for every application and selected mode; libopus delay is application/configuration dependent |
| `OPUS_SET_LSB_DEPTH` / `OPUS_GET_LSB_DEPTH` | Present | Partial | `SetLSBDepth`, `LSBDepth`; range, storage, and forced-mono propagation are tested, but the value does not yet affect codec decisions |
| `OPUS_SET_EXPERT_FRAME_DURATION` / `OPUS_GET_EXPERT_FRAME_DURATION` | Present | Equivalent | `SetExpertFrameDuration`, `ExpertFrameDuration`; all-rate/PCM/multistream tests and `opusref` semantic comparisons |
| `OPUS_SET_PREDICTION_DISABLED` / `OPUS_GET_PREDICTION_DISABLED` | Present | Equivalent | `SetPredictionDisabled`, `PredictionDisabled`; routing and transition tests |
| `OPUS_SET_DRED_DURATION` / `OPUS_GET_DRED_DURATION` | Absent | Out of scope | DRED payload transport is opaque; neural recovery is outside the compatibility claim |
| `OPUS_SET_DNN_BLOB` | Absent | Out of scope | DNN blob loading is outside the compatibility claim |
| `OPUS_SET_OSCE_BWE` / `OPUS_GET_OSCE_BWE` | Absent | Out of scope | OSCE bandwidth-extension DSP is outside the compatibility claim |
| `OPUS_SET_QEXT` / `OPUS_GET_QEXT` | Absent | Out of scope | QEXT payloads are transported opaquely; codec/DSP processing is outside scope |
| `OPUS_SET_IGNORE_EXTENSIONS` / `OPUS_GET_IGNORE_EXTENSIONS` | Absent | Out of scope | Packet extensions are parsed/generated explicitly; decoder extension-policy CTLs are outside scope |

## Decoder CTLs

| libopus CTL | Surface | Semantics | Evidence / note |
|---|---:|---:|---|
| `OPUS_SET_GAIN` / `OPUS_GET_GAIN` | Present | Equivalent | `SetGain`, `Gain`; range and decoded-energy tests |
| `OPUS_GET_LAST_PACKET_DURATION` | Present | Equivalent | `GetLastPacketDuration`; packet/reset tests |
| `OPUS_GET_PITCH` | Present | Equivalent | `Pitch`; SILK decode and reset tests, reported at output sample rate |
| `OPUS_GET_FINAL_RANGE` | Present | Equivalent | `FinalRange`; PLC, SILK/hybrid FEC, multistream XOR, and entropy-range tests |
| `OPUS_GET_BANDWIDTH` | Present | Equivalent | `Bandwidth`, `GetBandwidth`; getter tests include pre-decode and reset `BandwidthAuto` |
| `OPUS_SET_PHASE_INVERSION_DISABLED` / `OPUS_GET_PHASE_INVERSION_DISABLED` | Present | Equivalent | Setter/getter and stereo round-trip tests |
| DRED decoder CTLs | Absent | Out of scope | DRED neural PLC/FEC is outside the compatibility claim |

## Decoder Loss-Recovery Calls

| libopus behavior | Surface | Semantics | Evidence / note |
|---|---:|---:|---|
| PLC with explicit missing duration | Present | Equivalent | PCM-typed `DecodePLC` variants; duration grid, initial zero, mode transition, and transactional tests |
| FEC with explicit missing duration | Present | Equivalent | PCM-typed FEC variants; packed first-frame, PLC-prefix, unavailable-FEC, and libopus LBRR tests |
| Multistream/surround PLC and FEC | Present | Equivalent | Matching multistream methods inherited by surround; atomic multi-decoder recovery tests |
| Legacy inferred-duration FEC | Present | Partial | `DecodeFEC` preserves the v1 inferred-duration and CELT-only error contract; explicit-duration methods provide the libopus-like contract |

## Packet, Repacketizer, and PCM Helpers

| libopus function | Surface | Semantics | Evidence / note |
|---|---:|---:|---|
| `opus_packet_get_bandwidth` | Present | Partial | `PacketGetBandwidth`; validates complete framing and can reject input that libopus's shallow helper accepts |
| `opus_packet_get_samples_per_frame` | Present | Partial | `PacketGetSamplesPerFrame`; complete-framing validation is intentionally stricter |
| `opus_packet_get_nb_channels` | Present | Partial | `PacketGetNumChannels`; complete-framing validation is intentionally stricter |
| `opus_packet_get_nb_frames` | Present | Partial | `PacketGetNumFrames`; complete-framing validation is intentionally stricter |
| `opus_packet_get_nb_samples` / `opus_decoder_get_nb_samples` | Present | Partial | `PacketGetNumSamples`; full framing and 120 ms duration validation are stricter |
| `opus_packet_has_lbrr` | Present | Equivalent | `PacketHasLBRR`; packed-packet tests inspect the recoverable first frame |
| `opus_pcm_soft_clip` | Present | Equivalent | `SoftClipFloat32`; argument, continuity, and bounded-output tests |
| `opus_repacketizer_*` | Present | Equivalent | Repacketizer API; round-trip, mismatch, padding, and 120 ms limit tests |
| `opus_packet_pad` / `opus_packet_unpad` | Present | Equivalent | Packet padding round-trip and canonical unpadding tests |
| `opus_multistream_packet_pad` / `opus_multistream_packet_unpad` | Present | Equivalent | Multistream padding/unpadding tests |

## Multistream CTLs

| libopus CTL | Surface | Semantics | Evidence / note |
|---|---:|---:|---|
| `OPUS_MULTISTREAM_GET_ENCODER_STATE` | Present | Equivalent | `StreamEncoder`; child-state and interoperability tests |
| `OPUS_MULTISTREAM_GET_DECODER_STATE` | Present | Equivalent | `StreamDecoder`; child-state and transactional recovery tests |
| Generic encoder/decoder CTLs on multistream states | Partial | Partial | Aggregate bitrate, expert duration, reset, final range, and per-stream access exist; not every CTL has an aggregate convenience wrapper |

## Projection CTLs

| libopus CTL | Surface | Semantics | Evidence / note |
|---|---:|---:|---|
| `OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN` | Present | Equivalent | `MappingMatrix.Gain`; matrix validation and projection interoperability tests |
| `OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE` | Present | Equivalent | Matrix dimensions and serialized byte size; construction/serialization tests |
| `OPUS_PROJECTION_GET_DEMIXING_MATRIX` | Present | Equivalent | `MappingMatrix.Bytes` and projection decoder construction; round-trip and libopus tests |
| Generic/multistream CTLs on projection states | Partial | Partial | Projection exposes expert duration and wrapped multistream behavior; aggregate convenience remains incomplete |
