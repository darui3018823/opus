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
1.6.1 reference frame-by-frame. The **encoder** is still a simplified CELT-only
path and is not yet bit-exact — see [Status](#status).

> Honesty note: this project is decoder-complete and encoder-in-progress. It is
> suitable for decoding real Opus streams in Go; it is **not** yet a drop-in
> bit-exact replacement for libopus on the encode side.

## Status

| Area | State |
|------|-------|
| **Decoder** | ✅ Passes all 12 official RFC 8251 vectors (RMSE < 0.001); matches libopus 1.6.1 reference. SILK, CELT, and hybrid (SILK+CELT) modes are reconstructed, including hybrid SILK→CELT redundancy. |
| **Encoder** | 🚧 Simplified CELT-only, fullband, 20 ms packets. Functional but **not** bit-exact against libopus. `application`/VBR settings are stored but do not yet drive full mode selection. |
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

> The encoder currently emits CELT-only fullband 20 ms packets and is not
> bit-exact with libopus. Use it for round-trip/experimentation, not for
> interoperability-critical encoding yet.

```go
enc, err := opus.NewEncoder(48000, 2, opus.ApplicationAudio)
if err != nil {
	log.Fatal(err)
}
enc.SetBitrate(128000)
enc.SetComplexity(10)

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
```

## Supported Configurations

- **Sample rates**: 8 kHz, 12 kHz, 16 kHz, 24 kHz, 48 kHz. Non-48 kHz input to
  the encoder is resampled to 48 kHz internally; the decoder resamples its
  output to the requested rate.
- **Channels**: mono and stereo.
- **Decoder frame sizes**: all Opus durations (2.5/5/10/20/40/60 ms), selected
  per packet by the TOC byte.
- **Encoder frame size**: 20 ms (960 samples per channel at 48 kHz).
- **Application types** (stored by the encoder; full mode selection is WIP):
  - `opus.ApplicationVOIP`
  - `opus.ApplicationAudio`
  - `opus.ApplicationRestrictedLowDelay`

## Public API

### Encoder

```go
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error)

func (e *Encoder) Encode(pcm []int16, frameSize int) ([]byte, error)
func (e *Encoder) EncodeFloat(pcm []float64, frameSize int) ([]byte, error)

func (e *Encoder) SetBitrate(bitrate int) error      // 6000–510000 bps
func (e *Encoder) SetComplexity(complexity int) error // 0–10
func (e *Encoder) SetVBR(vbr bool)
func (e *Encoder) SetApplication(application Application)
func (e *Encoder) Reset() error
```

### Decoder

```go
func NewDecoder(sampleRate, channels int) (*Decoder, error)

func (d *Decoder) Decode(data []byte, pcm []int16) (int, error)
func (d *Decoder) DecodeFloat(data []byte) ([]float64, error)
func (d *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error) // currently a CELT PLC fallback
func (d *Decoder) Reset() error
func (d *Decoder) GetLastPacketDuration() int
```

There is intentionally **no** `EncodeFloat32`, `DecodeFloat32`, or
`DecodePLC(pcm, frameSize)` API. Use the float64 variants above.

## Architecture

```
github.com/darui3018823/opus/
├── opus.go / constants.go / errors.go  # Public API (Encoder/Decoder)
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

**Encoding flow**: PCM → optional resample to 48 kHz → CELT encoder (MDCT, band
processing, PVQ) → range coder → TOC prepended → Opus packet.

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
```

`FuzzDecode` and `FuzzDecodeFloat` assert that the decoder never panics on
arbitrary input. The `fuzz` CI workflow runs them nightly and on demand.

## Continuous Integration

Four GitHub Actions workflows, each running on a matrix of **amd64
(`ubuntu-latest`)** and **arm64 (`ubuntu-24.04-arm`)**:

- **`test.yml`** — `go vet`, `go test ./...`, and the official RFC 8251 vectors.
- **`race.yml`** — `go test -race ./...`.
- **`bench.yml`** — `go test -bench=. -benchmem`, uploading results as artifacts.
- **`fuzz.yml`** — nightly + manual `go test -fuzz` per target.

## Documentation

- **[docs/CURRENT_IMPLEMENTATION.md](docs/CURRENT_IMPLEMENTATION.md)** — code-derived snapshot of the API, internals, tests, and known gaps (authoritative).
- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — design decisions and libopus analysis.
- **[docs/ROADMAP.md](docs/ROADMAP.md)** — development phases and milestones.
- **[docs/DEVELOPER.md](docs/DEVELOPER.md)** — code style, porting guidance, profiling tips.
- **[IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md)** — spec gap list and compliance/test plan.

## Limitations

- The encoder is simplified CELT-only and not bit-exact; there is no SILK-only
  or hybrid encoder path.
- `DecodeFEC` is currently a PLC fallback, not packet FEC extraction.
- No multistream, surround, or Ogg Opus container API.
- `application`, VBR, and some CTL-style settings are stored but not yet wired to
  full libopus-compatible behavior.

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
