package silk

import (
	"fmt"
	"math"
)

// LPC (Linear Predictive Coding) analyzer for SILK.
// LPC models the vocal tract as a linear filter.

// LPCAnalysis performs LPC analysis on a frame of audio.
type LPCAnalysis struct {
	order      int       // LPC order (10-18 depending on sample rate)
	coeffs     []float64 // LPC coefficients
	residual   []float64 // Prediction residual
	reflection []float64 // Reflection coefficients (PARCOR)
	gain       float64   // Prediction gain
}

// NewLPCAnalysis creates a new LPC analyzer.
func NewLPCAnalysis(order int) *LPCAnalysis {
	return &LPCAnalysis{
		order:      order,
		coeffs:     make([]float64, order),
		reflection: make([]float64, order),
		gain:       1.0,
	}
}

// Analyze performs LPC analysis using Levinson-Durbin recursion.
// This extracts the spectral envelope of speech.
func (lpc *LPCAnalysis) Analyze(signal []float64) error {
	return lpc.AnalyzeWindowed(signal)
}

// AnalyzeWindowed performs LPC analysis on a tapered frame. SILK derives its
// NLSF target from windowed autocorrelation rather than from the raw frame; the
// taper keeps frame edges from dominating the spectral envelope estimate.
func (lpc *LPCAnalysis) AnalyzeWindowed(signal []float64) error {
	n := len(signal)
	if n < lpc.order+1 {
		return fmt.Errorf("signal too short for LPC order %d", lpc.order)
	}

	windowed := windowForLPC(signal)

	// Compute autocorrelation
	autocorr := make([]float64, lpc.order+1)
	for lag := 0; lag <= lpc.order; lag++ {
		sum := 0.0
		for i := lag; i < n; i++ {
			sum += windowed[i] * windowed[i-lag]
		}
		// A light lag window follows the same intent as libopus's bandwidth
		// expansion: keep the LPC filter comfortably inside the unit circle.
		if lag > 0 {
			sum *= math.Exp(-0.5 * math.Pow(0.03*float64(lag), 2))
		}
		autocorr[lag] = sum
	}

	// Apply small constant to avoid division by zero
	if autocorr[0] < 1e-10 {
		autocorr[0] = 1e-10
	}

	// Levinson-Durbin algorithm
	err := autocorr[0]
	workCoeffs := make([]float64, lpc.order+1)
	workCoeffs[0] = 1.0

	for i := 0; i < lpc.order; i++ {
		// Compute reflection coefficient
		lambda := 0.0
		for j := 0; j <= i; j++ {
			lambda -= workCoeffs[j] * autocorr[i+1-j]
		}
		lambda /= err

		lpc.reflection[i] = lambda

		// Update LPC coefficients
		newCoeffs := make([]float64, lpc.order+1)
		copy(newCoeffs, workCoeffs)
		newCoeffs[i+1] = lambda

		for j := 1; j <= i; j++ {
			newCoeffs[j] = workCoeffs[j] + lambda*workCoeffs[i+1-j]
		}

		copy(workCoeffs, newCoeffs)

		// Update error
		err *= (1.0 - lambda*lambda)
		if err < 1e-10 {
			err = 1e-10
		}
	}

	// Copy final coefficients (skip the leading 1.0)
	for i := 0; i < lpc.order; i++ {
		chirp := math.Pow(0.985, float64(i+1))
		lpc.coeffs[i] = workCoeffs[i+1] * chirp
	}

	lpc.gain = math.Sqrt(err)
	return nil
}

func windowForLPC(signal []float64) []float64 {
	out := make([]float64, len(signal))
	if len(signal) == 0 {
		return out
	}
	taper := len(signal) / 5
	if taper < 8 {
		taper = 8
	}
	if taper*2 > len(signal) {
		taper = len(signal) / 2
	}
	for i, v := range signal {
		w := 1.0
		switch {
		case i < taper:
			w = 0.5 - 0.5*math.Cos(math.Pi*float64(i+1)/float64(taper+1))
		case i >= len(signal)-taper:
			j := len(signal) - i
			w = 0.5 - 0.5*math.Cos(math.Pi*float64(j)/float64(taper+1))
		}
		out[i] = v * w
	}
	return out
}

// ComputeResidual computes the LPC residual (excitation signal).
func (lpc *LPCAnalysis) ComputeResidual(signal []float64) []float64 {
	n := len(signal)
	residual := make([]float64, n)

	for i := 0; i < n; i++ {
		pred := 0.0
		for j := 0; j < len(lpc.coeffs) && j < i; j++ {
			pred += lpc.coeffs[j] * signal[i-j-1]
		}
		residual[i] = signal[i] - pred
	}

	return residual
}

// Synthesize reconstructs signal from LPC coefficients and residual.
func (lpc *LPCAnalysis) Synthesize(residual []float64) []float64 {
	n := len(residual)
	signal := make([]float64, n)

	for i := 0; i < n; i++ {
		pred := 0.0
		for j := 0; j < len(lpc.coeffs) && j < i; j++ {
			pred += lpc.coeffs[j] * signal[i-j-1]
		}
		signal[i] = residual[i] + pred
	}

	return signal
}

// ToLSF converts LPC coefficients to Line Spectral Frequencies.
// LSFs are better for quantization and interpolation.
func (lpc *LPCAnalysis) ToLSF() []float64 {
	order := lpc.order
	lsf := make([]float64, order)

	// Form P(z) and Q(z) polynomials
	p := make([]float64, order+2)
	q := make([]float64, order+2)

	p[0] = 1.0
	q[0] = 1.0

	for i := 0; i < order; i++ {
		p[i+1] = lpc.coeffs[i]
		q[i+1] = lpc.coeffs[i]
	}

	// Add symmetric and antisymmetric components
	for i := 1; i <= order; i++ {
		p[i] = p[i] + p[order-i+1]
		q[i] = q[i] - q[order-i+1]
	}

	// Find roots using Chebyshev polynomial evaluation
	// (Simplified version - full implementation would use root finding)
	for i := 0; i < order; i++ {
		// Distribute roots evenly as placeholder
		// Real implementation would solve polynomial roots
		lsf[i] = math.Pi * float64(i+1) / float64(order+1)
	}

	return lsf
}

