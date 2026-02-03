package celt

import (
	"math"

	"github.com/darui3018823/opus/internal/entcode"
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

// PVQEncode encodes a vector using recursive PVQ splitting
// This matches RFC 6716 Section 4.3.4 mechanism.
func PVQEncode(enc *entcode.Encoder, vector []float64, k int) {
	// Extract pulses from the vector
	y := extractPulses(vector, k)

	// Encode pulse vector
	encodePVQRecursively(enc, len(vector), k, y)
}

// encodePVQRecursively encodes pulse vector y of dimension n with k total pulses
func encodePVQRecursively(enc *entcode.Encoder, n, k int, y []int) {
	if k == 0 {
		return
	}

	if n == 1 {
		// Single dimension: simply encode the sign
		// Magnitude is definitely k.
		if y[0] == 0 {
			// Should not happen if k > 0
			return
		}
		// Sign: 0 for positive, 1 for negative?
		// Opus standard: s=0 (positive), s=1 (negative)?
		// Actually Opus encodes sign *when it encounters a non-zero pulse*.
		// If N=1 and K>0, it IS non-zero.
		// "The sign is encoded using one bit with 0.5 probability"
		isNegative := y[0] < 0
		enc.EncodeBit(isNegative, 16384)
		return
	}

	// Split dimension
	m := n / 2

	// Count pulses in left side (first m dimensions)
	kLeft := 0
	for i := 0; i < m; i++ {
		kLeft += int(math.Abs(float64(y[i]))) // Logic assumes y is split correctly
	}

	// Define PDF for splitting k pulses into kLeft (left) and k-kLeft (right)
	// PDF[q] = icwrs(m, q) * icwrs(n-m, k-q)
	// Total = icwrs(n, k)

	// Calculate cumulative counts up to kLeft
	fl := uint32(0)
	fh := uint32(0)
	total := icwrs(n, k)

	targetQ := kLeft

	for q := 0; q <= k; q++ {
		count := icwrs(m, q) * icwrs(n-m, k-q)
		if q < targetQ {
			fl += count
		}
		if q <= targetQ {
			fh += count
		}
	}

	// Encode split point using exact counts
	enc.EncodeExact(fl, fh, total)

	// Recursively encode left and right
	encodePVQRecursively(enc, m, kLeft, y[:m])
	encodePVQRecursively(enc, n-m, k-kLeft, y[m:])
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
			abs := math.Abs(v)
			if abs > maxMag {
				maxMag = abs
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

// PVQDecode decodes a vector using recursive PVQ splitting
// dec: entropy decoder
// n: dimension
// k: number of pulses
// Returns: normalized vector
func PVQDecode(dec *entcode.Decoder, n, k int) []float64 {
	y := make([]int, n)
	decodePVQRecursively(dec, n, k, y)

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

// decodePVQRecursively decodes pulse vector y of dimension n with k total pulses
func decodePVQRecursively(dec *entcode.Decoder, n, k int, y []int) {
	if k == 0 {
		return
	}

	if n == 1 {
		// Single dimension: simply decode the sign
		// Magnitude is definitely k.
		// "The sign is encoded using one bit with 0.5 probability"
		isNegative := dec.DecodeBit(16384)
		if isNegative {
			y[0] = -k
		} else {
			y[0] = k
		}
		return
	}

	// Split dimension
	m := n / 2

	// We need to determine kLeft (q)
	// PDF[q] = icwrs(m, q) * icwrs(n-m, k-q)
	// Total = icwrs(n, k)

	total := icwrs(n, k)

	// Get target cumulative frequency
	c := dec.DecodeGetCumu(total)

	// Find q such that range of q covers c

	fl := uint32(0)
	fh := uint32(0)
	kLeft := -1

	currentFl := uint32(0)

	for q := 0; q <= k; q++ {
		count := icwrs(m, q) * icwrs(n-m, k-q)
		fh = currentFl + count

		if c < fh {
			// Found it
			kLeft = q
			fl = currentFl
			break
		}
		currentFl = fh
	}

	if kLeft == -1 {
		// Should not happen if logic is correct and total matches sum of counts
		// Fallback/Error?
		kLeft = k
		fl = total - 1 // Hack?
		fh = total
	}

	// Update decoder state
	dec.DecodeUpdate(fl, fh, total)

	// Recursively decode left and right
	decodePVQRecursively(dec, m, kLeft, y[:m])
	decodePVQRecursively(dec, n-m, k-kLeft, y[m:])
}
