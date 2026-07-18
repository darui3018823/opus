# Possible v2 API Changes

This is a compatibility audit, not a v2 commitment or implementation plan.
The current v1 API remains supported; do not make these changes in a v1
release. Before any v2 work, validate real caller impact and provide a
migration/deprecation path where practical.

Last audited: 2026-07-19, against the `v1.4.0` release candidate.

## Strong candidates

| Current v1 surface | Possible v2 direction | Reason and compatibility impact |
|---|---|---|
| `Application = int`, internal `SignalType` alias, and untyped mode/bandwidth/mapping/policy constants | Introduce package-owned defined types with typed constants | Prevents accidental interchange of unrelated integer domains and removes the public identity of an internal type. Changing parameter and return types is source-breaking. |
| `BitrateMin` / `BitrateMaxVal` are nominal libopus limits, while `SetBitrate` accepts a different implementation range; `BitrateMax` is a policy sentinel | Separate policy values from accepted numeric bounds, with names such as `BitrateMaximum` and `EncoderBitrateMin` / `EncoderBitrateMax` | Current names invite values that the encoder rejects. Renaming or redefining exported constants can break source or behavior. |
| `FrameSize80ms` through `FrameSize120ms` and `MaxFrameSize` | Use packet-duration/sample names, and distinguish maximum frame bytes from maximum unpadded packet storage | An individual Opus frame is at most 60 ms; the longer values describe packets. Renaming constants is source-breaking. |
| `Bandwidth()` plus `GetBandwidth()`, `GetLastPacketDuration()`, and `PacketGet*` helpers | Keep idiomatic noun getters, or isolate the C/libopus-compatible naming surface | The public naming convention is inconsistent. Removing aliases or renaming helpers is source-breaking. |
| Raw exported CTL request integers without a generic CTL method | Move compatibility request values to a focused compatibility surface, or remove them | The constants enlarge the main API without being directly callable. Relocation/removal is source-breaking. |
| Projection/Ambisonics constructors with many positional values and asymmetric encoder/decoder types | Use explicit family-specific configuration structs and symmetric types; carry matrix gain with matrix bytes | Current constructors conditionally use arguments and expose matrix information asymmetrically. Replacing constructors and concrete return types is source-breaking. |
| Root sentinel taxonomy (`ErrBadArg`, unused `ErrAllocFail` / `ErrInternalError`, broad `ErrUnimplemented`) | Define a documented error hierarchy with invalid-argument, unsupported-operation, insufficient-input/output, and compatibility categories | Some sentinels are unreachable and others cover unrelated recovery cases. Changing `errors.Is` results is behavioral compatibility work. |
| Ogg parse and stream errors are specific but do not consistently wrap a broad category | Make specific page/header/stream failures consistently match a broad category and preserve the underlying I/O cause | Callers currently cannot classify all malformed pages or streams with one `errors.Is` check. New wrapping can be additive, but removals/renames belong in v2. |

## Additional naming and ownership candidates

- Rename `oggopus.Head` / `Tags` to `OpusHead` / `OpusTags`, and consider
  copy-returning metadata accessors instead of mutable `Reader.Head` and
  `Reader.Tags` fields.
- Make Ogg 48 kHz units explicit and consistent, for example
  `DurationSamples48kHz` and `SeekPCM48kHz`.
- Align `Packet.BOS` / `Packet.EOS` fields with the `Page.BOS()` / `Page.EOS()`
  method shape, and distinguish “current link has seen EOS” from “the chained
  reader is exhausted.”
- Rename `HeaderType` to a flags-oriented name and align `CoupledCount` with
  `CoupledStreams` terminology.
- Use full or consistently initialism-cased control names, such as
  `PacketLossPercent`, `InBandFEC`, and an explicit packet-padding byte unit.
- Consider explicit PCM format names (`Float64`, `Float32`, `Int16`, and
  signed-24-in-`int32`) if a broader buffer/config API is designed. Naming
  churn alone is not enough reason to break callers.
- Reconsider `ErrNotSeekable`, which currently covers both capability absence
  and failures from a seekable source, and remove or replace the low-level
  `ErrAfterEOS` sentinel if the redesigned iterator cannot expose that state.
- Return a state-oriented error for an empty `Repacketizer.Out`, and distinguish
  incompatible packet aggregation from structurally invalid packet data.

## v1 policy

Within v1, preserve exported names, types, sentinel identities, method sets,
and documented behavior. Prefer additive APIs, documentation corrections, and
additional error wrapping only when existing `errors.Is` behavior remains
intact. Any deprecation should name the replacement and remain usable for the
rest of v1.

