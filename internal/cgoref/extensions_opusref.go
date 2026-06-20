//go:build opusref

package cgoref

/*
#cgo CFLAGS: -I${SRCDIR}/../../libopus/include -I${SRCDIR}/../../libopus/celt -I${SRCDIR}/../../libopus/src
#include <stdlib.h>
#include "../../libopus/src/extensions.c"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// PacketExtension mirrors libopus' private opus_extension_data for oracle
// comparisons of the public Go packet-extension implementation.
type PacketExtension struct {
	ID    int
	Frame int
	Data  []byte
}

// GenerateExtensions runs bundled libopus' opus_packet_extensions_generate.
func GenerateExtensions(extensions []PacketExtension, nbFrames, targetLen int) ([]byte, error) {
	if targetLen < 0 {
		return nil, fmt.Errorf("negative target length %d", targetLen)
	}
	var cexts *C.opus_extension_data
	var blocks []unsafe.Pointer
	if len(extensions) > 0 {
		size := C.size_t(len(extensions)) * C.size_t(unsafe.Sizeof(C.opus_extension_data{}))
		cexts = (*C.opus_extension_data)(C.malloc(size))
		if cexts == nil {
			return nil, fmt.Errorf("malloc extension array")
		}
		defer C.free(unsafe.Pointer(cexts))
		array := unsafe.Slice(cexts, len(extensions))
		for i, ext := range extensions {
			array[i].id = C.int(ext.ID)
			array[i].frame = C.int(ext.Frame)
			array[i].len = C.opus_int32(len(ext.Data))
			if len(ext.Data) > 0 {
				block := C.CBytes(ext.Data)
				blocks = append(blocks, block)
				array[i].data = (*C.uchar)(block)
			}
		}
		defer func() {
			for _, block := range blocks {
				C.free(block)
			}
		}()
	}

	outLen := targetLen
	if outLen == 0 {
		outLen = 1
		for {
			ret := C.opus_packet_extensions_generate(nil, C.opus_int32(outLen), cexts, C.opus_int32(len(extensions)), C.int(nbFrames), 0)
			if ret >= 0 {
				outLen = int(ret)
				break
			}
			if int(ret) != -2 {
				return nil, fmt.Errorf("opus_packet_extensions_generate size: %d", int(ret))
			}
			outLen *= 2
		}
	}
	if outLen == 0 {
		return []byte{}, nil
	}
	out := make([]byte, outLen)
	pad := 0
	if targetLen > 0 {
		pad = 1
	}
	ret := C.opus_packet_extensions_generate(
		(*C.uchar)(unsafe.Pointer(&out[0])), C.opus_int32(len(out)),
		cexts, C.opus_int32(len(extensions)), C.int(nbFrames), C.int(pad),
	)
	if ret < 0 {
		return nil, fmt.Errorf("opus_packet_extensions_generate: %d", int(ret))
	}
	return out[:int(ret)], nil
}
