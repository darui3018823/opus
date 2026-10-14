//go:build opusref && opus16

package cgoref

/*
#cgo CFLAGS: -I${SRCDIR}/../../libopus/include -IC:/msys64/mingw64/include/opus
#include <opus.h>
#include <opus_multistream.h>
#include <opus_projection.h>

static int go_opus_decoder_set_gain(OpusDecoder *dec, int gain) {
	return opus_decoder_ctl(dec, OPUS_SET_GAIN(gain));
}

static int go_opus_ms_decoder_set_gain(OpusMSDecoder *dec, int gain) {
	return opus_multistream_decoder_ctl(dec, OPUS_SET_GAIN(gain));
}

static int go_opus_projection_decoder_set_gain(OpusProjectionDecoder *dec, int gain) {
	return opus_projection_decoder_ctl(dec, OPUS_SET_GAIN(gain));
}
*/
import "C"
import (
	"fmt"
	"unsafe"
)

func packetPtr(packet []byte) (*C.uchar, C.opus_int32) {
	if len(packet) == 0 {
		return nil, 0
	}
	return (*C.uchar)(unsafe.Pointer(&packet[0])), C.opus_int32(len(packet))
}

func decodeResult(name string, n C.int) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("%s: %s", name, C.GoString(C.opus_strerror(n)))
	}
	return int(n), nil
}

// SetGain applies OPUS_SET_GAIN (Q8 dB).
func (d *Decoder) SetGain(gain int) error {
	_, err := decodeResult("OPUS_SET_GAIN", C.go_opus_decoder_set_gain(d.dec, C.int(gain)))
	return err
}

// Decode24 decodes one packet (or PLC for an empty one) with opus_decode24.
func (d *Decoder) Decode24(packet []byte, frameSize int, fec bool) ([]int32, error) {
	pcm := make([]int32, frameSize*d.channels)
	p, n := packetPtr(packet)
	ret, err := decodeResult("opus_decode24", C.opus_decode24(d.dec, p, n,
		(*C.opus_int32)(unsafe.Pointer(&pcm[0])), C.int(frameSize), boolToCInt(fec)))
	return pcm[:ret*d.channels], err
}

// SetGain applies OPUS_SET_GAIN (Q8 dB) to every stream.
func (d *MultistreamDecoder) SetGain(gain int) error {
	_, err := decodeResult("OPUS_SET_GAIN", C.go_opus_ms_decoder_set_gain(d.dec, C.int(gain)))
	return err
}

// Decode decodes one packet with opus_multistream_decode (int16, soft clip).
func (d *MultistreamDecoder) Decode(packet []byte, frameSize int, fec bool) ([]int16, error) {
	pcm := make([]int16, frameSize*d.channels)
	p, n := packetPtr(packet)
	ret, err := decodeResult("opus_multistream_decode", C.opus_multistream_decode(d.dec, p, n,
		(*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(frameSize), boolToCInt(fec)))
	return pcm[:ret*d.channels], err
}

// Decode24 decodes one packet with opus_multistream_decode24.
func (d *MultistreamDecoder) Decode24(packet []byte, frameSize int, fec bool) ([]int32, error) {
	pcm := make([]int32, frameSize*d.channels)
	p, n := packetPtr(packet)
	ret, err := decodeResult("opus_multistream_decode24", C.opus_multistream_decode24(d.dec, p, n,
		(*C.opus_int32)(unsafe.Pointer(&pcm[0])), C.int(frameSize), boolToCInt(fec)))
	return pcm[:ret*d.channels], err
}

// SetGain applies OPUS_SET_GAIN (Q8 dB).
func (d *ProjectionDecoder) SetGain(gain int) error {
	_, err := decodeResult("OPUS_SET_GAIN", C.go_opus_projection_decoder_set_gain(d.dec, C.int(gain)))
	return err
}

// Decode decodes one packet with opus_projection_decode.
func (d *ProjectionDecoder) Decode(packet []byte, frameSize int, fec bool) ([]int16, error) {
	pcm := make([]int16, frameSize*d.channels)
	p, n := packetPtr(packet)
	ret, err := decodeResult("opus_projection_decode", C.opus_projection_decode(d.dec, p, n,
		(*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(frameSize), boolToCInt(fec)))
	return pcm[:ret*d.channels], err
}

// Decode24 decodes one packet with opus_projection_decode24.
func (d *ProjectionDecoder) Decode24(packet []byte, frameSize int, fec bool) ([]int32, error) {
	pcm := make([]int32, frameSize*d.channels)
	p, n := packetPtr(packet)
	ret, err := decodeResult("opus_projection_decode24", C.opus_projection_decode24(d.dec, p, n,
		(*C.opus_int32)(unsafe.Pointer(&pcm[0])), C.int(frameSize), boolToCInt(fec)))
	return pcm[:ret*d.channels], err
}

// DecodeFloatPacket decodes one packet, or conceals with an empty one, with
// opus_projection_decode_float.
func (d *ProjectionDecoder) DecodeFloatPacket(packet []byte, frameSize int, fec bool) ([]float32, error) {
	pcm := make([]float32, frameSize*d.channels)
	p, n := packetPtr(packet)
	ret, err := decodeResult("opus_projection_decode_float", C.opus_projection_decode_float(d.dec, p, n,
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(frameSize), boolToCInt(fec)))
	return pcm[:ret*d.channels], err
}

// DecodeInt16 decodes one packet, or conceals with an empty one, with
// opus_decode.
func (d *Decoder) DecodeInt16(packet []byte, frameSize int, fec bool) ([]int16, error) {
	pcm := make([]int16, frameSize*d.channels)
	p, n := packetPtr(packet)
	ret, err := decodeResult("opus_decode", C.opus_decode(d.dec, p, n,
		(*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(frameSize), boolToCInt(fec)))
	return pcm[:ret*d.channels], err
}
