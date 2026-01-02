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
	n := len(signal)
	if n < lpc.order+1 {
		return fmt.Errorf("signal too short for LPC order %d", lpc.order)
	}

	// Compute autocorrelation
	autocorr := make([]float64, lpc.order+1)
	for lag := 0; lag <= lpc.order; lag++ {
		sum := 0.0
		for i := lag; i < n; i++ {
			sum += signal[i] * signal[i-lag]
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
		lpc.coeffs[i] = workCoeffs[i+1]
	}

	lpc.gain = math.Sqrt(err)
	return nil
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

// FromLSF converts Line Spectral Frequencies back to LPC coefficients.
func (lpc *LPCAnalysis) FromLSF(lsf []float64) error {
	if len(lsf) != lpc.order {
		return fmt.Errorf("LSF length %d doesn't match LPC order %d", len(lsf), lpc.order)
	}

	// Reconstruct LPC coefficients from LSF
	// (Simplified version)
	for i := 0; i < lpc.order; i++ {
		// Placeholder: proper implementation would reconstruct from polynomial roots
		lpc.coeffs[i] = lsf[i] / math.Pi
	}

	return nil
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
