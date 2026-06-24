//go:build !opusref

// Package cgoref is the libopus CGO reference wrapper. The real implementation
// is built only under the `opusref` build tag (it needs a C toolchain and
// libopus). Without that tag the package is intentionally empty so that
// `go build ./...`, `go vet ./...`, and `go test ./...` work with no native
// dependency. Run the reference comparison with `go test -tags opusref`.
package cgoref

import "fmt"

// Encoder is a no-op placeholder for non-opusref builds.
type Encoder struct{}
type MultistreamEncoder struct{}
type MultistreamDecoder struct{}

// NewEncoder reports that the libopus encoder is unavailable without opusref.
func NewEncoder(sampleRate, channels, application int) (*Encoder, error) {
	return nil, fmt.Errorf("cgoref encoder requires -tags opusref")
}

// SetBitrate is unavailable in non-opusref builds.
func (e *Encoder) SetBitrate(bps int) error {
	return fmt.Errorf("cgoref encoder requires -tags opusref")
}

// SetComplexity is unavailable in non-opusref builds.
func (e *Encoder) SetComplexity(complexity int) error {
	return fmt.Errorf("cgoref encoder requires -tags opusref")
}

// SetVBR is unavailable in non-opusref builds.
func (e *Encoder) SetVBR(enabled bool) error {
	return fmt.Errorf("cgoref encoder requires -tags opusref")
}

// SetVBRConstraint is unavailable in non-opusref builds.
func (e *Encoder) SetVBRConstraint(constrained bool) error {
	return fmt.Errorf("cgoref encoder requires -tags opusref")
}

// SetBandwidth is unavailable in non-opusref builds.
func (e *Encoder) SetBandwidth(bandwidth int) error {
	return fmt.Errorf("cgoref encoder requires -tags opusref")
}

// SetVoiceMode is unavailable in non-opusref builds.
func (e *Encoder) SetVoiceMode() error {
	return fmt.Errorf("cgoref encoder requires -tags opusref")
}

// Encode is unavailable in non-opusref builds.
func (e *Encoder) Encode(pcm []float32, frameSize int) ([]byte, error) {
	return nil, fmt.Errorf("cgoref encoder requires -tags opusref")
}

// Close is a no-op in non-opusref builds.
func (e *Encoder) Close() {}

func NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte, application int) (*MultistreamEncoder, error) {
	return nil, fmt.Errorf("cgoref multistream encoder requires -tags opusref")
}

func (e *MultistreamEncoder) Encode(pcm []float32, frameSize int) ([]byte, error) {
	return nil, fmt.Errorf("cgoref multistream encoder requires -tags opusref")
}

func (e *MultistreamEncoder) Close() {}

func NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*MultistreamDecoder, error) {
	return nil, fmt.Errorf("cgoref multistream decoder requires -tags opusref")
}

func (d *MultistreamDecoder) DecodeFloat(packet []byte, maxSPC int) ([]float32, error) {
	return nil, fmt.Errorf("cgoref multistream decoder requires -tags opusref")
}

func (d *MultistreamDecoder) Close() {}
