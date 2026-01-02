package silk

import (
	"fmt"
	"math"
)

// NLSF (Normalized Line Spectral Frequencies) quantization
// Line Spectral Frequencies are used in SILK for representing the LPC filter
// in a form that is more stable and easier to quantize than direct LPC coefficients.

// NLSFQuantizer handles NLSF quantization and dequantization
type NLSFQuantizer struct {
	order int // LPC order
}

// NewNLSFQuantizer creates a new NLSF quantizer
func NewNLSFQuantizer(order int) *NLSFQuantizer {
	if order < MinLPCOrder || order > MaxLPCOrder {
		return nil
	}
	return &NLSFQuantizer{order: order}
}

// Quantize quantizes NLSF coefficients using 2-stage vector quantization
func (q *NLSFQuantizer) Quantize(nlsf []float64) ([]int, error) {
	if len(nlsf) != q.order {
		return nil, fmt.Errorf("NLSF length %d does not match order %d", len(nlsf), q.order)
	}

	// Validate NLSF ordering and stability
	if !q.CheckStability(nlsf) {
		return nil, fmt.Errorf("NLSF coefficients are not properly ordered or stable")
	}

	// Compute perceptual weights
	weights := q.ComputeWeights(nlsf)

	// Stage 1: Coarse quantization
	indices := make([]int, 2)
	residual := make([]float64, q.order)
	copy(residual, nlsf)

	// Stage 1 quantization (simplified codebook search)
	indices[0] = q.findBestCodeword(residual, weights, 0)

	// Compute residual after stage 1
	codebook1 := q.getCodebook(0, indices[0])
	for i := range residual {
		residual[i] -= codebook1[i]
	}

	// Stage 2: Fine quantization
	indices[1] = q.findBestCodeword(residual, weights, 1)

	return indices, nil
}

// Dequantize reconstructs NLSF coefficients from quantized indices
func (q *NLSFQuantizer) Dequantize(indices []int) ([]float64, error) {
	if len(indices) != 2 {
		return nil, fmt.Errorf("expected 2 indices, got %d", len(indices))
	}

	nlsf := make([]float64, q.order)

	// Reconstruct from both stages
	codebook1 := q.getCodebook(0, indices[0])
	codebook2 := q.getCodebook(1, indices[1])

	for i := 0; i < q.order; i++ {
		nlsf[i] = codebook1[i] + codebook2[i]
	}

	// Ensure proper ordering and spacing
	// Distribute evenly if needed
	minVal := NLSFMinSpacing
	maxVal := math.Pi - NLSFMinSpacing
	totalRange := maxVal - minVal
	requiredSpacing := NLSFMinSpacing
	minTotalSpacing := float64(q.order-1) * requiredSpacing

	if totalRange < minTotalSpacing {
		// Not enough space - distribute evenly
		for i := 0; i < q.order; i++ {
			nlsf[i] = minVal + (totalRange * float64(i) / float64(q.order-1))
		}
	} else {
		// Clamp and enforce ordering
		for i := 0; i < q.order; i++ {
			if nlsf[i] < minVal {
				nlsf[i] = minVal + float64(i)*requiredSpacing
			}
			if nlsf[i] > maxVal {
				nlsf[i] = maxVal - float64(q.order-1-i)*requiredSpacing
			}
		}

		// Ensure stability after dequantization
		q.EnforceStability(nlsf)
	}

	return nlsf, nil
}

// CheckStability checks if NLSF coefficients are properly ordered and stable
func (q *NLSFQuantizer) CheckStability(nlsf []float64) bool {
	if len(nlsf) != q.order {
		return false
	}

	// Check ordering: 0 < nlsf[0] < nlsf[1] < ... < nlsf[n-1] < pi
	// Allow equality with minimum spacing
	if nlsf[0] < NLSFMinSpacing {
		return false
	}

	for i := 1; i < len(nlsf); i++ {
		if nlsf[i]-nlsf[i-1] < NLSFMinSpacing {
			return false
		}
	}

	if nlsf[len(nlsf)-1] > math.Pi-NLSFMinSpacing {
		return false
	}

	return true
}

