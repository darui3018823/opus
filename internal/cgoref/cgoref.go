//go:build opusref

// Package cgoref wraps libopus via CGO for golden-test comparisons.
package cgoref

/*
#cgo CFLAGS: -I${SRCDIR}/../../libopus/include -IC:/msys64/mingw64/include/opus
#cgo LDFLAGS: -LC:/msys64/mingw64/lib -lopus
#include <opus.h>
#include <stdlib.h>

static int go_opus_encoder_set_bitrate(OpusEncoder *enc, int bps) {
	return opus_encoder_ctl(enc, OPUS_SET_BITRATE(bps));
}

static int go_opus_encoder_set_complexity(OpusEncoder *enc, int complexity) {
	return opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(complexity));
}

static int go_opus_encoder_set_voice(OpusEncoder *enc) {
	return opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));
}
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// Encoder wraps a libopus OpusEncoder.
type Encoder struct {
	enc      *C.OpusEncoder
	channels int
}

// Decoder wraps a libopus OpusDecoder.
type Decoder struct {
	dec      *C.OpusDecoder
	channels int
}

// NewEncoder creates a libopus encoder.
func NewEncoder(sampleRate, channels, application int) (*Encoder, error) {
	var code C.int
	enc := C.opus_encoder_create(C.opus_int32(sampleRate), C.int(channels), C.int(application), &code)
	if code != 0 {
		return nil, fmt.Errorf("opus_encoder_create: %s", C.GoString(C.opus_strerror(code)))
	}
	return &Encoder{enc: enc, channels: channels}, nil
}

// NewDecoder creates a libopus decoder.
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	var code C.int
	dec := C.opus_decoder_create(C.opus_int32(sampleRate), C.int(channels), &code)
	if code != 0 {
		return nil, fmt.Errorf("opus_decoder_create: %s", C.GoString(C.opus_strerror(code)))
	}
	return &Decoder{dec: dec, channels: channels}, nil
}

// SetBitrate sets the libopus encoder target bitrate in bits per second.
func (e *Encoder) SetBitrate(bps int) error {
	code := C.go_opus_encoder_set_bitrate(e.enc, C.int(bps))
	if code != 0 {
		return fmt.Errorf("OPUS_SET_BITRATE: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

// SetComplexity sets the libopus encoder complexity (0..10).
func (e *Encoder) SetComplexity(complexity int) error {
	code := C.go_opus_encoder_set_complexity(e.enc, C.int(complexity))
	if code != 0 {
		return fmt.Errorf("OPUS_SET_COMPLEXITY: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

// SetVoiceMode biases libopus toward its voice/SILK mode decisions.
func (e *Encoder) SetVoiceMode() error {
	code := C.go_opus_encoder_set_voice(e.enc)
	if code != 0 {
		return fmt.Errorf("OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE): %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

// Encode encodes one interleaved float32 PCM frame with libopus.
func (e *Encoder) Encode(pcm []float32, frameSize int) ([]byte, error) {
	need := frameSize * e.channels
	if len(pcm) < need {
		return nil, fmt.Errorf("insufficient PCM data: got %d, need %d", len(pcm), need)
	}
	out := make([]byte, 4000)
	n := C.opus_encode_float(e.enc,
		(*C.float)(unsafe.Pointer(&pcm[0])),
		C.int(frameSize),
		(*C.uchar)(unsafe.Pointer(&out[0])),
		C.opus_int32(len(out)))
	if n < 0 {
		return nil, fmt.Errorf("opus_encode_float: %s", C.GoString(C.opus_strerror(n)))
	}
	return out[:int(n)], nil
}

// Close frees the libopus encoder.
func (e *Encoder) Close() {
	if e.enc != nil {
		C.opus_encoder_destroy(e.enc)
		e.enc = nil
	}
}

// DecodeFloat decodes one packet to float32. Returns samples per channel.
func (d *Decoder) DecodeFloat(packet []byte, maxSPC int) ([]float32, error) {
	pcm := make([]float32, maxSPC*d.channels)
	var ptr *C.uchar
	if len(packet) > 0 {
		ptr = (*C.uchar)(unsafe.Pointer(&packet[0]))
	}
	n := C.opus_decode_float(d.dec, ptr, C.opus_int32(len(packet)),
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(maxSPC), 0)
	if n < 0 {
		return nil, fmt.Errorf("opus_decode_float: %s", C.GoString(C.opus_strerror(n)))
	}
	return pcm[:int(n)*d.channels], nil
}

// Close frees the libopus decoder.
func (d *Decoder) Close() {
	if d.dec != nil {
		C.opus_decoder_destroy(d.dec)
		d.dec = nil
	}
}

// Version returns the libopus version string.
func Version() string {
	return C.GoString(C.opus_get_version_string())
}
