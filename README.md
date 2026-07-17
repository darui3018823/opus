# Pure Go Opus Codec

[![Go Reference](https://pkg.go.dev/badge/github.com/darui3018823/opus.svg)](https://pkg.go.dev/github.com/darui3018823/opus)
[![Test](https://github.com/darui3018823/opus/actions/workflows/test.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/test.yml)
[![Race](https://github.com/darui3018823/opus/actions/workflows/race.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/race.yml)
[![Fuzz](https://github.com/darui3018823/opus/actions/workflows/fuzz.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/fuzz.yml)
[![License](https://img.shields.io/badge/license-BSD--2--Clause-blue.svg)](LICENSE)

[日本語](README_ja.md) | English

`github.com/darui3018823/opus` is a stateful, Pure Go Opus codec library with
no runtime CGO dependency. It provides single-stream, multistream, surround,
projection/Ambisonics, packet transformation, and Ogg Opus APIs.

The decoder passes all 12 official RFC 8251 vectors with RMSE below 0.001 and
is cross-checked against libopus 1.6.1. The encoder produces
standards-compatible CELT, SILK-only, and hybrid packets, but is not bit-exact
with libopus and does not reproduce every libopus mode/rate/quality decision.
The authoritative implementation snapshot is
[docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md).

## Install

```bash
go get github.com/darui3018823/opus
```

The module currently declares Go 1.24.11 in [`go.mod`](go.mod).

## Minimal encode/decode

```go
package main

import (
	"fmt"
	"log"

	"github.com/darui3018823/opus"
)

func main() {
	const channels = 2
	encoder, err := opus.NewEncoder(
		opus.SampleRate48kHz,
		channels,
		opus.ApplicationAudio,
	)
	if err != nil {
		log.Fatal(err)
	}

	// frameSize is samples per channel; PCM is interleaved.
	pcm := make([]int16, opus.FrameSize20ms*channels)
	packet, err := encoder.Encode(pcm, opus.FrameSize20ms)
	if err != nil {
		log.Fatal(err)
	}

	decoder, err := opus.NewDecoder(opus.SampleRate48kHz, channels)
	if err != nil {
		log.Fatal(err)
	}
	decoded := make([]int16, opus.MaxFrameSize*channels)
	samplesPerChannel, err := decoder.Decode(packet, decoded)
	if err != nil {
		log.Fatal(err)
	}
	decoded = decoded[:samplesPerChannel*channels]
	fmt.Println(samplesPerChannel, len(decoded)) // 960 1920
}
```

Executable examples are included in the [root package documentation](https://pkg.go.dev/github.com/darui3018823/opus)
and [Ogg Opus package documentation](https://pkg.go.dev/github.com/darui3018823/opus/oggopus).

## Support matrix

| Area | Support |
|---|---|
| Sample rates | 8, 12, 16, 24, and 48 kHz |
| Single-stream channels | Mono and stereo |
| PCM APIs | Interleaved int16, signed 24-bit-in-int32, float32, and float64 |
| Encoder packet durations | CELT 2.5/5/10 ms; exact 20 ms multiples through 120 ms |
| Decoder packet durations | Valid Opus packets up to 120 ms |
| Coding modes | CELT encode/decode; voice-oriented SILK-only and hybrid encode; SILK/hybrid decode |
| Loss handling | CELT/SILK/hybrid PLC; SILK LBRR in-band FEC encode/decode |
| Multistream/surround | RFC self-delimited framing; families 0, 1 (through 7.1), and 255; PLC/FEC |
| Projection/Ambisonics | RFC 8486 families 2 and 3; predefined first- through fifth-order family-3 matrices |
| Packet tools | Inspection, repacketizing, padding, soft clipping, LBRR detection, extensions |
| Ogg Opus | CRC/lacing, headers/tags, timing trims, chained reading, per-link seek, single-link writing |
| Runtime dependencies | Pure Go; CGO/libopus is optional and test-only under `opusref` |

`MaxFrameSize` is 5760 samples per channel at 48 kHz (120 ms).
`MaxFrameBytes` is the 1275-byte compressed-frame limit. `MaxPacketSize` is a
conservative bound for an unpadded single-stream packet; explicit padding may
exceed it.

## Correct use of state and memory

Encoder, decoder, multistream, surround, projection, repacketizer, and Ogg
reader/writer values are stateful and are not safe for concurrent use. Use one
instance per logical stream, process packets in order, and serialize every
operation on an instance, including getters, controls, child stream access,
seeking, and `Reset`. Distinct instances may run concurrently. Do not copy an
instance after first use.

Codec methods borrow per-call PCM, packet, and destination slices only until
the call returns. Constructors copy mappings and matrices. Returned packet,
PCM, mapping, and matrix slices are caller-owned copies. Ogg readers and writers
borrow their `io.Reader`/`io.Writer` for the instance lifetime and do not close
it.

Where a codec type exposes `Reset`, it clears codec history and last-packet
observations while retaining configuration such as bitrate, application,
output gain, and phase-inversion controls. Reuse an instance after `Reset` only
for a new stream with the same configuration, or apply new controls explicitly.

## Conformance and reference testing

CI downloads the unmodified RFC 8251 vector archive and runs all 12 decoder
vectors. Vector data is not committed, so local vector tests skip when
`testdata/opus_newvectors/` is absent.

```bash
go test -count=1 ./...
go test -count=1 -run TestOfficialVectors .
```

Optional `opusref` tests require a C toolchain and libopus. They compare decoder
output, interoperability, final ranges, FEC/PLC, multistream, projection, and
selected encoder quality behavior against libopus 1.6.1. They are reference
checks, not runtime dependencies.

```bash
go test -count=1 -tags opusref ./...
```

## Input safety and fuzzing

Public decoder and packet/container parsing paths are designed to reject
malformed input with errors instead of panicking. Stateful decoder sequences,
PLC/FEC interleaving, encoder controls and extreme PCM (including floating-point
edge cases), packet extensions, multistream framing, repacketizing, and Ogg
Reader/Writer round trips have dedicated fuzz targets. CI runs the target set
nightly on Linux amd64 and arm64.

This is a bounded API guarantee, not a claim that fuzzing proves correctness.
Callers must still enforce application-level packet, stream, CPU, and memory
budgets; fuzz coverage does not guarantee audio quality, timing availability,
or protection against every denial-of-service pattern. Report suspected
security issues through [SECURITY.md](SECURITY.md), not a public issue.

Example local runs:

```bash
go test -run='^$' -fuzz='^FuzzDecoderSequence$' -fuzztime=60s .
go test -run='^$' -fuzz='^FuzzOggOpusReaderWriter$' -fuzztime=60s ./oggopus
```

## Current limitations

- Encoder output is standards-compatible but not bit-exact with libopus.
- SILK/hybrid encoding is voice-oriented and does not implement every libopus
  mode boundary, rate-control decision, or quality heuristic.
- DRED and QEXT packet extensions are transported opaquely; their codecs/DSP
  are not implemented.
- Projection family 3 uses predefined libopus 1.6.1 matrices; arbitrary custom
  encoder matrix generation is not provided.
- The Ogg Opus reader supports chained logical streams but not multiplexed
  physical-stream demultiplexing. Each Writer emits one logical stream.
- Not every libopus single-stream or multistream CTL has a public equivalent.
  `SetLSBDepth` is retained as a compatibility hint but currently does not
  affect codec decisions.

## Development

```bash
go generate ./...
git diff --exit-code
go fmt ./...
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go test -run='^$' -bench '^BenchmarkPerf/' -benchmem .
```

Normal CI runs native tests on Linux, macOS, and Windows for both amd64 and
arm64. The Windows arm64 runner is currently a GitHub public-preview image.
Generated-file drift, vet, and official vectors are separate Ubuntu jobs, and
the `opusref` workflow stays on Ubuntu with libopus. See the workflow files for
the exact current matrix.

## Documentation

- [Go API reference](https://pkg.go.dev/github.com/darui3018823/opus)
- [Ogg Opus API reference](https://pkg.go.dev/github.com/darui3018823/opus/oggopus)
- [Current implementation snapshot](docs/CURRENT_IMPLEMENTATION.md)
- [CTL and helper parity](docs/CTL_PARITY.md)
- [Performance baseline and benchmark method](docs/PERF_BASELINE.md)
- [Historical architecture and design background](docs/ARCHITECTURE.md)
- [Mode/rate policy differences](docs/MODE_RATE_POLICY_DIFF.md)
- [Real-corpus scoreboard](docs/REAL_CORPUS_SCOREBOARD.md)
- [Developer guide](docs/DEVELOPER.md)
- [Release checklist](docs/RELEASE_CHECKLIST.md)
- [Possible v2 API changes](docs/V2_API_CANDIDATES.md)
- [Security policy](SECURITY.md)
- [Contributing guide](CONTRIBUTING.md)

## License

BSD 2-Clause License. See [LICENSE](LICENSE).
