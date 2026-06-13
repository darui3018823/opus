package celt

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/entcode"
)

// TestQuantizeCoarseEnergyRoundtrip verifies that encode→decode roundtrip
// is lossless: the decoded log-energies exactly match what the encoder
// quantised (i.e., perfect ICDF roundtrip).
//
// Two scenarios are tested:
//  1. logE values close to the predictor output (small residuals — normal case).
//  2. logE values far from predictor output (large residuals — clamping case).
func TestQuantizeCoarseEnergyRoundtrip(t *testing.T) {
	numBands := MaxBands // 21 bands (20ms fullband)
	lm := 3
	channels := 1

	tests := []struct {
		name  string
		intra bool
		// logE values in nats; use values that produce residuals within table range
		logEOffset float64 // added to predictor output to control residual
	}{
		// Small residuals: ±1 step from predictor — should roundtrip exactly
		{"inter-frame/small-residual", false, 0.4},
		{"intra-frame/small-residual", true, 0.4},
		// Zero residual
		{"inter-frame/zero-residual", false, 0.0},
		{"intra-frame/zero-residual", true, 0.0},
	}

	initLogE := math.Log(1e-8)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prevLogEEnc := make([]float64, numBands)
			prevLogE2Enc := make([]float64, numBands)
			prevLogEDec := make([]float64, numBands)
			prevLogE2Dec := make([]float64, numBands)
			for i := range prevLogEEnc {
				prevLogEEnc[i] = initLogE
				prevLogE2Enc[i] = initLogE
				prevLogEDec[i] = initLogE
				prevLogE2Dec[i] = initLogE
			}

			// Build logE values: predictor output + small offset
			alpha := 0.80
			if tt.intra {
				alpha = 0.0
			}
			logE := make([]float64, numBands)
			for i := range logE {
				predicted := alpha * initLogE
				logE[i] = predicted + tt.logEOffset
			}

			// Encode
			const testBufBytes = 256
			enc := entcode.NewEncoder(testBufBytes)
			quantLogE := QuantizeCoarseEnergy(enc, logE, prevLogEEnc, prevLogE2Enc,
				tt.intra, numBands, lm, channels, testBufBytes*8)
			enc.Flush()

			// Decode with identical initial state
			dec := entcode.NewDecoder(enc.Bytes())
			decodedLogE := UnquantizeCoarseEnergy(dec, prevLogEDec, prevLogE2Dec,
				tt.intra, numBands, lm, channels, testBufBytes*8)

			// decoded must exactly equal quantised (bit-exact ICDF roundtrip)
			for i := 0; i < numBands; i++ {
				if math.Abs(decodedLogE[i]-quantLogE[i]) > 1e-9 {
					t.Errorf("band %d: decoded %.6f != quantised %.6f (diff=%.2e)",
						i, decodedLogE[i], quantLogE[i], math.Abs(decodedLogE[i]-quantLogE[i]))
				}
			}

			// decoded must be within one quantization step of logE
			// (only valid when residual is within table range — guaranteed by construction here)
			for i := 0; i < numBands; i++ {
				errAbs := math.Abs(decodedLogE[i] - logE[i])
				if errAbs > coarseEnergyStep+1e-9 {
					t.Errorf("band %d: |decoded-logE| = %.4f > step %.4f",
						i, errAbs, coarseEnergyStep)
				}
			}
		})
	}
}

