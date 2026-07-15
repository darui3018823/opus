# Pure Go Opus Codec

[![Go Reference](https://pkg.go.dev/badge/github.com/darui3018823/opus.svg)](https://pkg.go.dev/github.com/darui3018823/opus)
[![Test](https://github.com/darui3018823/opus/actions/workflows/test.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/test.yml)
[![Race](https://github.com/darui3018823/opus/actions/workflows/race.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/race.yml)
[![Fuzz](https://github.com/darui3018823/opus/actions/workflows/fuzz.yml/badge.svg)](https://github.com/darui3018823/opus/actions/workflows/fuzz.yml)
[![License](https://img.shields.io/badge/license-BSD--2--Clause-blue.svg)](LICENSE)

[日本語](README_ja.md) | English

A pure-Go implementation of the [Opus audio codec](https://opus-codec.org/)
(RFC 6716 / RFC 8251) with **no runtime CGO dependency**. The **decoder** passes
all 12 official RFC 8251 test vectors (RMSE < 0.001) and matches the libopus
1.6.1 reference frame-by-frame. The **encoder** implements the full CELT quality
pipeline, plus a limited low-bitrate SILK-only speech path, and produces
standard Opus packets that libopus decodes correctly — see [Status](#status).

> Note: the encoder is not bit-exact with libopus. The CELT path is suitable for
> speech and music in pure Go. The SILK path is intentionally limited to
> low-bitrate speech, and hybrid encoding is currently limited to high-bitrate
> 24/48 kHz voice packets.

## Status

| Area | State |
|------|-------|
| **Decoder** | ✅ Passes all 12 official RFC 8251 vectors (RMSE < 0.001); matches libopus 1.6.1 reference. SILK, CELT, and hybrid (SILK+CELT) modes are reconstructed, including hybrid SILK→CELT redundancy. |
| **Encoder** | ✅ Full CELT quality pipeline (Phase 1+2), limited SILK-only speech encode for low-bitrate voice, and initial hybrid speech encode for high-bitrate 24/48 kHz voice. Emits standard Opus packets that libopus 1.6.1 decodes correctly. SNR: ~48 dB (440 Hz), ~47 dB (1 kHz), ~43 dB (stereo) at 64 kbps. **Not** bit-exact with libopus. |
| **CGO** | None at runtime. A libopus wrapper exists only for reference tests, behind the `opusref` build tag. |
| **CI** | `test`, `race`, `bench`, and `fuzz` workflows run on **amd64 and arm64**. |

See [docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md) for the
authoritative, code-derived snapshot.

## Installation

```bash
go get github.com/darui3018823/opus
```

Requires Go 1.24 or newer (see `go.mod`).

## Usage

### Decoding (int16)

```go
package main

import (
	"log"

	"github.com/darui3018823/opus"
)

func main() {
	// 48 kHz, stereo.
	dec, err := opus.NewDecoder(48000, 2)
	if err != nil {
		log.Fatal(err)
	}

	// packet is one Opus packet (e.g. read from a file or the network).
	var packet []byte

	// Buffer for the decoded PCM. 120 ms at 48 kHz (the largest Opus frame)
	// is 5760 samples per channel; size generously for the frames you expect.
	pcm := make([]int16, 5760*2)

	n, err := dec.Decode(packet, pcm)
	if err != nil {
		log.Fatal(err)
	}
	// pcm[:n*2] now holds interleaved stereo samples (n samples per channel).
	_ = n
}
```

### Decoding (float64)

```go
// DecodeFloat returns a freshly allocated, interleaved []float64.
samples, err := dec.DecodeFloat(packet)
if err != nil {
	log.Fatal(err)
}
_ = samples
```

### Encoding

```go
enc, err := opus.NewEncoder(48000, 2, opus.ApplicationAudio)
if err != nil {
	log.Fatal(err)
}
enc.SetBitrate(64000)
enc.SetComplexity(10)
enc.SetVBR(true) // variable bitrate (default: CBR)

// 20 ms frame = 960 samples per channel at 48 kHz, interleaved stereo.
pcm := make([]int16, 960*2)
// ... fill pcm ...

packet, err := enc.Encode(pcm, 960)
if err != nil {
	log.Fatal(err)
}
_ = packet

// Float64 input is also supported:
//   packet, err := enc.EncodeFloat(make([]float64, 960*2), 960)
// Float32 input is supported with EncodeFloat32.

// Bandwidth is detected automatically from signal content; override if needed:
//   enc.SetBandwidth(opus.BandwidthWideband) // force wideband
//   enc.SetBandwidth(opus.BandwidthAuto)     // restore auto

// Optional content hint independent of Application:
//   enc.SetSignalType(opus.SignalVoice)

// Short CELT packets and multi-frame packets are supported:
//   packet, err := enc.Encode(pcm480, 480)   // 10 ms at 48 kHz
//   packet, err := enc.Encode(pcm1920, 1920) // 40 ms
```

## Supported Configurations

- **Sample rates**: 8 kHz, 12 kHz, 16 kHz, 24 kHz, 48 kHz. Non-48 kHz input to
  the encoder is resampled to 48 kHz internally; the decoder resamples its
  output to the requested rate.
- **Channels**: mono and stereo.
- **Decoder frame sizes**: all Opus durations (2.5/5/10/20/40/60 ms), selected
  per packet by the TOC byte.
- **Encoder frame sizes**: 2.5/5/10 ms CELT packets and exact 20 ms multiples
  from 20 ms through 120 ms (multi-frame, RFC 6716 §3.2).
- **Encoder bandwidth**: automatic (signal-content-driven FFT detection) or
  manual (`SetBandwidth`/`SetMaxBandwidth`). Ranges: NB/WB/SWB/FB.
- **Encoder mode selection**: CELT is used for general audio, music,
  restricted-low-delay, and voice above the useful hybrid range. Voice
  boundaries account for channel count and active LBRR: SILK-only extends to
  40 kbps mono or 48 kbps stereo, with extra headroom when FEC is active;
  24/48 kHz voice can use hybrid at intermediate rates, then returns to CELT.
- **Application types** (drive bandwidth and transient-detection behaviour):
  - `opus.ApplicationVOIP` — narrower bandwidth tiers, eager short-block switching
  - `opus.ApplicationAudio` — music/general defaults
  - `opus.ApplicationRestrictedLowDelay`
- **Signal hints**: `opus.SignalAuto`, `opus.SignalVoice`, and
  `opus.SignalMusic` can tune encoder heuristics without changing the Opus
  bitstream format.

## Public API

The public version constants are generated from the repository's
[`VERSION`](VERSION) file.

`MaxFrameSize` is 5760 samples per channel at 48 kHz (120 ms).
`MaxFrameBytes` is the 1275-byte compressed-frame limit. `MaxPacketSize` is a
conservative unpadded single-stream packet storage bound; explicit packet
padding can exceed it.

### Encoder

```go
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error)
func NewEncoderWithProfile(sampleRate, channels int, application Application, profile EncoderProfile) (*Encoder, error)

func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error)
func (e *Encoder) Encode24(pcm []int32, frameSize int) ([]byte, error)
func (e *Encoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error)
func (e *Encoder) EncodeFloat32(pcm []float32, frameSize int) ([]byte, error)

func (e *Encoder) Bitrate() int
func (e *Encoder) EffectiveBitrate() int
func (e *Encoder) Complexity() int
func (e *Encoder) VBR() bool
func (e *Encoder) VBRConstraint() bool
func (e *Encoder) Application() Application
func (e *Encoder) SampleRate() int
func (e *Encoder) Channels() int
func (e *Encoder) Lookahead() int
func (e *Encoder) FinalRange() uint32
func (e *Encoder) InDTX() bool

func (e *Encoder) SetBitrate(bitrate int) error       // 6000–510000 bps
func (e *Encoder) SetComplexity(complexity int) error  // 0–10
func (e *Encoder) SetVBR(vbr bool)
func (e *Encoder) SetVBRConstraint(constrained bool)   // true = CVBR
func (e *Encoder) SetApplication(application Application) error
func (e *Encoder) SetSignalType(signal SignalType)
func (e *Encoder) SignalType() SignalType
func (e *Encoder) SetBandwidth(bw int) error           // Auto/NB/WB/SWB/FB
func (e *Encoder) SetMaxBandwidth(bw int) error
func (e *Encoder) MaxBandwidth() int
func (e *Encoder) Bandwidth() int
func (e *Encoder) GetBandwidth() int
func (e *Encoder) SetDTX(dtx bool)
func (e *Encoder) DTX() bool
func (e *Encoder) SetInbandFEC(enabled bool)             // SILK-only/hybrid
func (e *Encoder) InbandFEC() bool
func (e *Encoder) SetPacketLossPerc(perc int)            // clamped to 0–100
func (e *Encoder) PacketLossPerc() int
func (e *Encoder) SetPacketPadding(n int)
func (e *Encoder) SetForceChannels(channels int) error
func (e *Encoder) ForceChannels() int
func (e *Encoder) SetLSBDepth(depth int) error
func (e *Encoder) LSBDepth() int
func (e *Encoder) SetPredictionDisabled(disabled bool)
func (e *Encoder) PredictionDisabled() bool
func (e *Encoder) SetPhaseInversionDisabled(disabled bool)
func (e *Encoder) PhaseInversionDisabled() bool
func (e *Encoder) Reset() error
```

`NewEncoder` preserves the historical 64 kbit/s, complexity 5, CBR defaults.
Use `EncoderProfileLibopus` for automatic bitrate, complexity 9, and constrained
VBR defaults.

### Concurrency and ownership

`Encoder` and `Decoder` are stateful and are not safe for concurrent use.
Create one instance per logical Opus stream and preserve packet order. All
methods on the same instance, including getters, configuration methods, and
`Reset`, must be serialized by the caller, for example with a mutex. Separate
instances may be used concurrently.

Do not copy an `Encoder` or `Decoder` after first use. Encode and decode methods
borrow caller-provided PCM, packet, and destination slices only until the method
returns. Returned encoded packets and PCM slices are owned by the caller.

### Decoder

```go
func NewDecoder(sampleRate, channels int) (*Decoder, error)

func (d *Decoder) Decode(data []byte, pcm []int16) (int, error)
func (d *Decoder) Decode24(data []byte, pcm []int32) (int, error)
func (d *Decoder) DecodeFloat(data []byte) ([]float64, error)
func (d *Decoder) DecodeFloat32(data []byte) ([]float32, error)
func (d *Decoder) DecodePLC(pcm []int16, frameSize int) (int, error) // CELT, SILK-only, or hybrid PLC
func (d *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error)   // SILK LBRR
func (d *Decoder) Reset() error
func (d *Decoder) GetLastPacketDuration() int
func (d *Decoder) Bandwidth() int
func (d *Decoder) GetBandwidth() int
func (d *Decoder) SampleRate() int
func (d *Decoder) Channels() int
func (d *Decoder) FinalRange() uint32
func (d *Decoder) Pitch() int
func (d *Decoder) SetGain(gainQ8 int) error
func (d *Decoder) Gain() int
func (d *Decoder) SetPhaseInversionDisabled(disabled bool)
func (d *Decoder) PhaseInversionDisabled() bool
```

### Multistream and surround

```go
func NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte, application Application) (*MultistreamEncoder, error)
func NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*MultistreamDecoder, error)

func NewSurroundEncoder(sampleRate, channels, mappingFamily int, application Application) (*SurroundEncoder, error)
func NewSurroundDecoder(sampleRate, channels, mappingFamily int) (*SurroundDecoder, error)
```

Multistream packets use RFC 6716 self-delimited framing and interoperate with
libopus 1.6.1. Surround supports mapping families 0, 1 (Vorbis order, up to
7.1), and 255.

### Projection and Ambisonics

```go
func NewProjectionEncoder(sampleRate, channels, mappingFamily int, application Application) (*ProjectionEncoder, error)
func NewProjectionDecoder(sampleRate, channels, streams, coupledStreams int, demixingMatrix []byte) (*ProjectionDecoder, error)
func NewAmbisonicsEncoder(sampleRate, channels, mappingFamily int, application Application) (*ProjectionEncoder, error)
func NewAmbisonicsDecoder(sampleRate, channels, mappingFamily, streams, coupledStreams int, mapping, demixingMatrix []byte) (*AmbisonicsDecoder, error)
```

RFC 8486 mapping families 2 and 3 are supported. Family 2 uses ACN/SN3D
Ambisonics channel mapping; family 3 uses the projection mixing/demixing
matrices provided by libopus 1.6.1. Both families have bidirectional libopus
interoperability tests.

### Packet operations

```go
func NewRepacketizer() *Repacketizer
func (r *Repacketizer) Cat(packet []byte) error
func (r *Repacketizer) NumFrames() int
func (r *Repacketizer) Out() ([]byte, error)
func (r *Repacketizer) OutRange(begin, end int) ([]byte, error)
func PacketPad(packet []byte, newLen int) ([]byte, error)
func PacketUnpad(packet []byte) ([]byte, error)
func MultistreamPacketPad(packet []byte, streams, newLen int) ([]byte, error)
func MultistreamPacketUnpad(packet []byte, streams int) ([]byte, error)
func PacketHasLBRR(packet []byte) (bool, error)
func SoftClipFloat32(pcm []float32, channels int, mem []float32) error
func PacketExtensionsCount(packet []byte) (int, error)
func PacketExtensionsParse(packet []byte) ([]PacketExtension, error)
func PacketExtensionsGenerate(packet []byte, extensions []PacketExtension, paddingBytes int) ([]byte, error)
```

Packet extensions are transported through code-3 padding. DRED and QEXT
payloads are exposed as opaque data; their neural/DSP codecs are not
implemented here.
See [docs/CTL_PARITY.md](docs/CTL_PARITY.md) for CTL/helper parity against
libopus 1.6.1.

### Ogg Opus containers

The `github.com/darui3018823/opus/oggopus` package provides CRC-checked Ogg
page parsing/writing, packet continuation and lacing, `OpusHead`/`OpusTags`
metadata, and complete single-logical-stream Ogg Opus readers and writers.

## Architecture

```
github.com/darui3018823/opus/
├── opus.go / multistream.go / surround.go / projection.go
├── extensions.go / repacketizer.go         # Packet operations
├── oggopus/                                # Ogg page and Ogg Opus APIs
├── internal/
│   ├── opus_framing.go                  # TOC byte parsing/generation (RFC 6716 §3)
│   ├── dsp/                             # FFT, MDCT/IMDCT, windows, math
│   ├── entcode/                         # Range encoder/decoder
│   ├── resampler/                       # Opus-rate sample rate conversion
│   ├── celt/                            # CELT decoder parity + simplified encoder
│   ├── silk/                            # SILK decoder/encoder, tables, helpers
│   └── cgoref/                          # libopus reference wrapper (build tag: opusref)
└── docs/                                # Design and status documentation
```

**Decoding flow**: Opus packet → TOC parsed → CELT or SILK/hybrid path → range
decoder + reconstruction → optional resample/channel adjust → PCM.

**Encoding flow**: PCM → mode selection → either SILK-only speech encode
(resampling 24/48 kHz voice input to WB SILK when selected) or optional resample
to 48 kHz and CELT encode (MDCT, band processing, PVQ) → range coder → TOC
prepended → Opus packet.

## Building & Testing

```bash
go build ./...
go vet ./...
go test ./...                 # library packages + official vectors (when present)
go test -race ./...
go test -bench=. -benchmem -run='^$' ./...
```

Official RFC 8251 test vectors are **not** committed (`testdata/` is
git-ignored). Tests that need them call `t.Skip` when they are absent. To run
them locally, download and extract the vectors so they land in
`testdata/opus_newvectors/`:

```bash
curl -fSL -o /tmp/v.tar.gz https://opus-codec.org/docs/opus_testvectors-rfc8251.tar.gz
mkdir -p testdata && tar -xzf /tmp/v.tar.gz -C testdata/
go test -run TestOfficialVectors ./...
```

### libopus reference comparison (optional)

The `TestCGORef` test decodes every vector with both this codec and libopus and
compares them frame-by-frame. It requires a C toolchain plus libopus and is
gated behind the `opusref` build tag (so normal builds stay CGO-free):

```bash
go test -tags opusref -run TestCGORef .
```

On Windows, run CGO builds from PowerShell with a working MinGW/MSYS2 toolchain.

### Fuzzing

```bash
go test -run='^$' -fuzz='^FuzzDecode$' -fuzztime=60s .
go test -run='^$' -fuzz='^FuzzOggParsers$' -fuzztime=60s ./oggopus
```

The fuzz suite covers single-stream decoding, packet extensions, multistream
self-delimited framing, repacketization/padding, and Ogg Opus parsing. The
`fuzz` CI workflow runs every target nightly and on demand.

## Continuous Integration

Four GitHub Actions workflows, each running on a matrix of **amd64
(`ubuntu-latest`)** and **arm64 (`ubuntu-24.04-arm`)**:

- **`test.yml`** — `go vet`, `go test ./...`, and the official RFC 8251 vectors.
- **`race.yml`** — `go test -race ./...`.
- **`bench.yml`** — `go test -bench=. -benchmem`, uploading results as artifacts.
- **`fuzz.yml`** — nightly + manual `go test -fuzz` per target.

## Documentation

- **[docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md)** — code-derived snapshot of the API, internals, tests, and known gaps (authoritative).
- **[docs/CTL_PARITY.md](docs/CTL_PARITY.md)** — libopus 1.6.1 CTL/helper parity matrix.
- **[docs/REAL_CORPUS_SCOREBOARD.md](docs/REAL_CORPUS_SCOREBOARD.md)** — opt-in real-corpus matched-bitrate A/B scoreboard.
- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — design decisions and libopus analysis.
- **[docs/ROADMAP.md](docs/ROADMAP.md)** — development phases and milestones.
- **[docs/DEVELOPER.md](docs/DEVELOPER.md)** — code style, porting guidance, profiling tips.
- **[IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md)** — spec gap list and compliance/test plan.

## Limitations

- SILK/hybrid encode remains voice-oriented and does not yet reproduce every
  libopus mode boundary or quality decision.
- The encoder is not bit-exact with libopus, but produces standards-conformant
  packets that any compliant decoder (including libopus) can decode.
- VBR/CVBR and application/signal hints shape the CELT encoder heuristics, but
  do not provide full libopus-equivalent mode/rate-control behavior.
- SILK-only and hybrid encoding can emit LBRR/in-band FEC for mono and stereo
  via `SetInbandFEC(true)` and a non-zero `SetPacketLossPerc`.
- `DecodeFEC` recovers SILK-only and hybrid packets from LBRR in the following
  packet. Hybrid recovery contains the redundant SILK low band. `DecodePLC`
  conceals CELT-only, SILK-only, and hybrid losses after a successful decode.
- Projection family 3 uses the predefined libopus 1.6.1 matrices; the package
  does not currently generate arbitrary custom encoder matrices.
- The Ogg Opus package handles one logical stream and does not provide seeking,
  chained-stream orchestration, or multiplexed-stream demux.
- Multistream/surround do not yet expose every libopus multistream CTL or the
  complete libopus surround energy-mask analysis.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on how to contribute, report bugs, and submit pull requests.

Please note that this project is released with a [Contributor Code of Conduct](CODE_OF_CONDUCT.md). By participating you agree to abide by its terms.

## License

BSD 2-Clause License — see [LICENSE](LICENSE).

## Acknowledgments

- **[libopus](https://github.com/xiph/opus)** — reference implementation by the Xiph.Org Foundation.
- **[RFC 6716](https://datatracker.ietf.org/doc/html/rfc6716)** / **[RFC 8251](https://datatracker.ietf.org/doc/html/rfc8251)** — the Opus specification and its updates.

## Support

For issues and questions, please use the [GitHub issue tracker](https://github.com/darui3018823/opus/issues).