// EnforceStability enforces minimum spacing between NLSF coefficients
func (q *NLSFQuantizer) EnforceStability(nlsf []float64) {
	if len(nlsf) != q.order {
		return
	}

	// Sort if out of order
	for i := 0; i < len(nlsf)-1; i++ {
		if nlsf[i] > nlsf[i+1] {
			// Swap
			nlsf[i], nlsf[i+1] = nlsf[i+1], nlsf[i]
		}
	}

	// Enforce minimum spacing iteratively
	changed := true
	for iter := 0; iter < 10 && changed; iter++ {
		changed = false
		for i := 0; i < len(nlsf)-1; i++ {
			if nlsf[i+1]-nlsf[i] < NLSFMinSpacing {
				// Push apart
				avg := (nlsf[i] + nlsf[i+1]) / 2
				nlsf[i] = avg - NLSFMinSpacing/2
				nlsf[i+1] = avg + NLSFMinSpacing/2
				changed = true
			}
		}

		// Clamp to valid range
		if nlsf[0] < NLSFMinSpacing {
			nlsf[0] = NLSFMinSpacing
			changed = true
		}
		if nlsf[len(nlsf)-1] > math.Pi-NLSFMinSpacing {
			nlsf[len(nlsf)-1] = math.Pi - NLSFMinSpacing
			changed = true
		}
	}
}

// ComputeWeights computes perceptual weights for NLSF quantization
func (q *NLSFQuantizer) ComputeWeights(nlsf []float64) []float64 {
	weights := make([]float64, q.order)

	for i := 0; i < q.order; i++ {
		// Higher weight for lower frequencies (more perceptually important)
		// Weight inversely proportional to frequency
		freq := nlsf[i] / math.Pi // Normalize to [0, 1]
		weights[i] = 1.0 / (1.0 + freq*freq)
	}

	return weights
}

// Interpolate interpolates between two sets of NLSF coefficients
func (q *NLSFQuantizer) Interpolate(nlsf1, nlsf2 []float64, alpha float64) []float64 {
	if len(nlsf1) != q.order || len(nlsf2) != q.order {
		return nil
	}

	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}

	result := make([]float64, q.order)
	for i := 0; i < q.order; i++ {
		result[i] = (1-alpha)*nlsf1[i] + alpha*nlsf2[i]
	}

	// Ensure stability
	q.EnforceStability(result)

	return result
}

// findBestCodeword finds the best codebook entry using weighted distance
func (q *NLSFQuantizer) findBestCodeword(residual, weights []float64, stage int) int {
	numCodewords := q.getCodebookSize(stage)
	bestIndex := 0
	bestDist := math.MaxFloat64

	for idx := 0; idx < numCodewords; idx++ {
		codebook := q.getCodebook(stage, idx)
		dist := q.weightedDistance(residual, codebook, weights)

		if dist < bestDist {
			bestDist = dist
			bestIndex = idx
		}
	}

	return bestIndex
}

// weightedDistance computes weighted Euclidean distance
func (q *NLSFQuantizer) weightedDistance(vec1, vec2, weights []float64) float64 {
	dist := 0.0
	for i := 0; i < q.order; i++ {
		diff := vec1[i] - vec2[i]
		dist += weights[i] * diff * diff
	}
	return dist
}

// getCodebookSize returns the number of codewords for a given stage
func (q *NLSFQuantizer) getCodebookSize(stage int) int {
	// Simplified: stage 0 has 32 entries, stage 1 has 16
	if stage == 0 {
		return 32
	}
	return 16
}

// getCodebook returns a codebook entry
// This is a simplified version - real libopus has trained codebooks
func (q *NLSFQuantizer) getCodebook(stage int, index int) []float64 {
	codebook := make([]float64, q.order)

	// Generate synthetic codebook entries
	// In real implementation, these would be trained codebooks from libopus
	for i := 0; i < q.order; i++ {
		// Spread codebook entries across the valid range
		base := math.Pi * float64(i+1) / float64(q.order+1)
		offset := (float64(index) / float64(q.getCodebookSize(stage)) - 0.5) * 0.3
		if stage == 1 {
			offset *= 0.3 // Finer quantization for stage 2
		}
		codebook[i] = base + offset
	}

	return codebook
}
