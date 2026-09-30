//go:build opusref

package cgoref

/*
#include <opus.h>
*/
import "C"

import "unsafe"

// PacketParse runs libopus opus_packet_parse and returns its result (the
// frame count or a negative error), the TOC byte, the frame sizes and the
// payload offset.
func PacketParse(data []byte) (ret int, toc byte, sizes []int, payloadOffset int) {
	var ctoc C.uchar
	var csize [48]C.opus_int16
	var coff C.int
	var ptr *C.uchar
	if len(data) > 0 {
		ptr = (*C.uchar)(unsafe.Pointer(&data[0]))
	} else {
		var zero C.uchar
		ptr = &zero
	}
	ret = int(C.opus_packet_parse(ptr, C.opus_int32(len(data)), &ctoc, nil, &csize[0], &coff))
	if ret < 0 {
		return ret, 0, nil, 0
	}
	sizes = make([]int, ret)
	for i := range sizes {
		sizes[i] = int(csize[i])
	}
	return ret, byte(ctoc), sizes, int(coff)
}
