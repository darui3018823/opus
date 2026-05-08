package silk

import (
	"fmt"
	"math"
)

// NLSF (Normalized Line Spectral Frequencies) quantization
// Line Spectral Frequencies are used in SILK for representing the LPC filter
// in a form that is more stable and easier to quantize than direct LPC coefficients.

// NB first-stage codebook (32 entries x 10 coefficients)
// Values are NLSF in Q15 (0 = 0, 32768 = pi). Entries cover the NLSF
// space with varying spectral shapes. Each row is strictly ascending.
var nlsfCB1_NB = [32][10]int16{
	{1892, 4634, 7376, 10117, 12859, 15601, 18342, 21084, 23826, 26568},
	{1500, 3800, 6500, 9800, 13200, 16500, 19800, 23000, 26000, 28800},
	{2200, 5200, 8000, 10800, 13500, 16200, 18800, 21500, 24300, 27200},
	{1200, 3200, 5800, 9000, 12500, 16000, 19500, 22800, 25800, 28500},
	{2500, 5800, 8600, 11200, 13800, 16400, 19000, 21800, 24800, 27800},
	{1000, 2800, 5200, 8200, 11800, 15500, 19200, 22600, 25600, 28200},
	{2800, 6200, 9000, 11600, 14000, 16500, 19200, 22000, 25000, 28000},
	{1600, 4200, 7000, 10000, 13000, 15800, 18600, 21600, 24600, 27600},
	{2000, 4800, 7600, 10400, 13200, 16000, 18800, 21600, 24400, 27200},
	{1100, 3000, 5400, 8400, 11600, 14800, 18200, 21600, 24800, 27800},
	{2400, 5600, 8400, 11000, 13600, 16200, 18800, 21600, 24600, 27600},
	{1400, 3600, 6200, 9200, 12400, 15600, 18800, 22000, 25000, 27800},
	{2600, 6000, 8800, 11400, 13800, 16200, 18800, 21600, 24800, 28000},
	{1800, 4400, 7200, 10000, 12800, 15600, 18400, 21400, 24400, 27400},
	{2100, 5000, 7800, 10600, 13400, 16200, 19000, 21800, 24600, 27400},
	{1300, 3400, 6000, 9000, 12200, 15400, 18600, 21800, 25000, 28000},
	{2300, 5400, 8200, 10800, 13400, 16000, 18600, 21400, 24400, 27400},
	{1700, 4000, 6800, 9600, 12600, 15800, 18800, 21800, 24600, 27400},
	{2000, 4600, 7400, 10200, 13000, 15800, 18600, 21400, 24200, 27000},
	{1500, 4000, 6800, 9800, 13000, 16000, 19000, 22000, 25000, 27800},
	{2200, 5000, 7800, 10600, 13200, 15800, 18400, 21200, 24200, 27200},
	{900, 2600, 5000, 8000, 11200, 14600, 18200, 21800, 25200, 28200},
	{2700, 6000, 8800, 11400, 13800, 16200, 19000, 22000, 25200, 28200},
	{1600, 3800, 6400, 9400, 12600, 15800, 19000, 22000, 24800, 27400},
	{2000, 5200, 8200, 10800, 13200, 15600, 18200, 21000, 24000, 27000},
	{1200, 3200, 5600, 8600, 11800, 15200, 18600, 22000, 25200, 28000},
	{2400, 5600, 8200, 10800, 13400, 16000, 18800, 21800, 24800, 27800},
	{1400, 3600, 6200, 9200, 12200, 15400, 18600, 21800, 25200, 28200},
	{2600, 5800, 8400, 11000, 13600, 16200, 19000, 21800, 24800, 27800},
	{1000, 2800, 5200, 8200, 11400, 14800, 18400, 22000, 25400, 28400},
	{2200, 5200, 8000, 10600, 13200, 15800, 18600, 21600, 24800, 28000},
	{1600, 4000, 6800, 9600, 12400, 15400, 18400, 21400, 24400, 27400},
}

