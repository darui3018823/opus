package dsp

import "math"

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

	// Choose M = next power of 2 >= 2N - 1 for zero-padded convolution.
	M := nextPow2(2*N - 1)

	// Chirp factor: W[n] = exp(+i*pi*n²/N)
	W := make([]Complex, N)
	for n := 0; n < N; n++ {
		ang := math.Pi * float64(n) * float64(n) / float64(N)
		W[n] = Complex{Real: math.Cos(ang), Imag: math.Sin(ang)}
	}

	// a[n] = x[n] * conj(W[n])  (= x[n] * exp(-i*pi*n²/N))
	a := make([]Complex, M)
	for n := 0; n < N; n++ {
		a[n] = x[n].Mul(W[n].Conj())
	}

	// b[m] = chirp convolution kernel: W[m] for m in [0,N), W[N-m] for m in (M-N+1,M), else 0.
	b := make([]Complex, M)
	for m := 0; m < N; m++ {
		b[m] = W[m]
	}
	for m := M - N + 1; m < M; m++ {
		b[m] = W[M-m]
	}

	// Transform a and b; multiply; inverse transform.
	cfg, _ := NewFFTConfig(M)
	A, _ := cfg.Execute(a)
	B, _ := cfg.Execute(b)
	AB := make([]Complex, M)
	for i := 0; i < M; i++ {
		AB[i] = A[i].Mul(B[i])
	}
	c, _ := cfg.ExecuteInverse(AB)

	// DFT[k] = conj(W[k]) * c[k], for k = 0..N-1.
	out := make([]Complex, N)
	for k := 0; k < N; k++ {
		out[k] = W[k].Conj().Mul(c[k])
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
