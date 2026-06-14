//go:build opusref

// Package cgoref wraps libopus via CGO for golden-test comparisons.
package cgoref

/*
#cgo CFLAGS: -IC:/msys64/mingw64/include/opus
#cgo LDFLAGS: -LC:/msys64/mingw64/lib -lopus
#include <opus.h>
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// Decoder wraps a libopus OpusDecoder.
type Decoder struct {
	dec      *C.OpusDecoder
	channels int
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
