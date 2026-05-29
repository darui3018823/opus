package dsp

import (
	"math"
	"testing"
)

func TestIMDCTFormula(t *testing.T) {
	for _, N := range []int{2, 4, 8, 120, 240, 480, 960} {
		// Create a test spectral vector
		X := make([]float64, N)
		for k := range X {
			X[k] = math.Sin(float64(k+1) * 0.7)
		}

		// Reference MDCT-IV: y[n] = (2/N) * sum_k X[k]*cos(pi(2k+1)(2n+N+1)/(4N))
		yRef := make([]float64, N)
		for n := 0; n < N; n++ {
			sum := 0.0
			for k := 0; k < N; k++ {
				sum += X[k] * math.Cos(math.Pi*float64(2*k+1)*float64(2*n+N+1)/float64(4*N))
			}
			yRef[n] = 2.0 / float64(N) * sum
		}

		win := make([]float32, N)
		m := NewCELTMode(N, N, win)
		yOurs := m.IMDCT(X)

		maxErr := 0.0
		for i := range yRef {
			err := math.Abs(yRef[i] - yOurs[i])
			if err > maxErr {
				maxErr = err
			}
		}
		t.Logf("N=%d: max error = %e", N, maxErr)
		if maxErr > 1e-9 {
			t.Errorf("N=%d: IMDCT error %e exceeds threshold", N, maxErr)
		}
	}
}
