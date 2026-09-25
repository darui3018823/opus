package opus

import "testing"

func TestFloatToInt16MatchesLibopusFloat2Int16(t *testing.T) {
	tests := []struct {
		name string
		lsb  float64
		want int16
	}{
		{name: "zero", lsb: 0, want: 0},
		{name: "positive below half", lsb: 0.49, want: 0},
		{name: "negative below half", lsb: -0.49, want: 0},
		{name: "positive half to even zero", lsb: 0.5, want: 0},
		{name: "negative half to even zero", lsb: -0.5, want: 0},
		{name: "positive one and half", lsb: 1.5, want: 2},
		{name: "negative one and half", lsb: -1.5, want: -2},
		{name: "positive two and half to even", lsb: 2.5, want: 2},
		{name: "negative two and half to even", lsb: -2.5, want: -2},
		{name: "positive ordinary rounding", lsb: 7.6, want: 8},
		{name: "negative ordinary rounding", lsb: -7.6, want: -8},
		{name: "positive saturation", lsb: 32768, want: 32767},
		{name: "negative saturation", lsb: -32769, want: -32768},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dst := []int16{123}
			floatToInt16(dst, []float64{tc.lsb / 32768})
			if dst[0] != tc.want {
				t.Fatalf("floatToInt16(%g LSB) = %d, want %d", tc.lsb, dst[0], tc.want)
			}
		})
	}
}
