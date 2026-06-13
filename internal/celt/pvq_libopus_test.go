package celt

import "testing"

func TestCwrsiLibopusCollapseMasks(t *testing.T) {
	tests := []struct {
		name      string
		n, k, idx int
		B         int
		wantMask  uint
	}{
		{name: "tv07_band0", n: 8, k: 11, idx: 653774, B: 8, wantMask: 244},
		{name: "tv07_band1", n: 8, k: 9, idx: 204284, B: 8, wantMask: 246},
		{name: "tv07_band8", n: 16, k: 7, idx: 2210540, B: 8, wantMask: 214},
		{name: "tv07_band12", n: 32, k: 7, idx: 618907197, B: 8, wantMask: 214},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			y := cwrsiLibopus(tt.n, tt.k, uint32(tt.idx))
			sum := 0
			for _, v := range y {
				if v < 0 {
					sum -= v
				} else {
					sum += v
				}
			}
			if sum != tt.k {
				t.Fatalf("pulse sum=%d, want %d; y=%v", sum, tt.k, y)
			}
			if got := extractCollapseMask(y, tt.n, tt.B); got != tt.wantMask {
				t.Fatalf("mask=%d, want %d; y=%v", got, tt.wantMask, y)
			}
		})
	}
}