// FromLSF converts Line Spectral Frequencies back to LPC coefficients
// using the standard P(z)/Q(z) Chebyshev polynomial reconstruction.
func (lpc *LPCAnalysis) FromLSF(lsf []float64) error {
	if len(lsf) != lpc.order {
		return fmt.Errorf("LSF length %d doesn't match LPC order %d", len(lsf), lpc.order)
	}
	order := lpc.order
	halfOrder := order / 2

	// Build P'(z) from even-indexed LSFs (w_0, w_2, ...)
	pp := []float64{1.0}
	for k := 0; k < halfOrder; k++ {
		c := -2.0 * math.Cos(lsf[2*k])
		newPP := make([]float64, len(pp)+2)
		for i, v := range pp {
			newPP[i] += v
			newPP[i+1] += c * v
			newPP[i+2] += v
		}
		pp = newPP
	}

	// Build Q'(z) from odd-indexed LSFs (w_1, w_3, ...)
	qp := []float64{1.0}
	for k := 0; k < halfOrder; k++ {
		c := -2.0 * math.Cos(lsf[2*k+1])
		newQP := make([]float64, len(qp)+2)
		for i, v := range qp {
			newQP[i] += v
			newQP[i+1] += c * v
			newQP[i+2] += v
		}
		qp = newQP
	}

	// P(z) = P'(z) * (1 + z^{-1})
	p := make([]float64, len(pp)+1)
	for i, v := range pp {
		p[i] += v
		p[i+1] += v
	}

	// Q(z) = Q'(z) * (1 - z^{-1})
	q := make([]float64, len(qp)+1)
	for i, v := range qp {
		q[i] += v
		q[i+1] -= v
	}

	// A(z) = 0.5*(P(z)+Q(z)), take coefficients a[1..order]
	maxLen := len(p)
	if len(q) > maxLen {
		maxLen = len(q)
	}
	a := make([]float64, maxLen)
	for i := range p {
		a[i] += p[i]
	}
	for i := range q {
		a[i] += q[i]
	}
	for i := 0; i < order && i+1 < len(a); i++ {
		lpc.coeffs[i] = 0.5 * a[i+1]
	}
	return nil
}

// SynthesizeWithHistory reconstructs signal using previous frame's output as history.
func (lpc *LPCAnalysis) SynthesizeWithHistory(residual, history []float64) []float64 {
	n := len(residual)
	signal := make([]float64, n)
	order := len(lpc.coeffs)
	for i := 0; i < n; i++ {
		pred := 0.0
		for j := 0; j < order; j++ {
			idx := i - j - 1
			var past float64
			if idx >= 0 {
				past = signal[idx]
			} else if hi := len(history) + idx; hi >= 0 && hi < len(history) {
				past = history[hi]
			}
			pred += lpc.coeffs[j] * past
		}
		signal[i] = residual[i] + pred
	}
	return signal
}

// GetCoefficients returns the LPC coefficients.
func (lpc *LPCAnalysis) GetCoefficients() []float64 {
	return lpc.coeffs
}

// GetReflectionCoefficients returns reflection coefficients (PARCOR).
func (lpc *LPCAnalysis) GetReflectionCoefficients() []float64 {
	return lpc.reflection
}

// GetGain returns the prediction gain.
func (lpc *LPCAnalysis) GetGain() float64 {
	return lpc.gain
}

// LSFToLPC converts Line Spectral Frequencies to LPC coefficients
func LSFToLPC(lsf []float64) []float64 {
	lpc := NewLPCAnalysis(len(lsf))
	if lpc == nil {
		return nil
	}

	err := lpc.FromLSF(lsf)
	if err != nil {
		return nil
	}

	return lpc.GetCoefficients()
}

// AnalyzeLPC analyzes a signal and returns LPC coefficients
func AnalyzeLPC(signal []float64, order int) []float64 {
	lpc := NewLPCAnalysis(order)
	if lpc == nil {
		return nil
	}

	err := lpc.Analyze(signal)
	if err != nil {
		return nil
	}

	return lpc.GetCoefficients()
}

// LPCToLSF converts LPC coefficients to Line Spectral Frequencies
func LPCToLSF(lpcCoeffs []float64) []float64 {
	lpc := NewLPCAnalysis(len(lpcCoeffs))
	if lpc == nil {
		return nil
	}

	// Set coefficients
	copy(lpc.coeffs, lpcCoeffs)

	return lpc.ToLSF()
}

// ComputeResidual computes the residual signal from LPC analysis
func ComputeResidual(signal []float64, lpcCoeffs []float64) []float64 {
	lpc := NewLPCAnalysis(len(lpcCoeffs))
	if lpc == nil {
		return nil
	}

	// Set coefficients
	copy(lpc.coeffs, lpcCoeffs)

	return lpc.ComputeResidual(signal)
}

// SynthesizeLPC synthesizes signal from residual using LPC coefficients
func SynthesizeLPC(residual []float64, lpcCoeffs []float64) []float64 {
	lpc := NewLPCAnalysis(len(lpcCoeffs))
	if lpc == nil {
		return nil
	}

	// Set coefficients
	copy(lpc.coeffs, lpcCoeffs)

	return lpc.Synthesize(residual)
}
