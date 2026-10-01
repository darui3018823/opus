package silk

import "testing"

func TestSilkFloat2IntUsesFloat32RoundToEven(t *testing.T) {
	tests := []struct {
		in   float64
		want int32
	}{
		{in: 0.5, want: 0},
		{in: 1.5, want: 2},
		{in: 2.5, want: 2},
		{in: -0.5, want: 0},
		{in: -1.5, want: -2},
		{in: -2.5, want: -2},
		{in: 1.5 + 1e-9, want: 2},
	}
	for _, test := range tests {
		if got := silkFloat2Int(test.in); got != test.want {
			t.Errorf("silkFloat2Int(%g)=%d, want %d", test.in, got, test.want)
		}
	}
}
