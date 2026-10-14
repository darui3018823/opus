package entcode

import "testing"

func TestSetTellMovesBothWays(t *testing.T) {
	dec := NewDecoder(make([]byte, 16))
	dec.SetTell(100)
	if got := dec.ECTell(); got != 100 {
		t.Fatalf("ECTell after SetTell(100)=%d", got)
	}
	// A hybrid frame whose SILK layer overran the budget is pulled back.
	dec.SetTell(40)
	if got := dec.ECTell(); got != 40 {
		t.Fatalf("ECTell after SetTell(40)=%d", got)
	}
}

func TestDecodeUintClampsOutOfRangeValues(t *testing.T) {
	// ft > 256 reads its low bits raw; an all-ones buffer yields a value
	// above ft-1, which ec_dec_uint clamps to ft-1.
	data := make([]byte, 8)
	for i := range data {
		data[i] = 0xFF
	}
	const ft = 1000
	for i := 0; i < 4; i++ {
		dec := NewDecoder(data)
		for j := 0; j < i; j++ {
			dec.DecodeBits(3)
		}
		if got := dec.DecodeUint(ft); got > ft-1 {
			t.Fatalf("DecodeUint(%d)=%d exceeds ft-1", ft, got)
		}
	}
}
