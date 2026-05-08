package celt

import (
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// PVQ (Pyramid Vector Quantization) implementation
// This is the core quantization method used in CELT
// Implements CWRS (Combinatorial Weights for Random Sparse) codebook
// per RFC 6716 Section 5.4.3.3

// vCache memoizes V(n,k) computations to avoid exponential recursion.
// Values are stored as uint64; saturation at math.MaxUint32 prevents wrap-around.
var vCache = make(map[[2]int]uint64)

const cwrsMax = uint64(math.MaxUint32)

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// cwrsV computes V(n, k) — the number of signed PVQ code vectors with
// n dimensions and k pulses (L1 norm equal to k).
//
// Recurrence from RFC 6716 §5.4.3.3:
//
//	V(0, 0) = 1
//	V(0, k) = 0  for k > 0
//	V(n, 0) = 1
//	V(n, k) = V(n-1, k) + V(n, k-1) + V(n-1, k-1)  for n,k > 0
//
// The value is saturated at math.MaxUint32 so it never wraps to zero for
// large inputs, which would cause divide-by-zero in the range coder.
func cwrsV(n, k int) uint64 {
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
	a := cwrsV(n-1, k)
	b := cwrsV(n, k-1)
	c := cwrsV(n-1, k-1)
	v := a + b + c
	// Saturate instead of wrapping to keep sentinel (0 = invalid) meaningful.
	if v < a || v < b || v > cwrsMax {
		v = cwrsMax
	}
	vCache[key] = v
	return v
}

// icwrs returns the uint32-clamped codebook size for n dimensions and k pulses.
// A return value of 0 means either n==0 or k==0 (trivial); saturated inputs
// return math.MaxUint32.
func icwrs(n, k int) uint32 {
	v := cwrsV(n, k)
	if v > cwrsMax {
		return math.MaxUint32
	}
	return uint32(v)
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
			blockSize64 := uint64(2) * cwrsV(n-i-1, k-p)
			blockSize := uint32(min64(blockSize64, cwrsMax))
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
			add := uint32(min64(2*cwrsV(n-i-1, kTotal-j), cwrsMax))
			index += add
		}

		kLeft = kTotal
	}

	return index
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
		// Single dimension: encode the sign bit only (magnitude must be k).
		if y[0] == 0 {
			return
		}
		enc.EncodeBit(y[0] < 0, 16384)
		return
	}

	m := n / 2

	kLeft := 0
	for i := 0; i < m; i++ {
		kLeft += int(math.Abs(float64(y[i])))
	}

	// PDF[q] = cwrsV(m, q) * cwrsV(n-m, k-q), total = cwrsV(n, k)
	// Use uint64 throughout to avoid overflow before converting to uint32 for
	// the range coder (which accepts uint32 totals ≤ math.MaxUint32).
	total64 := cwrsV(n, k)
	if total64 == 0 || total64 >= cwrsMax {
		return
	}
	var fl64, fh64 uint64
	for q := 0; q <= k; q++ {
		count := cwrsV(m, q) * cwrsV(n-m, k-q)
		if q < kLeft {
			fl64 += count
		}
		if q <= kLeft {
			fh64 += count
		}
	}

	// Clamp to uint32 range for the range coder.
	clamp := func(v uint64) uint32 {
		if v > cwrsMax {
			return math.MaxUint32
		}
		return uint32(v)
	}
	enc.EncodeExact(clamp(fl64), clamp(fh64), clamp(total64))

	encodePVQRecursively(enc, m, kLeft, y[:m])
	encodePVQRecursively(enc, n-m, k-kLeft, y[m:])
}

// binomial computes binomial coefficient C(n, k).
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

// PVQDecode decodes a vector using recursive PVQ splitting.
// dec: entropy decoder, n: dimension, k: number of pulses.
// Returns a normalized unit vector.
func PVQDecode(dec *entcode.Decoder, n, k int) []float64 {
	y := make([]int, n)
	decodePVQRecursively(dec, n, k, y)

	output := make([]float64, n)
	norm := 0.0
	for i := 0; i < n; i++ {
		output[i] = float64(y[i])
		norm += output[i] * output[i]
	}

	if norm > 0 {
		scale := 1.0 / math.Sqrt(norm)
		for i := 0; i < n; i++ {
			output[i] *= scale
		}
	}

	return output
}

// decodePVQRecursively decodes pulse vector y of dimension n with k total pulses.
func decodePVQRecursively(dec *entcode.Decoder, n, k int, y []int) {
	if k == 0 {
		return
	}

	if n == 1 {
		if dec.DecodeBit(16384) {
			y[0] = -k
		} else {
			y[0] = k
		}
		return
	}

	m := n / 2
	total64 := cwrsV(n, k)
	// 0 means degenerate; cwrsMax means overflow — neither can be decoded safely.
	if total64 == 0 || total64 >= cwrsMax {
		return
	}

	clamp32 := func(v uint64) uint32 {
		if v > cwrsMax {
			return math.MaxUint32
		}
		return uint32(v)
	}
	total := clamp32(total64)

	c := dec.DecodeGetCumu(total)

	var fl, fh uint32
	kLeft := k
	currentFl := uint32(0)

	for q := 0; q <= k; q++ {
		count := clamp32(cwrsV(m, q) * cwrsV(n-m, k-q))
		// Guard against uint32 overflow in the running sum.
		next := currentFl + count
		if next < currentFl {
			next = math.MaxUint32
		}
		fh = next
		if c < fh {
			kLeft = q
			fl = currentFl
			break
		}
		currentFl = fh
	}

	dec.DecodeUpdate(fl, fh, total)

	decodePVQRecursively(dec, m, kLeft, y[:m])
	decodePVQRecursively(dec, n-m, k-kLeft, y[m:])
}
