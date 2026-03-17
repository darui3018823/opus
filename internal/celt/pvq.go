package celt

import (
	"math"
)

// PVQ (Pyramid Vector Quantization) implementation
// This is the core quantization method used in CELT
// Implements CWRS (Combinatorial Weights for Random Sparse) codebook
// per RFC 6716 Section 5.4.3.3

// vCache memoizes V(n,k) computations to avoid exponential recursion.
var vCache = make(map[[2]int]uint32)

// cwrsV computes V(n, k) — the number of signed PVQ code vectors with
// n dimensions and k pulses (L1 norm equal to k).
//
// Recurrence from RFC 6716 §5.4.3.3:
//
//	V(0, 0) = 1
//	V(0, k) = 0  for k > 0
//	V(n, 0) = 1
//	V(n, k) = V(n-1, k) + V(n, k-1) + V(n-1, k-1)  for n,k > 0
func cwrsV(n, k int) uint32 {
	if k < 0 || n < 0 {
		return 0
	}
	if k == 0 {
		return 1
	}
	if n == 0 {
		return 0
	}
	key := [2]int{n, k}
	if v, ok := vCache[key]; ok {
		return v
	}
	v := cwrsV(n-1, k) + cwrsV(n, k-1) + cwrsV(n-1, k-1)
	vCache[key] = v
	return v
}

// icwrs is kept as an alias for cwrsV so existing call sites still compile.
func icwrs(n, k int) uint32 {
	return cwrsV(n, k)
}

// cwrsi decodes a CWRS index into a pulse vector of length n with k pulses.
// This implements the decoding algorithm from RFC 6716 §5.4.3.3.
//
// The index encodes a unique signed integer vector y with ||y||_1 = k.
func cwrsi(n, k int, index uint32) []int {
	y := make([]int, n)
	if k == 0 {
		return y
	}

	for i := 0; i < n-1; i++ {
		// Determine the number of pulses p assigned to position i.
		// We iterate p downward from k to 0, accumulating the codebook
		// sizes for the sub-problems that precede p in the ordering.
		//
		// For p > 0 the vectors with |y_i| = p contribute
		//   2 * V(n-i-1, k-p)  code points  (factor 2 for the sign).
		// For p == 0 there is no sign bit, contributing V(n-i-1, k) code points.

		p := k
		// Skip past groups with more pulses than index allows
		for p > 0 {
			// Number of codewords for vectors where this position gets
			// fewer than p absolute pulses: those are the ones before us.
			// The codewords for |y_i| == p occupy 2*V(n-i-1, k-p) entries.
			blockSize := uint32(2) * cwrsV(n-i-1, k-p)
			if index < blockSize {
				break
			}
			index -= blockSize
			p--
		}

		if p > 0 {
			// Decode sign: LSB of remaining index
			sign := index & 1
			index >>= 1
			if sign != 0 {
				y[i] = -p
			} else {
				y[i] = p
			}
		} else {
			y[i] = 0
		}
		k -= abs(y[i])
	}

	// Last position gets all remaining pulses; sign from remaining index bit
	if k > 0 {
		sign := index & 1
		if sign != 0 {
			y[n-1] = -k
		} else {
			y[n-1] = k
		}
	}

	return y
}

// icwrsi encodes a pulse vector back into a CWRS index (inverse of cwrsi).
// We build the index by working from the last dimension backward, since the
// decoder processes dimensions from first to last, peeling off outer layers.
func icwrsi(n int, y []int) uint32 {
	// Compute total pulses
	k := 0
	for _, v := range y {
		if v < 0 {
			k -= v
		} else {
			k += v
		}
	}

	// Build index from inside out (last dimension first).
	// After processing dimensions n-1 .. i+1, 'index' holds the sub-index
	// for those trailing dimensions. Then we prepend dimension i.

	// Start with the last dimension: just a sign bit if pulses > 0.
	index := uint32(0)
	remaining := k
	for j := 0; j < n-1; j++ {
		ap := y[j]
		if ap < 0 {
			ap = -ap
		}
		remaining -= ap
	}
	// remaining = |y[n-1]|
	if remaining > 0 && y[n-1] < 0 {
		index = 1
	}

	// Now work backward from dimension n-2 to 0
	kLeft := remaining // pulses left for dimensions i+1 .. n-1
	for i := n - 2; i >= 0; i-- {
		p := y[i]
		ap := p
		if ap < 0 {
			ap = -ap
		}

		kTotal := kLeft + ap // total pulses for dimensions i .. n-1

		if ap > 0 {
			// Embed sign: the decoder reads sign = index & 1, index >>= 1
			signBit := uint32(0)
			if p < 0 {
				signBit = 1
			}
			index = (index << 1) | signBit
		}

		// Add offsets for groups with more pulses at position i
		// In the decoder, groups with j > ap pulses (j from k down to ap+1)
		// each contribute 2*V(n-i-1, kTotal-j) codewords before us.
		for j := kTotal; j > ap; j-- {
			index += 2 * cwrsV(n-i-1, kTotal-j)
		}

		kLeft = kTotal
	}

	return index
}

// PVQDecode decodes a PVQ index into a unit vector
// n: vector dimension
// k: number of pulses (L1 norm)
// index: PVQ codebook index
func PVQDecode(n, k int, index uint32) []float64 {
	if n <= 0 || k < 0 {
		return make([]float64, n)
	}

	// Decode CWRS index to integer pulse vector
	y := cwrsi(n, k, index)

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

// PVQEncode encodes a vector into a PVQ index (for encoder)
func PVQEncode(vector []float64, k int) uint32 {
	// Extract pulses from the vector
	y := extractPulses(vector, k)

	// Encode pulses to index using proper CWRS encoding
	return icwrsi(len(vector), y)
}

// extractPulses extracts pulse positions from a normalized vector
func extractPulses(vector []float64, k int) []int {
	n := len(vector)
	pulses := make([]int, n)

	// Copy and scale vector
	scaled := make([]float64, n)
	sumAbs := 0.0
	for i, v := range vector {
		scaled[i] = v
		sumAbs += math.Abs(v)
	}

	if sumAbs < 1e-10 {
		return pulses
	}

	// Scale so L1 norm is k
	gain := float64(k) / sumAbs
	for i := range scaled {
		scaled[i] *= gain
	}

	// Greedy pulse allocation
	for i := 0; i < k; i++ {
		// Find position with largest weighted error/magnitude
		maxIdx := 0
		maxMag := -1.0
		for j, v := range scaled {
			a := math.Abs(v)
			if a > maxMag {
				maxMag = a
				maxIdx = j
			}
		}

		// Assign pulse
		if scaled[maxIdx] > 0 {
			pulses[maxIdx]++
			scaled[maxIdx] -= 1.0
		} else {
			pulses[maxIdx]--
			scaled[maxIdx] += 1.0
		}
	}

	return pulses
}

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
