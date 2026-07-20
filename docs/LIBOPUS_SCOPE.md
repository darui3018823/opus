# libopus Compatibility Scope

Last reviewed: 2026-07-21

This project targets a Pure Go implementation of standard Opus and the core
libopus 1.6.1 behavior needed to encode, decode, recover loss, inspect and
transform packets, and operate multistream and projection streams. It is not a
C ABI drop-in replacement and encoder output is not expected to be bit-exact.

## In Scope

- RFC 6716 Opus bitstreams and RFC 8251 decoder conformance
- CELT, SILK-only, and hybrid interoperability
- Core encoder and decoder controls where the Go API exposes them
- PLC, SILK LBRR in-band FEC, packet helpers, and repacketization
- RFC multistream/surround framing and RFC 8486 projection/Ambisonics
- Measured encoder quality, bitrate behavior, and practical runtime cost

Ogg Opus is a useful Pure Go product feature, but it is not counted as a
libopus core-library compatibility requirement.

## Outside the Compatibility Claim

- libopus 1.6.1 DRED parsing, processing, neural recovery, and DRED CTLs
- QEXT and OSCE codec/DSP processing, DNN blob loading, and decoder extension
  policy CTLs
- `opus_custom.h` / Opus Custom
- libopus's C ABI and source-level C API compatibility
- Bit-exact parity with the libopus encoder

Packet extensions, including DRED and QEXT payloads, can be parsed, generated,
or transported opaquely. That transport support is not codec/DSP support.

## Claim Boundary

The accurate current claim is “Pure Go standard Opus with substantial core
libopus interoperability.” It is not “complete libopus 1.6.1 replacement.”
Remaining core gaps are tracked in `docs/CURRENT_IMPLEMENTATION.md`, while
surface and semantic control parity are separated in `docs/CTL_PARITY.md`.
