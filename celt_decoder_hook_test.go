package opus

import "github.com/darui3018823/opus/internal/celt"

// forEachCELTDecoder applies fn to every CELT decoder dec has created and to
// each one it creates later, for tests that hook the CELT decode stages.
func forEachCELTDecoder(dec *Decoder, fn func(*celt.Decoder)) {
	dec.celtDecoderHook = fn
	for bw := range dec.celtDecoders {
		for _, c := range dec.celtDecoders[bw] {
			if c != nil {
				fn(c)
			}
		}
	}
}
