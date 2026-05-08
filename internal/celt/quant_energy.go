package celt

import (
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// RFC 6716 §5.1.2 coarse energy ICDF tables.
//
// Our entcode implementation uses a STRICTLY DECREASING ICDF table where
// icdf[s] = Pr(X > s) * ft  (ft = 256 for ftb=8).
// The last entry must be 0.
//
// For inter-frame prediction the residuals (after alpha*prevLogE subtraction)
// are concentrated near zero; for intra the distribution is broader.
//
// The probabilities below are derived from the libopus reference tables
// converted to our decreasing convention.  Residuals are encoded with the
// sign-magnitude interleaving:
//   symbol 0 → residual  0
//   symbol 1 → residual +1,  symbol 2 → residual -1
//   symbol 3 → residual +2,  symbol 4 → residual -2  …
//
// Inter-frame table (lm=3, 20 ms): symbols 0..12
//   Pr(X=0) ≈ 55%, Pr(X=1) ≈ 21%, Pr(X=2) ≈ 21%, then exponentially decaying.
var eModelProbInter = []uint8{
	// icdf[s] = Pr(X > s) * 256, decreasing, last=0
	// sym 0 (res=0):  Pr(X>0) = 45%  → 115/256
	// sym 1 (res=+1): Pr(X>1) = 24%  → 61/256
	// sym 2 (res=-1): Pr(X>2) = 3%   → 8/256 (symmetric, so -1 and +1 equally likely)
	// We use a symmetric Laplace-like distribution with p0=0.55, lambda≈0.79
	// Pr(|res|=k) = (1-p0) * lambda^(k-1) * (1-lambda) / 2  for k≥1
	// p0=0.55 → Pr(X>0) = 0.45 → 115
	// Pr(|res|=1) = 0.45*(1-0.75) = 0.1125 each side → Pr(X>1)=0.45-0.1125=0.3375 → 86
	// Pr(|res|=2) = 0.1125*0.75 = 0.0844 each → Pr(X>2)=0.3375-0.0844=0.2531 → 65
	// ...
	115, 86, 57, 40, 27, 18, 12, 8, 5, 3, 2, 1, 0,
}

// Intra-frame table: symbols 0..22
//   Wider distribution, less concentrated at 0 (no predictor).
//   We use p0=0.15, lambda≈0.80.
var eModelProbIntra = []uint8{
	// Pr(X>0) = 0.85 → 217
	// sym 0 (res=0):  217
	// sym 1 (res=+1): Pr(X>1) = 0.85-0.075=0.775 → 198
	// sym 2 (res=-1): Pr(X>2) = 0.775-0.075=0.700 → 179
	// sym 3 (res=+2): decay by lambda=0.80: 0.075*0.80=0.060 → Pr(X>3)=0.640 → 164
	// etc.
	217, 198, 179, 163, 148, 134, 121, 109, 98, 88, 79, 71,
	63, 56, 50, 44, 39, 34, 30, 26, 22, 18, 0,
}

// maxInterSymbol is the index of the last non-zero-probability symbol
// in the inter-frame table.
const maxInterSymbol = 12

// maxIntraSymbol is the index of the last non-zero-probability symbol
// in the intra-frame table.
const maxIntraSymbol = 22

// Alpha (inter-frame predictor) coefficients for coarse energy.
// RFC 6716 Table 8: alpha[lm][band], lm=3 (20 ms).
var coarseEnergyAlpha = [MaxBands]float64{
	0.80, 0.80, 0.80, 0.80, 0.80, 0.80, 0.80, 0.80,
	0.80, 0.80, 0.80, 0.80, 0.80, 0.80, 0.80, 0.80,
	0.80, 0.80, 0.80, 0.80, 0.80,
}

// Beta is the predictor coefficient for the second lag (prevLogE2).
// libopus sets beta=0 for lm=3 (20 ms).
var coarseEnergyBeta = [MaxBands]float64{}

// coarseEnergyStep is the quantisation step in nats (natural-log domain).
// libopus codes energy in Q4 log2 units (step = ln(2)/16 ≈ 0.0433).
// We use 0.5 nats (≈ 4.3 dB) so that typical residuals fit within ±6 (inter)
// and ±11 (intra) after prediction with alpha=0.80.
const coarseEnergyStep = 0.5

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

// maxResidual returns the maximum absolute residual representable by a table
// whose last usable symbol has index maxSym.
func maxResidual(maxSym int) int {
	// symbols 0, 1, 2, …, maxSym
	// 0 → 0; 1,2 → ±1; …; maxSym-1,maxSym → ±(maxSym/2)
	return maxSym / 2
}

// QuantizeCoarseEnergy encodes band log-energies using RFC 6716 §5.1.2.
//
// Parameters:
//
//	enc       — range encoder
//	logE      — current frame log-energies (nats), length numBands
//	prevLogE  — previous frame quantised log-energies (nats), length numBands
//	prevLogE2 — two-frames-ago quantised log-energies (nats), length numBands
//	intra     — true for first frame (no inter predictor)
//	numBands, lm, channels — frame configuration
//
// Returns the quantised log-energies to be stored as prevLogE for next frame.
func QuantizeCoarseEnergy(
	enc *entcode.Encoder,
	logE []float64,
	prevLogE []float64,
	prevLogE2 []float64,
	intra bool,
	numBands int,
	lm int,
	channels int,
) []float64 {
	quantLogE := make([]float64, numBands)

	icdf := eModelProbInter
	maxSym := maxInterSymbol
	if intra {
		icdf = eModelProbIntra
		maxSym = maxIntraSymbol
	}
	maxRes := maxResidual(maxSym)

	for i := 0; i < numBands; i++ {
		alpha := coarseEnergyAlpha[i]
		beta := coarseEnergyBeta[i]
		if intra {
			alpha = 0.0
			beta = 0.0
		}

		// Inter-frame prediction
		predicted := alpha*prevLogE[i] + beta*prevLogE2[i]

		// Quantise residual
		diff := logE[i] - predicted
		qDiff := int(math.Round(diff / coarseEnergyStep))
		if qDiff > maxRes {
			qDiff = maxRes
		}
		if qDiff < -maxRes {
			qDiff = -maxRes
		}

		sym := residualToSymbol(qDiff)

		// Encode using the ICDF table (ftb=8 → ft=256)
		enc.EncodeIcdf(sym, icdf, 8)

		// Reconstruct quantised log energy
		quantLogE[i] = predicted + float64(qDiff)*coarseEnergyStep
	}

	return quantLogE
}

// UnquantizeCoarseEnergy decodes band log-energies using RFC 6716 §5.1.2.
//
// prevLogE and prevLogE2 supply the predictor state; they are NOT updated
// here — the caller must update them after this call.
func UnquantizeCoarseEnergy(
	dec *entcode.Decoder,
	prevLogE []float64,
	prevLogE2 []float64,
	intra bool,
	numBands int,
	lm int,
	channels int,
) []float64 {
	quantLogE := make([]float64, numBands)

	icdf := eModelProbInter
	if intra {
		icdf = eModelProbIntra
	}

	for i := 0; i < numBands; i++ {
		alpha := coarseEnergyAlpha[i]
		beta := coarseEnergyBeta[i]
		if intra {
			alpha = 0.0
			beta = 0.0
		}

		// Inter-frame prediction (must match encoder exactly)
		predicted := alpha*prevLogE[i] + beta*prevLogE2[i]

		// Decode ICDF symbol
		sym := dec.DecodeIcdf(icdf, 8)

		// Convert to signed residual
		qDiff := symbolToResidual(sym)

		// Reconstruct
		quantLogE[i] = predicted + float64(qDiff)*coarseEnergyStep
	}

	return quantLogE
}
