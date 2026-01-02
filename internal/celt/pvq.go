package celt

import (
	"math"
)

// PVQ (Pyramid Vector Quantization) implementation
// This is the core quantization method used in CELT

// cwrs (Combinatorial Weights for Random Sparse)
// Computes binomial coefficients and combinatorial numbers

// binomial computes binomial coefficient C(n, k)
func binomial(n, k int) uint32 {
	if k > n || k < 0 {
		return 0
	}
	if k == 0 || k == n {
		return 1
	}
	if k > n-k {
		k = n - k
	}
	
	result := uint32(1)
	for i := 0; i < k; i++ {
		result = result * uint32(n-i) / uint32(i+1)
	}
	return result
}

// icwrs computes the number of ways to code a vector with N elements and K pulses
// This is used to determine the PVQ codebook size
func icwrs(n, k int) uint32 {
	if n <= 0 || k < 0 {
		return 0
	}
	if k == 0 {
		return 1
	}
	// C(n+k-1, k)
	return binomial(n+k-1, k)
}

// PVQDecode decodes a PVQ index into a unit vector
// n: vector dimension
// k: number of pulses (L1 norm)
// index: PVQ codebook index
func PVQDecode(n, k int, index uint32) []float64 {
	if n <= 0 || k < 0 {
		return make([]float64, n)
	}
	
	// Allocate output vector
	y := make([]int, n)
	
	// Decode index to pulse positions and signs
	decode_pvq_index(n, k, index, y)
	
	// Convert integer pulse positions to normalized floats
	output := make([]float64, n)
	norm := 0.0
	for i := 0; i < n; i++ {
		output[i] = float64(y[i])
		norm += output[i] * output[i]
	}
	
	// Normalize to unit vector
	if norm > 0 {
		scale := 1.0 / math.Sqrt(norm)
		for i := 0; i < n; i++ {
			output[i] *= scale
		}
	}
	
	return output
}

// decode_pvq_index decodes a PVQ index into pulse positions
func decode_pvq_index(n, k int, index uint32, y []int) {
	if k == 0 {
		// No pulses - zero vector
		for i := 0; i < n; i++ {
			y[i] = 0
		}
		return
	}
	
	if n == 1 {
		// Single dimension - all pulses go here
		y[0] = k
		// Sign bit
		if index&1 != 0 {
			y[0] = -y[0]
		}
		return
	}
	
	// Decode recursively
	// Find how many pulses in first n-1 dimensions
	var krest int
	for krest = 0; krest <= k; krest++ {
		size := icwrs(n-1, krest)
		if index < size {
			break
		}
		index -= size
	}
	
	// Decode first n-1 dimensions with krest pulses
	decode_pvq_index(n-1, krest, index, y)
	
	// Last dimension gets remaining pulses
	y[n-1] = k - krest
	
	// Sign handling (simplified)
	if y[n-1] != 0 {
		// In full implementation, sign would be encoded in index
		// For now, assume positive
	}
}

// PVQEncode encodes a vector into a PVQ index (for encoder)
// This is a stub for future encoder implementation
func PVQEncode(vector []float64, k int) uint32 {
	// Simplified encoding - just return 0 for now
	// Full implementation would:
	// 1. Find pulse positions from vector
	// 2. Encode positions and signs into index
	return 0
}

// extractPulses extracts pulse positions from a normalized vector
func extractPulses(vector []float64, k int) []int {
	n := len(vector)
	pulses := make([]int, n)
	
	// Copy and scale vector
	scaled := make([]float64, n)
	maxVal := 0.0
	for i, v := range vector {
		abs := math.Abs(v)
		if abs > maxVal {
			maxVal = abs
		}
		scaled[i] = v
	}
	
	// Scale to have magnitude approximately k
	if maxVal > 0 {
		scale := float64(k) / (maxVal * math.Sqrt(float64(n)))
		for i := range scaled {
			scaled[i] *= scale
		}
	}
	
	// Round to nearest integer pulses
	remaining := k
	for i := 0; i < n && remaining > 0; i++ {
		// Find position with largest magnitude
		maxIdx := 0
		maxMag := 0.0
		for j := 0; j < n; j++ {
			mag := math.Abs(scaled[j])
			if mag > maxMag {
				maxMag = mag
				maxIdx = j
			}
		}
		
		// Assign pulse
		if scaled[maxIdx] > 0 {
			pulses[maxIdx]++
		} else {
			pulses[maxIdx]--
		}
		scaled[maxIdx] = 0
		remaining--
	}
	
	return pulses
}
