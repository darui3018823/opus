package celt

import "testing"

func TestApplyDeemphasisMatchesLibopusFloatState(t *testing.T) {
	d, err := NewDecoder(120, 48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	samples := []float64{0, 32768, -16384, 8192, -4096}
	want := make([]float32, len(samples))
	const (
		coef      = float32(0.85)
		verySmall = float32(1e-30)
	)
	var wantMem float32
	for i := range samples {
		y := float32(samples[i]) + verySmall
		y += wantMem
		wantMem = coef * y
		want[i] = y
	}

	d.applyDeemphasis(0, samples)
	for i := range samples {
		if got := float32(samples[i]); got != want[i] {
			t.Errorf("sample %d = %g, want %g", i, got, want[i])
		}
	}
	if got := d.preemphMem[0]; got != wantMem {
		t.Errorf("memory = %g, want %g", got, wantMem)
	}
}
