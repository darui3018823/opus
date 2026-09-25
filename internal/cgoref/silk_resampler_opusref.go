//go:build opusref

package cgoref

/*
#include <stdint.h>
#include <string.h>

typedef int16_t opus_int16;
typedef int32_t opus_int32;
typedef int opus_int;

// silk_resampler_state_struct is private to libopus; the real struct is
// 24 + 144 + 96 + 8 ints + a pointer bytes, so an oversized aligned buffer
// stands in for it.
typedef struct { int64_t raw[128]; } resampler_state_box;

extern opus_int silk_resampler_init(void *S, opus_int32 Fs_Hz_in, opus_int32 Fs_Hz_out, opus_int forEnc);
extern opus_int silk_resampler(void *S, opus_int16 out[], const opus_int16 in[], opus_int32 inLen);

static int box_init(resampler_state_box *b, int in, int out, int forEnc)
{
    memset(b, 0, sizeof(*b));
    return silk_resampler_init(b, in, out, forEnc);
}
static int box_process(resampler_state_box *b, opus_int16 *out, const opus_int16 *in, int inLen)
{
    return silk_resampler(b, out, in, inLen);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// SILKResampler wraps libopus' silk_resampler for reference comparisons.
type SILKResampler struct {
	box    C.resampler_state_box
	fsIn   int
	fsOut  int
	forEnc bool
}

// NewSILKResampler initializes silk_resampler_init(forEnc).
func NewSILKResampler(fsHzIn, fsHzOut int, forEnc bool) (*SILKResampler, error) {
	r := &SILKResampler{fsIn: fsHzIn, fsOut: fsHzOut, forEnc: forEnc}
	fe := 0
	if forEnc {
		fe = 1
	}
	if rc := C.box_init(&r.box, C.int(fsHzIn), C.int(fsHzOut), C.int(fe)); rc != 0 {
		return nil, fmt.Errorf("silk_resampler_init(%d, %d, %d) = %d", fsHzIn, fsHzOut, fe, rc)
	}
	return r, nil
}

// Process runs silk_resampler on in and returns len(in)*out/in samples.
func (r *SILKResampler) Process(in []int16) ([]int16, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("empty input")
	}
	n := len(in) * r.fsOut / r.fsIn
	out := make([]int16, n+64)
	if rc := C.box_process(&r.box, (*C.opus_int16)(unsafe.Pointer(&out[0])), (*C.opus_int16)(unsafe.Pointer(&in[0])), C.int(len(in))); rc != 0 {
		return nil, fmt.Errorf("silk_resampler = %d", rc)
	}
	return out[:n], nil
}