// NB second-stage codebook (8 entries x 10 coefficients)
// Residual refinements in Q15. These are small signed corrections
// added to the first-stage output.
var nlsfCB2_NB = [8][10]int16{
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{200, 100, -100, -200, 100, 200, -100, 0, 100, -100},
	{-200, -100, 100, 200, -100, -200, 100, 0, -100, 100},
	{100, 200, 100, 0, -100, -200, -100, 100, 200, 100},
	{-100, -200, -100, 0, 100, 200, 100, -100, -200, -100},
	{150, -150, 150, -150, 150, -150, 150, -150, 150, -150},
	{-150, 150, -150, 150, -150, 150, -150, 150, -150, 150},
	{100, 100, 100, 100, -100, -100, -100, -100, 100, 100},
}

// WB first-stage codebook (32 entries x 16 coefficients)
// Values are NLSF in Q15 for wideband (16kHz, order=16).
var nlsfCB1_WB [32][16]int16

// WB second-stage codebook (8 entries x 16 coefficients)
var nlsfCB2_WB [8][16]int16

// MB first-stage codebook (32 entries x 12 coefficients)
// For mediumband (12kHz, order=12).
var nlsfCB1_MB [32][12]int16

// MB second-stage codebook (8 entries x 12 coefficients)
var nlsfCB2_MB [8][12]int16

func init() {
	// Generate WB codebooks (order 16)
	generateCodebooks16()
	// Generate MB codebooks (order 12)
	generateCodebooks12()
}

func generateCodebooks16() {
	// Generate 32 first-stage entries for order 16
	for entry := 0; entry < 32; entry++ {
		// Base spacing with variation per entry
		baseOffset := float64(entry-16) / 16.0 * 0.15 // spectral tilt
		for i := 0; i < 16; i++ {
			// Evenly-spaced baseline with per-entry variation
			base := float64(i+1) / 17.0 * math.Pi
			varied := base + baseOffset*base*(math.Pi-base)/math.Pi
			// Clamp
			if varied < 0.05 {
				varied = 0.05
			}
			if varied > math.Pi-0.05 {
				varied = math.Pi - 0.05
			}
			nlsfCB1_WB[entry][i] = int16(varied / math.Pi * 32768.0)
		}
	}

	// Generate second-stage residual codebook
	for entry := 0; entry < 8; entry++ {
		for i := 0; i < 16; i++ {
			switch entry {
			case 0:
				nlsfCB2_WB[entry][i] = 0
			case 1:
				nlsfCB2_WB[entry][i] = int16(150 * math.Sin(float64(i)*math.Pi/8))
			case 2:
				nlsfCB2_WB[entry][i] = int16(-150 * math.Sin(float64(i)*math.Pi/8))
			case 3:
				nlsfCB2_WB[entry][i] = int16(100 * math.Cos(float64(i)*math.Pi/8))
			case 4:
				nlsfCB2_WB[entry][i] = int16(-100 * math.Cos(float64(i)*math.Pi/8))
			case 5:
				if i%2 == 0 {
					nlsfCB2_WB[entry][i] = 120
				} else {
					nlsfCB2_WB[entry][i] = -120
				}
			case 6:
				if i%2 == 0 {
					nlsfCB2_WB[entry][i] = -120
				} else {
					nlsfCB2_WB[entry][i] = 120
				}
			case 7:
				if i < 8 {
					nlsfCB2_WB[entry][i] = 80
				} else {
					nlsfCB2_WB[entry][i] = -80
				}
			}
		}
	}
}

