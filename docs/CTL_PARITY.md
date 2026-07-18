# CTL and Helper Parity Matrix

Last reviewed: 2026-07-19

Baseline: libopus 1.6.1 public headers:

- `include/opus_defines.h`
- `include/opus.h`
- `include/opus_multistream.h`
- `include/opus_projection.h`

Status values:

- `Supported`: public Go API exposes equivalent behavior.
- `Partial`: API exists, but behavior is intentionally narrower than libopus.
- `Unsupported`: no public equivalent yet.
- `Out of scope`: the package intentionally transports or omits this extension.

## Generic CTLs

| libopus CTL | Status | Go API / note |
|---|---:|---|
| `OPUS_RESET_STATE` | Supported | `(*Encoder).Reset`, `(*Decoder).Reset`, multistream/surround/projection reset methods |
| `OPUS_GET_FINAL_RANGE` | Supported | `FinalRange` getters |
| `OPUS_GET_SAMPLE_RATE` | Supported | `SampleRate` getters |
| `OPUS_GET_BANDWIDTH` | Supported | `(*Encoder).Bandwidth`, `GetBandwidth`; `(*Decoder).Bandwidth`, `GetBandwidth` |
| `OPUS_SET_PHASE_INVERSION_DISABLED` | Supported | `SetPhaseInversionDisabled` |
| `OPUS_GET_PHASE_INVERSION_DISABLED` | Supported | `PhaseInversionDisabled` |
| `OPUS_GET_IN_DTX` | Supported | `(*Encoder).InDTX` |

## Encoder CTLs

| libopus CTL | Status | Go API / note |
|---|---:|---|
| `OPUS_SET_APPLICATION` / `OPUS_GET_APPLICATION` | Partial | `SetApplication`, `Application`; API/state is exposed, but mode policy is narrower than libopus |
| `OPUS_SET_BITRATE` / `OPUS_GET_BITRATE` | Partial | `SetBitrate`, `Bitrate`, `EffectiveBitrate`; numeric requests >=6000 bit/s plus auto/max sentinels are accepted. High requests clamp to 750000 bit/s per channel at the CTL boundary and to the 1275-byte limit per constituent frame when applied. Positive requests below 6000 remain unsupported because the predictive encoder cannot yet reproduce libopus' compact low-rate packets |
| `OPUS_SET_MAX_BANDWIDTH` / `OPUS_GET_MAX_BANDWIDTH` | Supported | `SetMaxBandwidth`, `MaxBandwidth` |
| `OPUS_SET_BANDWIDTH` / `OPUS_GET_BANDWIDTH` | Supported | `SetBandwidth`, `Bandwidth`, `GetBandwidth` |
| `OPUS_SET_COMPLEXITY` / `OPUS_GET_COMPLEXITY` | Supported | `SetComplexity`, `Complexity` |
| `OPUS_SET_INBAND_FEC` / `OPUS_GET_INBAND_FEC` | Supported | `SetInbandFEC`, `InbandFEC`; LBRR is SILK/hybrid only, and pending LBRR is carried through a following digital-silence packet |
| `OPUS_SET_PACKET_LOSS_PERC` / `OPUS_GET_PACKET_LOSS_PERC` | Supported | `SetPacketLossPerc`, `PacketLossPerc` |
| `OPUS_SET_DTX` / `OPUS_GET_DTX` | Supported | `SetDTX`, `DTX` |
| `OPUS_SET_VBR` / `OPUS_GET_VBR` | Partial | `SetVBR`, `VBR`; CELT and non-redundant hybrid sizing are wired, while broader SILK/hybrid policy is narrower than libopus |
| `OPUS_SET_VBR_CONSTRAINT` / `OPUS_GET_VBR_CONSTRAINT` | Partial | `SetVBRConstraint`, `VBRConstraint`; broader SILK/hybrid CVBR policy is narrower than libopus |
| `OPUS_SET_FORCE_CHANNELS` / `OPUS_GET_FORCE_CHANNELS` | Supported | `SetForceChannels`, `ForceChannels` |
| `OPUS_SET_SIGNAL` / `OPUS_GET_SIGNAL` | Partial | `SetSignalType`, `SignalType`; request state defaults to Auto and remains independent from Application, but mode and quality policy is narrower than libopus |
| `OPUS_GET_LOOKAHEAD` | Supported | `Lookahead` |
| `OPUS_SET_LSB_DEPTH` / `OPUS_GET_LSB_DEPTH` | Supported | `SetLSBDepth`, `LSBDepth` |
| `OPUS_SET_EXPERT_FRAME_DURATION` / `OPUS_GET_EXPERT_FRAME_DURATION` | Supported | `SetExpertFrameDuration`, `ExpertFrameDuration`; fixed durations treat Encode's `frameSize` as available samples and consume the selected prefix |
| `OPUS_SET_PREDICTION_DISABLED` / `OPUS_GET_PREDICTION_DISABLED` | Supported | `SetPredictionDisabled`, `PredictionDisabled` |
| `OPUS_SET_DRED_DURATION` / `OPUS_GET_DRED_DURATION` | Out of scope | DRED payload transport is opaque; neural recovery is not implemented |
| `OPUS_SET_DNN_BLOB` | Out of scope | DNN blob loading is not part of the pure-Go codec |
| `OPUS_SET_OSCE_BWE` / `OPUS_GET_OSCE_BWE` | Out of scope | OSCE bandwidth extension DSP is not implemented |
| `OPUS_SET_QEXT` / `OPUS_GET_QEXT` | Out of scope | QEXT payloads are transported opaquely |
| `OPUS_SET_IGNORE_EXTENSIONS` / `OPUS_GET_IGNORE_EXTENSIONS` | Unsupported | Packet extensions are parsed/generated explicitly; decoder ignore policy is not a CTL |

