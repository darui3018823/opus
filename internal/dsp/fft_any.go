package dsp

import (
	"math"
	"sync"
)

type bluesteinPlan struct {
	n         int
	m         int
	chirp     []Complex
	kernelFFT []Complex
	fft       *FFTConfig
}

var bluesteinPlans sync.Map

func getBluesteinPlan(n int) *bluesteinPlan {
	if cached, ok := bluesteinPlans.Load(n); ok {
		return cached.(*bluesteinPlan)
	}
	m := nextPow2(2*n - 1)
	plan := &bluesteinPlan{
		n:     n,
		m:     m,
		chirp: make([]Complex, n),
	}
	for i := 0; i < n; i++ {
		ang := math.Pi * float64(i) * float64(i) / float64(n)
		plan.chirp[i] = Complex{Real: math.Cos(ang), Imag: math.Sin(ang)}
	}
	kernel := make([]Complex, m)
	for i := 0; i < n; i++ {
		kernel[i] = plan.chirp[i]
	}
	for i := m - n + 1; i < m; i++ {
		kernel[i] = plan.chirp[m-i]
	}
	plan.fft, _ = NewFFTConfig(m)
	plan.kernelFFT = kernel
	_ = plan.fft.ExecuteInPlace(plan.kernelFFT)
	actual, _ := bluesteinPlans.LoadOrStore(n, plan)
	return actual.(*bluesteinPlan)
}

// nextPow2 returns the smallest power of 2 >= n.
func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// AnyFFT computes the DFT of x using Bluestein's chirp-Z algorithm.
// Works for any length N (not restricted to powers of 2).
func AnyFFT(x []Complex) []Complex {
	N := len(x)
	if N == 0 {
		return nil
	}
	if IsPowerOf2(N) {
		out, _ := FFT(x)
		return out
	}

	plan := getBluesteinPlan(N)
	work := make([]Complex, plan.m)
	for n := 0; n < N; n++ {
		work[n] = x[n].Mul(plan.chirp[n].Conj())
	}
	_ = plan.fft.ExecuteInPlace(work)
	for i := 0; i < plan.m; i++ {
		work[i] = work[i].Mul(plan.kernelFFT[i]).Conj()
	}
	_ = plan.fft.ExecuteInPlace(work)
	scale := 1.0 / float64(plan.m)

	out := make([]Complex, N)
	for k := 0; k < N; k++ {
		c := work[k].Conj().MulScalar(scale)
		out[k] = plan.chirp[k].Conj().Mul(c)
	}
	return out
}

// AnyIFFT computes the inverse DFT of x using AnyFFT.
func AnyIFFT(x []Complex) []Complex {
	N := len(x)
	// Conjugate, forward DFT, conjugate + scale.
	conj := make([]Complex, N)
	for i, c := range x {
		conj[i] = c.Conj()
	}
	out := AnyFFT(conj)
	scale := 1.0 / float64(N)
	for i := range out {
		out[i] = out[i].Conj().MulScalar(scale)
	}
	return out
}