func generateCodebooks12() {
	// Generate 32 first-stage entries for order 12
	for entry := 0; entry < 32; entry++ {
		baseOffset := float64(entry-16) / 16.0 * 0.15
		for i := 0; i < 12; i++ {
			base := float64(i+1) / 13.0 * math.Pi
			varied := base + baseOffset*base*(math.Pi-base)/math.Pi
			if varied < 0.05 {
				varied = 0.05
			}
			if varied > math.Pi-0.05 {
				varied = math.Pi - 0.05
			}
			nlsfCB1_MB[entry][i] = int16(varied / math.Pi * 32768.0)
		}
	}

	// Generate second-stage residual codebook
	for entry := 0; entry < 8; entry++ {
		for i := 0; i < 12; i++ {
			switch entry {
			case 0:
				nlsfCB2_MB[entry][i] = 0
			case 1:
				nlsfCB2_MB[entry][i] = int16(150 * math.Sin(float64(i)*math.Pi/6))
			case 2:
				nlsfCB2_MB[entry][i] = int16(-150 * math.Sin(float64(i)*math.Pi/6))
			case 3:
				nlsfCB2_MB[entry][i] = int16(100 * math.Cos(float64(i)*math.Pi/6))
			case 4:
				nlsfCB2_MB[entry][i] = int16(-100 * math.Cos(float64(i)*math.Pi/6))
			case 5:
				if i%2 == 0 {
					nlsfCB2_MB[entry][i] = 120
				} else {
					nlsfCB2_MB[entry][i] = -120
				}
			case 6:
				if i%2 == 0 {
					nlsfCB2_MB[entry][i] = -120
				} else {
					nlsfCB2_MB[entry][i] = 120
				}
			case 7:
				if i < 6 {
					nlsfCB2_MB[entry][i] = 80
				} else {
					nlsfCB2_MB[entry][i] = -80
				}
			}
		}
	}
}

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

	// Convert to Q15 for codebook comparison
	nlsfQ15 := make([]float64, q.order)
	for i := range nlsf {
		nlsfQ15[i] = nlsf[i] / math.Pi * 32768.0
	}

	// Stage 1: Find best codebook entry
	indices := make([]int, 2)
	bestDist := math.MaxFloat64
	for idx := 0; idx < 32; idx++ {
		cb := q.getCodebookQ15(0, idx)
		dist := 0.0
		for i := 0; i < q.order; i++ {
			diff := nlsfQ15[i] - float64(cb[i])
			dist += weights[i] * diff * diff
		}
		if dist < bestDist {
			bestDist = dist
			indices[0] = idx
		}
	}

	// Compute residual after stage 1
	cb1 := q.getCodebookQ15(0, indices[0])
	residual := make([]float64, q.order)
	for i := 0; i < q.order; i++ {
		residual[i] = nlsfQ15[i] - float64(cb1[i])
	}

	// Stage 2: Find best residual codebook entry
	bestDist = math.MaxFloat64
	for idx := 0; idx < 8; idx++ {
		cb := q.getCodebookQ15(1, idx)
		dist := 0.0
		for i := 0; i < q.order; i++ {
			diff := residual[i] - float64(cb[i])
			dist += weights[i] * diff * diff
		}
		if dist < bestDist {
			bestDist = dist
			indices[1] = idx
		}
	}

	return indices, nil
}

// Dequantize reconstructs NLSF coefficients from quantized indices
func (q *NLSFQuantizer) Dequantize(indices []int) ([]float64, error) {
	if len(indices) != 2 {
		return nil, fmt.Errorf("expected 2 indices, got %d", len(indices))
	}

	// Clamp indices to valid range
	idx0 := indices[0]
	if idx0 < 0 {
		idx0 = 0
	}
	if idx0 >= 32 {
		idx0 = 31
	}
	idx1 := indices[1]
	if idx1 < 0 {
		idx1 = 0
	}
	if idx1 >= 8 {
		idx1 = 7
	}

	// Look up codebook entries
	cb1 := q.getCodebookQ15(0, idx0)
	cb2 := q.getCodebookQ15(1, idx1)

	nlsf := make([]float64, q.order)
	for i := 0; i < q.order; i++ {
		// Sum stage 1 + stage 2, convert from Q15 to radians
		q15Val := float64(cb1[i]) + float64(cb2[i])
		nlsf[i] = q15Val / 32768.0 * math.Pi
	}

	// Enforce stability (ordering and spacing)
	q.EnforceStability(nlsf)

	return nlsf, nil
}