## Decoder CTLs

| libopus CTL | Status | Go API / note |
|---|---:|---|
| `OPUS_SET_GAIN` / `OPUS_GET_GAIN` | Supported | `SetGain`, `Gain` |
| `OPUS_GET_LAST_PACKET_DURATION` | Supported | `GetLastPacketDuration` |
| `OPUS_GET_PITCH` | Supported | `Pitch` |
| `OPUS_GET_FINAL_RANGE` | Supported | `FinalRange`; PLC reports zero, SILK FEC reports the recovered first-frame entropy range, hybrid FEC reports the CELT PLC RNG state, and multistream/surround XOR elementary ranges |
| `OPUS_GET_BANDWIDTH` | Supported | `Bandwidth`, `GetBandwidth`; returns `BandwidthAuto` before first successful decode or after reset |
| `OPUS_SET_PHASE_INVERSION_DISABLED` / `OPUS_GET_PHASE_INVERSION_DISABLED` | Supported | `SetPhaseInversionDisabled`, `PhaseInversionDisabled` |
| DRED decoder CTLs | Out of scope | DRED neural PLC/FEC is not implemented |

## Decoder Loss-Recovery Calls

| libopus behavior | Status | Go API / note |
|---|---:|---|
| PLC with explicit missing duration | Supported | `DecodePLC`, `DecodePLC24`, `DecodePLCFloat`, `DecodePLCFloat32`; accepts every positive 2.5 ms multiple through 120 ms and returns zero concealment before the first packet |
| FEC with explicit missing duration | Supported | `DecodeFECWithDuration`, `DecodeFEC24`, `DecodeFECFloat`, `DecodeFECFloat32`; packed carriers use only their first Opus frame and PLC fills any prefix or unavailable-FEC case |
| Multistream/surround PLC and FEC | Supported | Matching methods on `MultistreamDecoder`; `SurroundDecoder` inherits them. Recovery uses one shared missing duration and commits elementary states atomically |
| Legacy inferred-duration FEC | Supported | `DecodeFEC` retains the v1 signature, infers the carrier's total duration, and retains the CELT-only error contract |

## Packet, Repacketizer, and PCM Helpers

| libopus function | Status | Go API / note |
|---|---:|---|
| `opus_packet_get_bandwidth` | Supported | `PacketGetBandwidth` |
| `opus_packet_get_samples_per_frame` | Supported | `PacketGetSamplesPerFrame` |
| `opus_packet_get_nb_channels` | Supported | `PacketGetNumChannels` |
| `opus_packet_get_nb_frames` | Supported | `PacketGetNumFrames` |
| `opus_packet_get_nb_samples` / `opus_decoder_get_nb_samples` | Supported | `PacketGetNumSamples` |
| `opus_packet_has_lbrr` | Supported | `PacketHasLBRR`; packed packets inspect only the first Opus frame, matching the frame recoverable by libopus FEC |
| `opus_pcm_soft_clip` | Supported | `SoftClipFloat32` |
| `opus_repacketizer_*` | Supported | `NewRepacketizer`, `Cat`, `NumFrames`, `Out`, `OutRange`, `Reset` |
| `opus_packet_pad` / `opus_packet_unpad` | Supported | `PacketPad`, `PacketUnpad` |
| `opus_multistream_packet_pad` / `opus_multistream_packet_unpad` | Supported | `MultistreamPacketPad`, `MultistreamPacketUnpad` |

## Multistream CTLs

| libopus CTL | Status | Go API / note |
|---|---:|---|
| `OPUS_MULTISTREAM_GET_ENCODER_STATE` | Supported | `(*MultistreamEncoder).StreamEncoder` |
| `OPUS_MULTISTREAM_GET_DECODER_STATE` | Supported | `(*MultistreamDecoder).StreamDecoder` |
| Generic encoder/decoder CTLs on multistream states | Partial | Aggregate bitrate, expert frame duration, reset, final range, and per-stream access are public; not every CTL has an aggregate convenience wrapper |

## Projection CTLs

| libopus CTL | Status | Go API / note |
|---|---:|---|
| `OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN` | Supported | `(*MappingMatrix).Gain` |
| `OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE` | Supported | Matrix dimensions and serialized size are available through `MappingMatrix` metadata/bytes |
| `OPUS_PROJECTION_GET_DEMIXING_MATRIX` | Supported | `(*MappingMatrix).Bytes`, projection decoder construction with demixing matrix bytes |
| Generic/multistream CTLs on projection states | Partial | Projection exposes expert frame duration and wraps multistream behavior; per-stream access is available where exposed |
