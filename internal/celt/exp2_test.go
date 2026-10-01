package celt

import (
	"math"
	"testing"
)

func TestCELTExp2RoundedFloat32(t *testing.T) {
	tests := []struct {
		x    uint32
		want uint32
	}{
		{x: 0x4055e45e, want: 0x41223fad},
		{x: 0x405567da, want: 0x41216573},
		{x: 0x4047a8f4, want: 0x410b1268},
		{x: 0xc061cf80, want: 0x3db1811a},
	}
	for _, test := range tests {
		x := math.Float32frombits(test.x)
		got := math.Float32bits(celtExp2RoundedFloat32(x))
		if got != test.want {
			t.Errorf("2**(%08x)=%08x, want %08x", test.x, got, test.want)
		}
	}
}