// getCodebookQ15 returns a codebook entry as a slice of int16 in Q15.
// stage 0 = first stage (32 entries), stage 1 = second stage (8 entries).
func (q *NLSFQuantizer) getCodebookQ15(stage, index int) []int16 {
	result := make([]int16, q.order)

	switch q.order {
	case 10: // NB
		if stage == 0 && index >= 0 && index < 32 {
			copy(result, nlsfCB1_NB[index][:])
		} else if stage == 1 && index >= 0 && index < 8 {
			copy(result, nlsfCB2_NB[index][:])
		}
	case 12: // MB
		if stage == 0 && index >= 0 && index < 32 {
			copy(result, nlsfCB1_MB[index][:])
		} else if stage == 1 && index >= 0 && index < 8 {
			copy(result, nlsfCB2_MB[index][:])
		}
	case 16: // WB
		if stage == 0 && index >= 0 && index < 32 {
			copy(result, nlsfCB1_WB[index][:])
		} else if stage == 1 && index >= 0 && index < 8 {
			copy(result, nlsfCB2_WB[index][:])
		}
	default:
		// For other orders (e.g. 18 for SWB), generate on-the-fly
		if stage == 0 {
			for i := 0; i < q.order; i++ {
				base := float64(i+1) / float64(q.order+1) * math.Pi
				offset := (float64(index)/32.0 - 0.5) * 0.15 * base * (math.Pi - base) / math.Pi
				val := base + offset
				result[i] = int16(val / math.Pi * 32768.0)
			}
		} else {
			// Stage 2: small residuals
			for i := 0; i < q.order; i++ {
				result[i] = int16(float64(index-4) * 50.0 * math.Sin(float64(i)*math.Pi/float64(q.order)))
			}
		}
	}

	return result
}

// CheckStability checks if NLSF coefficients are properly ordered and stable
func (q *NLSFQuantizer) CheckStability(nlsf []float64) bool {
	if len(nlsf) != q.order {
		return false
	}

	// Check ordering: 0 < nlsf[0] < nlsf[1] < ... < nlsf[n-1] < pi
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

	// Sort if out of order (bubble sort, small N)
	for pass := 0; pass < len(nlsf); pass++ {
		swapped := false
		for i := 0; i < len(nlsf)-1; i++ {
			if nlsf[i] > nlsf[i+1] {
				nlsf[i], nlsf[i+1] = nlsf[i+1], nlsf[i]
				swapped = true
			}
		}
		if !swapped {
			break
		}
	}

	// Enforce minimum spacing iteratively
	changed := true
	for iter := 0; iter < 20 && changed; iter++ {
		changed = false
		for i := 0; i < len(nlsf)-1; i++ {
			if nlsf[i+1]-nlsf[i] < NLSFMinSpacing {
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

// InterpolateNLSF interpolates between two NLSF vectors
func InterpolateNLSF(nlsf1, nlsf2 []float64, factor float64) []float64 {
	if len(nlsf1) != len(nlsf2) {
		return nil
	}

	result := make([]float64, len(nlsf1))
	for i := range nlsf1 {
		result[i] = nlsf1[i]*(1-factor) + nlsf2[i]*factor
	}

	return result
}

// QuantizeNLSF is a simplified wrapper for NLSF quantization
func QuantizeNLSF(nlsf []float64) ([]float64, []int) {
	quantizer := NewNLSFQuantizer(len(nlsf))
	if quantizer == nil {
		return nlsf, []int{0, 0}
	}

	indices, err := quantizer.Quantize(nlsf)
	if err != nil {
		return nlsf, []int{0, 0}
	}

	quantized, _ := quantizer.Dequantize(indices)
	return quantized, indices
}

// DequantizeNLSF dequantizes NLSF from indices for a given LPC order.
func DequantizeNLSF(indices []int, order int) []float64 {
	quantizer := NewNLSFQuantizer(order)
	if quantizer == nil {
		// Fallback: return evenly spaced NLSF
		nlsf := make([]float64, order)
		for i := range nlsf {
			nlsf[i] = math.Pi * float64(i+1) / float64(order+1)
		}
		return nlsf
	}

	result, err := quantizer.Dequantize(indices)
	if err != nil {
		// Fallback
		nlsf := make([]float64, order)
		for i := range nlsf {
			nlsf[i] = math.Pi * float64(i+1) / float64(order+1)
		}
		return nlsf
	}
	return result
}
