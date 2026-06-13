package entcode

import (
	"fmt"
	"testing"
)

func TestLaplaceRoundtrip(t *testing.T) {
	fs0 := uint32(72) << 7  // band 0, LM=3 inter
	decay := int(127) << 6

	for _, val := range []int{0, 1, -1, 2, -2, 5, -5, 10, -10, 16, -16} {
		enc := NewEncoder(64)
		v := val
		enc.EncodeLaplace(&v, fs0, decay)
		enc.Flush()
		dec := NewDecoder(enc.Bytes())
		got := dec.DecodeLaplace(fs0, decay)
		if got != v {
			t.Errorf("val=%d clamped=%d decoded=%d MISMATCH", val, v, got)
		} else {
			fmt.Printf("val=%4d clamped=%4d decoded=%4d OK\n", val, v, got)
		}
	}
}
