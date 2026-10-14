//go:build opusref

package cgoref

/*
#cgo CFLAGS: -I${SRCDIR}/../../libopus/include -IC:/msys64/mingw64/include/opus
#include <opus.h>
#include <opus_multistream.h>

static int go_opus_ms_encoder_set_vbr(OpusMSEncoder *enc, int vbr) {
	return opus_multistream_encoder_ctl(enc, OPUS_SET_VBR(vbr));
}
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// SetVBR applies OPUS_SET_VBR to every stream.
func (e *MultistreamEncoder) SetVBR(vbr bool) error {
	if r := C.go_opus_ms_encoder_set_vbr(e.enc, boolToCInt(vbr)); r != 0 {
		return fmt.Errorf("OPUS_SET_VBR: %s", C.GoString(C.opus_strerror(r)))
	}
	return nil
}

// EncodeMax encodes one float32 frame into a buffer of maxBytes
// (opus_multistream_encode_float's max_data_bytes).
func (e *MultistreamEncoder) EncodeMax(pcm []float32, frameSize, maxBytes int) ([]byte, error) {
	out := make([]byte, maxBytes)
	n := C.opus_multistream_encode_float(e.enc,
		(*C.float)(unsafe.Pointer(&pcm[0])), C.int(frameSize),
		(*C.uchar)(unsafe.Pointer(&out[0])), C.opus_int32(maxBytes))
	if n < 0 {
		return nil, fmt.Errorf("opus_multistream_encode_float: %s", C.GoString(C.opus_strerror(n)))
	}
	return out[:int(n)], nil
}