// TestQuantizeCoarseEnergyClampedRoundtrip verifies that even when logE is
// far from the predictor (large residuals get clamped), the decoder still
// reproduces the clamped value exactly (bit-exact roundtrip).
func TestQuantizeCoarseEnergyClampedRoundtrip(t *testing.T) {
	numBands := MaxBands
	lm := 3
	channels := 1
	initLogE := math.Log(1e-8)

	for _, intra := range []bool{false, true} {
		name := "inter"
		if intra {
			name = "intra"
		}
		t.Run(name, func(t *testing.T) {
			prevLogEEnc := make([]float64, numBands)
			prevLogE2Enc := make([]float64, numBands)
			prevLogEDec := make([]float64, numBands)
			prevLogE2Dec := make([]float64, numBands)
			for i := range prevLogEEnc {
				prevLogEEnc[i] = initLogE
				prevLogE2Enc[i] = initLogE
				prevLogEDec[i] = initLogE
				prevLogE2Dec[i] = initLogE
			}

			// logE much higher than predictor → residuals will be clamped
			logE := make([]float64, numBands)
			for i := range logE {
				logE[i] = math.Log(1.0) // 0 nats, much higher than initLogE ≈ -18.4
			}

			const clamped512 = 512
			enc := entcode.NewEncoder(clamped512)
			quantLogE := QuantizeCoarseEnergy(enc, logE, prevLogEEnc, prevLogE2Enc,
				intra, numBands, lm, channels, clamped512*8)
			enc.Flush()

			dec := entcode.NewDecoder(enc.Bytes())
			decodedLogE := UnquantizeCoarseEnergy(dec, prevLogEDec, prevLogE2Dec,
				intra, numBands, lm, channels, clamped512*8)

			// Must be bit-exact
			for i := 0; i < numBands; i++ {
				if math.Abs(decodedLogE[i]-quantLogE[i]) > 1e-9 {
					t.Errorf("band %d: decoded %.6f != quantised %.6f",
						i, decodedLogE[i], quantLogE[i])
				}
			}
		})
	}
}

// TestQuantizeCoarseEnergyInterFramePrediction checks that the inter-frame
// predictor reduces the residual (and hence bit cost) on a steady signal.
func TestQuantizeCoarseEnergyInterFramePrediction(t *testing.T) {
	numBands := MaxBands
	lm := 3
	channels := 1

	// Steady log-energies (constant across frames)
	logE := make([]float64, numBands)
	for i := range logE {
		logE[i] = 3.0 // arbitrary constant
	}

	initLogE := math.Log(1e-8)
	prevLogE := make([]float64, numBands)
	prevLogE2 := make([]float64, numBands)
	for i := range prevLogE {
		prevLogE[i] = initLogE
		prevLogE2[i] = initLogE
	}

	// First frame — intra
	const pred256 = 256
	enc1 := entcode.NewEncoder(pred256)
	q1 := QuantizeCoarseEnergy(enc1, logE, prevLogE, prevLogE2, true, numBands, lm, channels, pred256*8)
	enc1.Flush()
	copy(prevLogE2, prevLogE)
	copy(prevLogE, q1)

	bitsIntra := len(enc1.Bytes()) * 8

	// Second frame — inter (predictor has been primed)
	enc2 := entcode.NewEncoder(pred256)
	_ = QuantizeCoarseEnergy(enc2, logE, prevLogE, prevLogE2, false, numBands, lm, channels, pred256*8)
	enc2.Flush()

	bitsInter := len(enc2.Bytes()) * 8

	t.Logf("intra bits = %d, inter bits = %d", bitsIntra, bitsInter)

	// Inter-frame coding of a steady signal should use no more bits than intra.
	// (It will typically use fewer, but the ICDF tables may produce the same
	// output in edge cases — we just assert non-regression here.)
	if bitsInter > bitsIntra+8 { // allow one byte of framing slack
		t.Errorf("inter bits %d > intra bits %d — predictor is not helping", bitsInter, bitsIntra)
	}
}

// TestUnquantizeCoarseEnergySymbolEdgeCases verifies symbol↔residual mapping
// for 0, ±1, ±20.
func TestUnquantizeCoarseEnergySymbolEdgeCases(t *testing.T) {
	cases := []struct {
		residual int
		symbol   int
	}{
		{0, 0},
		{1, 1},
		{-1, 2},
		{2, 3},
		{-2, 4},
		{20, 39},
		{-20, 40},
	}
	for _, tc := range cases {
		sym := residualToSymbol(tc.residual)
		if sym != tc.symbol {
			t.Errorf("residualToSymbol(%d) = %d, want %d", tc.residual, sym, tc.symbol)
		}
		res := symbolToResidual(tc.symbol)
		if res != tc.residual {
			t.Errorf("symbolToResidual(%d) = %d, want %d", tc.symbol, res, tc.residual)
		}
	}
}
