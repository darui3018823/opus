//go:build opusref

package cgoref

/*
#include <opus.h>
#include <opus_multistream.h>
#include <opus_projection.h>

static int go_projection_get_matrix_size(OpusProjectionEncoder *enc, opus_int32 *size) {
	return opus_projection_encoder_ctl(enc, OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE(size));
}

static int go_projection_get_matrix_gain(OpusProjectionEncoder *enc, opus_int32 *gain) {
	return opus_projection_encoder_ctl(enc, OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN(gain));
}

static int go_projection_get_matrix(OpusProjectionEncoder *enc, unsigned char *matrix, opus_int32 size) {
	return opus_projection_encoder_ctl(enc, OPUS_PROJECTION_GET_DEMIXING_MATRIX(matrix, size));
}

static int go_projection_set_bitrate(OpusProjectionEncoder *enc, opus_int32 bitrate) {
	return opus_projection_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
}

static int go_projection_set_vbr(OpusProjectionEncoder *enc, int enabled) {
	return opus_projection_encoder_ctl(enc, OPUS_SET_VBR(enabled));
}

static int go_ambisonics_ms_set_bitrate(OpusMSEncoder *enc, opus_int32 bitrate) {
	return opus_multistream_encoder_ctl(enc, OPUS_SET_BITRATE(bitrate));
}

static int go_ambisonics_ms_set_vbr(OpusMSEncoder *enc, int enabled) {
	return opus_multistream_encoder_ctl(enc, OPUS_SET_VBR(enabled));
}

static int go_ambisonics_ms_set_vbr_constraint(OpusMSEncoder *enc, int enabled) {
	return opus_multistream_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(enabled));
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type ProjectionEncoder struct {
	enc            *C.OpusProjectionEncoder
	channels       int
	streams        int
	coupledStreams int
}

type ProjectionDecoder struct {
	dec      *C.OpusProjectionDecoder
	channels int
}

type AmbisonicsMultistreamEncoder struct {
	enc            *C.OpusMSEncoder
	channels       int
	streams        int
	coupledStreams int
	mapping        []byte
}

func NewProjectionEncoder(sampleRate, channels, mappingFamily, application int) (*ProjectionEncoder, error) {
	var streams, coupledStreams, code C.int
	enc := C.opus_projection_ambisonics_encoder_create(
		C.opus_int32(sampleRate), C.int(channels), C.int(mappingFamily),
		&streams, &coupledStreams, C.int(application), &code)
	if code != 0 || enc == nil {
		return nil, fmt.Errorf("opus_projection_ambisonics_encoder_create: %s", C.GoString(C.opus_strerror(code)))
	}
	return &ProjectionEncoder{
		enc:            enc,
		channels:       channels,
		streams:        int(streams),
		coupledStreams: int(coupledStreams),
	}, nil
}

func (e *ProjectionEncoder) Streams() int        { return e.streams }
func (e *ProjectionEncoder) CoupledStreams() int { return e.coupledStreams }

func (e *ProjectionEncoder) SetBitrate(bitrate int) error {
	code := C.go_projection_set_bitrate(e.enc, C.opus_int32(bitrate))
	if code != 0 {
		return fmt.Errorf("projection OPUS_SET_BITRATE: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

func (e *ProjectionEncoder) SetVBR(enabled bool) error {
	code := C.go_projection_set_vbr(e.enc, boolToCInt(enabled))
	if code != 0 {
		return fmt.Errorf("projection OPUS_SET_VBR: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

func (e *ProjectionEncoder) DemixingMatrix() ([]byte, int, error) {
	var size, gain C.opus_int32
	if code := C.go_projection_get_matrix_size(e.enc, &size); code != 0 {
		return nil, 0, fmt.Errorf("projection matrix size: %s", C.GoString(C.opus_strerror(code)))
	}
	if code := C.go_projection_get_matrix_gain(e.enc, &gain); code != 0 {
		return nil, 0, fmt.Errorf("projection matrix gain: %s", C.GoString(C.opus_strerror(code)))
	}
	matrix := make([]byte, int(size))
	if code := C.go_projection_get_matrix(e.enc, (*C.uchar)(unsafe.Pointer(&matrix[0])), size); code != 0 {
		return nil, 0, fmt.Errorf("projection matrix: %s", C.GoString(C.opus_strerror(code)))
	}
	return matrix, int(gain), nil
}

func (e *ProjectionEncoder) EncodeFloat(pcm []float32, frameSize int) ([]byte, error) {
	need := frameSize * e.channels
	if len(pcm) < need {
		return nil, fmt.Errorf("insufficient PCM data: got %d, need %d", len(pcm), need)
	}
	out := make([]byte, 65536)
	n := C.opus_projection_encode_float(e.enc,
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(frameSize),
		(*C.uchar)(unsafe.Pointer(&out[0])), C.opus_int32(len(out)))
	if n < 0 {
		return nil, fmt.Errorf("opus_projection_encode_float: %s", C.GoString(C.opus_strerror(n)))
	}
	return out[:int(n)], nil
}

func (e *ProjectionEncoder) Close() {
	if e.enc != nil {
		C.opus_projection_encoder_destroy(e.enc)
		e.enc = nil
	}
}

func NewProjectionDecoder(sampleRate, channels, streams, coupledStreams int, matrix []byte) (*ProjectionDecoder, error) {
	if len(matrix) == 0 {
		return nil, fmt.Errorf("empty projection matrix")
	}
	var code C.int
	dec := C.opus_projection_decoder_create(
		C.opus_int32(sampleRate), C.int(channels), C.int(streams), C.int(coupledStreams),
		(*C.uchar)(unsafe.Pointer(&matrix[0])), C.opus_int32(len(matrix)), &code)
	if code != 0 || dec == nil {
		return nil, fmt.Errorf("opus_projection_decoder_create: %s", C.GoString(C.opus_strerror(code)))
	}
	return &ProjectionDecoder{dec: dec, channels: channels}, nil
}

func (d *ProjectionDecoder) DecodeFloat(packet []byte, maxSPC int) ([]float32, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("empty projection packet")
	}
	pcm := make([]float32, maxSPC*d.channels)
	n := C.opus_projection_decode_float(d.dec,
		(*C.uchar)(unsafe.Pointer(&packet[0])), C.opus_int32(len(packet)),
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(maxSPC), 0)
	if n < 0 {
		return nil, fmt.Errorf("opus_projection_decode_float: %s", C.GoString(C.opus_strerror(n)))
	}
	return pcm[:int(n)*d.channels], nil
}

func (d *ProjectionDecoder) Close() {
	if d.dec != nil {
		C.opus_projection_decoder_destroy(d.dec)
		d.dec = nil
	}
}

func NewAmbisonicsMultistreamEncoder(sampleRate, channels, mappingFamily, application int) (*AmbisonicsMultistreamEncoder, error) {
	mapping := make([]byte, channels)
	var streams, coupledStreams, code C.int
	enc := C.opus_multistream_surround_encoder_create(
		C.opus_int32(sampleRate), C.int(channels), C.int(mappingFamily),
		&streams, &coupledStreams, (*C.uchar)(unsafe.Pointer(&mapping[0])),
		C.int(application), &code)
	if code != 0 || enc == nil {
		return nil, fmt.Errorf("opus_multistream_surround_encoder_create: %s", C.GoString(C.opus_strerror(code)))
	}
	return &AmbisonicsMultistreamEncoder{
		enc:            enc,
		channels:       channels,
		streams:        int(streams),
		coupledStreams: int(coupledStreams),
		mapping:        mapping,
	}, nil
}

func (e *AmbisonicsMultistreamEncoder) Streams() int        { return e.streams }
func (e *AmbisonicsMultistreamEncoder) CoupledStreams() int { return e.coupledStreams }
func (e *AmbisonicsMultistreamEncoder) Mapping() []byte {
	return append([]byte(nil), e.mapping...)
}

func (e *AmbisonicsMultistreamEncoder) SetBitrate(bitrate int) error {
	code := C.go_ambisonics_ms_set_bitrate(e.enc, C.opus_int32(bitrate))
	if code != 0 {
		return fmt.Errorf("ambisonics OPUS_SET_BITRATE: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

func (e *AmbisonicsMultistreamEncoder) SetVBR(enabled bool) error {
	code := C.go_ambisonics_ms_set_vbr(e.enc, boolToCInt(enabled))
	if code != 0 {
		return fmt.Errorf("ambisonics OPUS_SET_VBR: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

func (e *AmbisonicsMultistreamEncoder) SetVBRConstraint(enabled bool) error {
	code := C.go_ambisonics_ms_set_vbr_constraint(e.enc, boolToCInt(enabled))
	if code != 0 {
		return fmt.Errorf("ambisonics OPUS_SET_VBR_CONSTRAINT: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

func (e *AmbisonicsMultistreamEncoder) EncodeFloat(pcm []float32, frameSize int) ([]byte, error) {
	need := frameSize * e.channels
	if len(pcm) < need {
		return nil, fmt.Errorf("insufficient PCM data: got %d, need %d", len(pcm), need)
	}
	out := make([]byte, 65536)
	n := C.opus_multistream_encode_float(e.enc,
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(frameSize),
		(*C.uchar)(unsafe.Pointer(&out[0])), C.opus_int32(len(out)))
	if n < 0 {
		return nil, fmt.Errorf("opus_multistream_encode_float: %s", C.GoString(C.opus_strerror(n)))
	}
	return out[:int(n)], nil
}

func (e *AmbisonicsMultistreamEncoder) Close() {
	if e.enc != nil {
		C.opus_multistream_encoder_destroy(e.enc)
		e.enc = nil
	}
}
