//go:build opusref

// Package cgoref wraps libopus via CGO for golden-test comparisons.
package cgoref

/*
#cgo CFLAGS: -I${SRCDIR}/../../libopus/include -IC:/msys64/mingw64/include/opus
#cgo LDFLAGS: -LC:/msys64/mingw64/lib -lopus
#include <opus.h>
#include <opus_multistream.h>
#include <stdlib.h>

static int go_opus_encoder_set_bitrate(OpusEncoder *enc, int bps) {
	return opus_encoder_ctl(enc, OPUS_SET_BITRATE(bps));
}

static int go_opus_encoder_set_complexity(OpusEncoder *enc, int complexity) {
	return opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(complexity));
}

static int go_opus_encoder_set_vbr(OpusEncoder *enc, int enabled) {
	return opus_encoder_ctl(enc, OPUS_SET_VBR(enabled));
}

static int go_opus_encoder_set_vbr_constraint(OpusEncoder *enc, int constrained) {
	return opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(constrained));
}

static int go_opus_encoder_set_bandwidth(OpusEncoder *enc, int bandwidth) {
	return opus_encoder_ctl(enc, OPUS_SET_BANDWIDTH(bandwidth));
}

static int go_opus_encoder_set_voice(OpusEncoder *enc) {
	return opus_encoder_ctl(enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));
}

static int go_opus_encoder_set_inband_fec(OpusEncoder *enc, int enabled) {
	return opus_encoder_ctl(enc, OPUS_SET_INBAND_FEC(enabled));
}

static int go_opus_encoder_set_packet_loss(OpusEncoder *enc, int perc) {
	return opus_encoder_ctl(enc, OPUS_SET_PACKET_LOSS_PERC(perc));
}

static int go_opus_decoder_disable_osce_bwe(OpusDecoder *dec) {
#ifdef OPUS_SET_OSCE_BWE_REQUEST
	int ret = opus_decoder_ctl(dec, OPUS_SET_OSCE_BWE(0));
	return ret == OPUS_UNIMPLEMENTED ? 0 : ret;
#else
	(void)dec;
	return 0;
#endif
}

static int go_opus_decoder_get_final_range(OpusDecoder *dec, opus_uint32 *rng) {
	return opus_decoder_ctl(dec, OPUS_GET_FINAL_RANGE(rng));
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

// MultistreamEncoder wraps a libopus OpusMSEncoder.
type MultistreamEncoder struct {
	enc      *C.OpusMSEncoder
	channels int
}

// MultistreamDecoder wraps a libopus OpusMSDecoder.
type MultistreamDecoder struct {
	dec      *C.OpusMSDecoder
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
	code = C.go_opus_decoder_disable_osce_bwe(dec)
	if code != 0 {
		C.opus_decoder_destroy(dec)
		return nil, fmt.Errorf("OPUS_SET_OSCE_BWE(0): %s", C.GoString(C.opus_strerror(code)))
	}
	return &Decoder{dec: dec, channels: channels}, nil
}

// NewMultistreamEncoder creates a libopus multistream encoder.
func NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte, application int) (*MultistreamEncoder, error) {
	if len(mapping) != channels || len(mapping) == 0 {
		return nil, fmt.Errorf("invalid mapping length %d for %d channels", len(mapping), channels)
	}
	var code C.int
	enc := C.opus_multistream_encoder_create(
		C.opus_int32(sampleRate), C.int(channels), C.int(streams), C.int(coupledStreams),
		(*C.uchar)(unsafe.Pointer(&mapping[0])), C.int(application), &code)
	if code != 0 {
		return nil, fmt.Errorf("opus_multistream_encoder_create: %s", C.GoString(C.opus_strerror(code)))
	}
	return &MultistreamEncoder{enc: enc, channels: channels}, nil
}

// NewMultistreamDecoder creates a libopus multistream decoder.
func NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*MultistreamDecoder, error) {
	if len(mapping) != channels || len(mapping) == 0 {
		return nil, fmt.Errorf("invalid mapping length %d for %d channels", len(mapping), channels)
	}
	var code C.int
	dec := C.opus_multistream_decoder_create(
		C.opus_int32(sampleRate), C.int(channels), C.int(streams), C.int(coupledStreams),
		(*C.uchar)(unsafe.Pointer(&mapping[0])), &code)
	if code != 0 {
		return nil, fmt.Errorf("opus_multistream_decoder_create: %s", C.GoString(C.opus_strerror(code)))
	}
	return &MultistreamDecoder{dec: dec, channels: channels}, nil
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

// SetVBR enables or disables variable bitrate encoding.
func (e *Encoder) SetVBR(enabled bool) error {
	code := C.go_opus_encoder_set_vbr(e.enc, boolToCInt(enabled))
	if code != 0 {
		return fmt.Errorf("OPUS_SET_VBR: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

// SetVBRConstraint enables or disables constrained VBR.
func (e *Encoder) SetVBRConstraint(constrained bool) error {
	code := C.go_opus_encoder_set_vbr_constraint(e.enc, boolToCInt(constrained))
	if code != 0 {
		return fmt.Errorf("OPUS_SET_VBR_CONSTRAINT: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

// SetBandwidth forces the libopus encoder bandwidth.
func (e *Encoder) SetBandwidth(bandwidth int) error {
	code := C.go_opus_encoder_set_bandwidth(e.enc, C.int(bandwidth))
	if code != 0 {
		return fmt.Errorf("OPUS_SET_BANDWIDTH: %s", C.GoString(C.opus_strerror(code)))
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

// SetInbandFEC enables or disables libopus inband FEC (LBRR).
func (e *Encoder) SetInbandFEC(enabled bool) error {
	code := C.go_opus_encoder_set_inband_fec(e.enc, boolToCInt(enabled))
	if code != 0 {
		return fmt.Errorf("OPUS_SET_INBAND_FEC: %s", C.GoString(C.opus_strerror(code)))
	}
	return nil
}

// SetPacketLossPerc sets the libopus expected packet-loss percentage (0..100).
func (e *Encoder) SetPacketLossPerc(perc int) error {
	code := C.go_opus_encoder_set_packet_loss(e.enc, C.int(perc))
	if code != 0 {
		return fmt.Errorf("OPUS_SET_PACKET_LOSS_PERC: %s", C.GoString(C.opus_strerror(code)))
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

// Encode encodes one float32 frame with libopus multistream.
func (e *MultistreamEncoder) Encode(pcm []float32, frameSize int) ([]byte, error) {
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

// Close frees the libopus multistream encoder.
func (e *MultistreamEncoder) Close() {
	if e.enc != nil {
		C.opus_multistream_encoder_destroy(e.enc)
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

// DecodeFloatFEC reconstructs the lost frame preceding packet via libopus'
// in-band FEC path (opus_decode_float with decode_fec=1). packet must be the
// next received packet (which carries the LBRR redundancy); frameSize is the
// number of samples per channel of the lost frame. Returns samples per channel.
func (d *Decoder) DecodeFloatFEC(packet []byte, frameSize int) ([]float32, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("empty packet for FEC decode")
	}
	pcm := make([]float32, frameSize*d.channels)
	n := C.opus_decode_float(d.dec,
		(*C.uchar)(unsafe.Pointer(&packet[0])), C.opus_int32(len(packet)),
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(frameSize), 1)
	if n < 0 {
		return nil, fmt.Errorf("opus_decode_float(FEC): %s", C.GoString(C.opus_strerror(n)))
	}
	return pcm[:int(n)*d.channels], nil
}

// FinalRange returns the entropy decoder's final range for the last packet.
func (d *Decoder) FinalRange() (uint32, error) {
	var rng C.opus_uint32
	code := C.go_opus_decoder_get_final_range(d.dec, &rng)
	if code != 0 {
		return 0, fmt.Errorf("OPUS_GET_FINAL_RANGE: %s", C.GoString(C.opus_strerror(code)))
	}
	return uint32(rng), nil
}

// Close frees the libopus decoder.
func (d *Decoder) Close() {
	if d.dec != nil {
		C.opus_decoder_destroy(d.dec)
		d.dec = nil
	}
}

// DecodeFloat decodes one packet with libopus multistream.
func (d *MultistreamDecoder) DecodeFloat(packet []byte, maxSPC int) ([]float32, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("empty multistream packet")
	}
	pcm := make([]float32, maxSPC*d.channels)
	n := C.opus_multistream_decode_float(d.dec,
		(*C.uchar)(unsafe.Pointer(&packet[0])), C.opus_int32(len(packet)),
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(maxSPC), 0)
	if n < 0 {
		return nil, fmt.Errorf("opus_multistream_decode_float: %s", C.GoString(C.opus_strerror(n)))
	}
	return pcm[:int(n)*d.channels], nil
}

// Close frees the libopus multistream decoder.
func (d *MultistreamDecoder) Close() {
	if d.dec != nil {
		C.opus_multistream_decoder_destroy(d.dec)
		d.dec = nil
	}
}

// Version returns the libopus version string.
func Version() string {
	return C.GoString(C.opus_get_version_string())
}

func boolToCInt(v bool) C.int {
	if v {
		return 1
	}
	return 0
}
