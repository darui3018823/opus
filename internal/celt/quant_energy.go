package celt

import (
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// e_prob_model is the exact copy of e_prob_model[4][2][42] from libopus
// celt/quant_bands.c.
//
// Indexed as [LM][intra][band_pair_index] where:
//   - LM  0=120 samples, 1=240, 2=480, 3=960 (20 ms @ 48 kHz)
//   - intra  0=inter-frame, 1=intra-frame
//   - 42 values = 21 pairs (p0, decay) for bands 0..20
//
// For band i, pi = 2*min(i,20).
//
//	fs    = prob_model[pi]   << 7   (probability of 0, scaled by 32768)
//	decay = prob_model[pi+1] << 6   (decay, scaled by 32768)
var eProbModel = [4][2][42]uint8{
	/* 120 sample frames */
	{
		/* Inter */
		{72, 127, 65, 129, 66, 128, 65, 128, 64, 128, 62, 128, 64, 128,
			64, 128, 92, 78, 92, 79, 92, 78, 90, 79, 116, 41, 115, 40,
			114, 40, 132, 26, 132, 26, 145, 17, 161, 12, 176, 10, 177, 11},
		/* Intra */
		{24, 179, 48, 138, 54, 135, 54, 132, 53, 134, 56, 133, 55, 132,
			55, 132, 61, 114, 70, 96, 74, 88, 75, 88, 87, 74, 89, 66,
			91, 67, 100, 59, 108, 50, 120, 40, 122, 37, 97, 43, 78, 50},
	},
	/* 240 sample frames */
	{
		/* Inter */
		{83, 78, 84, 81, 88, 75, 86, 74, 87, 71, 90, 73, 93, 74,
			93, 74, 109, 40, 114, 36, 117, 34, 117, 34, 143, 17, 145, 18,
			146, 19, 162, 12, 165, 10, 178, 7, 189, 6, 190, 8, 177, 9},
		/* Intra */
		{23, 178, 54, 115, 63, 102, 66, 98, 69, 99, 74, 89, 71, 91,
			73, 91, 78, 89, 86, 80, 92, 66, 93, 64, 102, 59, 103, 60,
			104, 60, 117, 52, 123, 44, 138, 35, 133, 31, 97, 38, 77, 45},
	},
	/* 480 sample frames */
	{
		/* Inter */
		{61, 90, 93, 60, 105, 42, 107, 41, 110, 45, 116, 38, 113, 38,
			112, 38, 124, 26, 132, 27, 136, 19, 140, 20, 155, 14, 159, 16,
			158, 18, 170, 13, 177, 10, 187, 8, 192, 6, 175, 9, 159, 10},
		/* Intra */
		{21, 178, 59, 110, 71, 86, 75, 85, 84, 83, 91, 66, 88, 73,
			87, 72, 92, 75, 98, 72, 105, 58, 107, 54, 115, 52, 114, 55,
			112, 56, 129, 51, 132, 40, 150, 33, 140, 29, 98, 35, 77, 42},
	},
	/* 960 sample frames */
	{
		/* Inter */
		{42, 121, 96, 66, 108, 43, 111, 40, 117, 44, 123, 32, 120, 36,
			119, 33, 127, 33, 134, 34, 139, 21, 147, 23, 152, 20, 158, 25,
			154, 26, 166, 21, 173, 16, 184, 13, 184, 10, 150, 13, 139, 15},
		/* Intra */
		{22, 178, 63, 114, 74, 82, 84, 83, 92, 82, 103, 62, 96, 72,
			96, 67, 101, 73, 107, 72, 113, 55, 118, 52, 125, 52, 118, 52,
			117, 55, 135, 49, 137, 39, 157, 32, 145, 29, 97, 33, 77, 40},
	},
}

// smallEnergyIcdf is used when the bit budget is very tight (2–14 bits
// remaining). Matches small_energy_icdf in libopus celt/quant_bands.c.
// ec_enc_icdf(enc, 2*qi^-(qi<0), small_energy_icdf, 2) → ftb=2, ft=4.
var smallEnergyIcdf = []uint8{2, 1, 0}

// predCoef[LM] = pred_coef[LM] / 32768 — inter-frame predictor coefficient.
// Source: libopus celt/quant_bands.c  pred_coef[4] = {29440, 26112, 21248, 16384}
var predCoef = [4]float64{
	29440.0 / 32768.0, // LM=0 (120 samples)
	26112.0 / 32768.0, // LM=1 (240 samples)
	21248.0 / 32768.0, // LM=2 (480 samples)
	16384.0 / 32768.0, // LM=3 (960 samples, 20 ms)
}

// betaCoef[LM] = beta_coef[LM] / 32768 — inter-frame beta coefficient.
// Source: libopus celt/quant_bands.c  beta_coef[4] = {30147, 22282, 12124, 6554}
var betaCoef = [4]float64{
	30147.0 / 32768.0, // LM=0
	22282.0 / 32768.0, // LM=1
	12124.0 / 32768.0, // LM=2
	6554.0 / 32768.0,  // LM=3 (≈ 0.200)
}

// betaIntra = 4915 / 32768 ≈ 0.150 — intra-frame beta coefficient.
// Source: libopus celt/quant_bands.c  beta_intra = 4915
const betaIntra = 4915.0 / 32768.0

// eMeans is the mean log2-amplitude per band, from libopus celt/quant_bands.c.
// Band energies are stored as (actual_log2_amplitude - eMeans[band]).
// This reduces the residuals, saving bits.
var eMeans = [25]float64{
	6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
	4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
	4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
	4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
	3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
}

// EMean returns the eMeans value for band i, clamped to valid range.
func EMean(i int) float64 {
	if i < 0 {
		return eMeans[0]
	}
	if i >= len(eMeans) {
		return eMeans[len(eMeans)-1]
	}
	return eMeans[i]
}

// coarseEnergyStep is the quantisation step: 1 step = 1 log2 amplitude unit.
const coarseEnergyStep = 1.0

// lmClamp ensures lm is in valid range [0, 3].
func lmClamp(lm int) int {
	if lm < 0 {
		return 0
	}
	if lm > 3 {
		return 3
	}
	return lm
}

// QuantizeCoarseEnergy encodes band log-energies using RFC 6716 §5.1.2.
//
// Parameters:
//
//	enc       — range encoder
//	logE      — current frame log2-amplitude energies, length numBands
//	prevLogE  — previous frame quantised log2-amplitude energies, length numBands
//	prevLogE2 — two-frames-ago quantised log2-amplitude energies, length numBands
//	intra     — true for first frame (no inter predictor)
//	numBands, lm, channels — frame configuration
//	totalBits — total bit budget for this packet (used to select coding path)
//
// Returns the quantised log2-amplitude energies to be stored as prevLogE for next frame.
func QuantizeCoarseEnergy(
	enc *entcode.Encoder,
	logE []float64,
	prevLogE []float64,
	prevLogE2 []float64,
	intra bool,
	numBands int,
	lm int,
	channels int,
	totalBits int,
) []float64 {
	lm = lmClamp(lm)
	intraIdx := 0
	if intra {
		intraIdx = 1
	}
	probModel := eProbModel[lm][intraIdx]

	coef := predCoef[lm]
	beta := betaCoef[lm]
	if intra {
		coef = 0.0
		beta = betaIntra
	}

	budget := totalBits // whole bits, matches libopus ec_tell() usage

	quantLogE := make([]float64, numBands)
	prev := 0.0

	for i := 0; i < numBands; i++ {
		pi := 2 * i
		if pi > 40 {
			pi = 40 // IMIN(i, 20)*2
		}
		fs := uint32(probModel[pi]) << 7
		decay := int(probModel[pi+1]) << 6

		// Inter-frame prediction — matches libopus quant_coarse_energy_impl:
		//   tmp = coef * MAX(-9, oldEBands[i]) + eMeans[i] + prev
		// logE[i] and oldEBands are in actual log2-amplitude (not mean-subtracted).
		oldE := prevLogE[i]
		if oldE < -9.0 {
			oldE = -9.0
		}
		predicted := coef*oldE + eMeans[i] + prev

		// Quantise residual (nearest integer) — same rounding as libopus floor(.5+f)
		diff := logE[i] - predicted
		qi := int(math.Floor(0.5 + diff))

		// Pre-clamp when the bit budget is tight, matching libopus behaviour.
		tell := enc.Tell()
		bitsLeft := budget - tell - 3*channels*(numBands-i-1)
		if i > 0 && bitsLeft < 30 {
			if bitsLeft < 24 {
				if qi > 1 {
					qi = 1
				}
			}
			if bitsLeft < 16 {
				if qi < -1 {
					qi = -1
				}
			}
		}

		// Choose coding path based on remaining bits (libopus quant_coarse_energy_impl).
		bitsNow := budget - tell // remaining bits for this band
		if bitsNow >= 15 {
			enc.EncodeLaplace(&qi, fs, decay)
		} else if bitsNow >= 2 {
			if qi < -1 {
				qi = -1
			}
			if qi > 1 {
				qi = 1
			}
			enc.EncodeIcdf(encodeSmallEnergySym(qi), smallEnergyIcdf, 2)
		} else if bitsNow >= 1 {
			if qi > 0 {
				qi = 0
			}
			enc.EncodeBitLogp(qi < 0, 1)
		} else {
			qi = -1
		}

		// Reconstruct actual log2-amplitude (includes eMeans[i], same as libopus).
		quantLogE[i] = coef*oldE + eMeans[i] + prev + float64(qi)
		if quantLogE[i] < -28.0 {
			quantLogE[i] = -28.0
		}

		// Update inter-band predictor accumulator
		prev = prev + float64(qi) - beta*float64(qi)
	}

	return quantLogE
}

// encodeSmallEnergySym converts qi ∈ {-1, 0, 1} to small_energy_icdf symbol.
// Matches libopus expression  2*qi ^ -(qi<0):
//
//	qi= 0 → 0, qi=+1 → 2, qi=-1 → 1
func encodeSmallEnergySym(qi int) int {
	if qi >= 0 {
		return 2 * qi
	}
	return -2*qi - 1
}

// UnquantizeCoarseEnergy decodes band log2-amplitude energies using RFC 6716 §5.1.2.
//
// prevLogE and prevLogE2 supply the predictor state; they are NOT updated
// here — the caller must update them after this call.
// totalBits must match the value passed to QuantizeCoarseEnergy.
func UnquantizeCoarseEnergy(
	dec *entcode.Decoder,
	prevLogE []float64,
	prevLogE2 []float64,
	intra bool,
	numBands int,
	lm int,
	channels int,
	totalBits int,
) []float64 {
	lm = lmClamp(lm)
	intraIdx := 0
	if intra {
		intraIdx = 1
	}
	probModel := eProbModel[lm][intraIdx]

	coef := predCoef[lm]
	beta := betaCoef[lm]
	if intra {
		coef = 0.0
		beta = betaIntra
	}

	budget := totalBits // whole bits, matches libopus: budget = dec->storage*8

	// quantLogE[i*channels + c] = actual log2-amplitude for band i, channel c.
	// Matches libopus oldEBands[i*C + c] layout.
	quantLogE := make([]float64, numBands*channels)
	prev := make([]float64, channels) // per-channel inter-band predictor

	for i := 0; i < numBands; i++ {
		pi := 2 * i
		if pi > 40 {
			pi = 40
		}
		fs := uint32(probModel[pi]) << 7
		decay := int(probModel[pi+1]) << 6

		for c := 0; c < channels; c++ {
			// Inter-frame prediction — matches libopus unquant_coarse_energy:
			//   tmp = coef * MAX(-9, oldEBands[i*C+c]) + eMeans[i] + prev[c]
			oldE := prevLogE[i*channels+c]
			if oldE < -9.0 {
				oldE = -9.0
			}
			predicted := coef*oldE + eMeans[i] + prev[c]

			// Coding path selection must mirror the encoder exactly.
			tell := dec.Tell()
			bitsNow := budget - tell

			var qi int
			if bitsNow >= 15 {
				qi = dec.DecodeLaplace(fs, decay)
			} else if bitsNow >= 2 {
				sym := dec.DecodeIcdf(smallEnergyIcdf, 2)
				qi = (sym >> 1) ^ -(sym & 1)
			} else if bitsNow >= 1 {
				if dec.DecodeBitLogp(1) {
					qi = -1
				} else {
					qi = 0
				}
			} else {
				qi = -1
			}

			// Reconstruct actual log2-amplitude (includes eMeans contribution).
			v := predicted + float64(qi)
			if v < -28.0 {
				v = -28.0
			}
			quantLogE[i*channels+c] = v
			prev[c] += float64(qi) - beta*float64(qi)
		}
	}

	return quantLogE
}

// symbolToResidual and residualToSymbol are kept for backward compatibility
// with tests, but are no longer used in the main encoding path.

// symbolToResidual converts an ICDF symbol index to a signed residual.
// Sign-magnitude interleaving: 0→0, 1→+1, 2→-1, 3→+2, 4→-2, …
func symbolToResidual(sym int) int {
	if sym == 0 {
		return 0
	}
	if sym&1 == 1 {
		return (sym + 1) >> 1 // positive
	}
	return -(sym >> 1) // negative
}

// residualToSymbol is the inverse of symbolToResidual.
func residualToSymbol(r int) int {
	if r == 0 {
		return 0
	}
	if r > 0 {
		return r*2 - 1
	}
	return (-r) * 2
}
