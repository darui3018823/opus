//go:build opusref

package silk

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestSILKQ2LTPOracle compares the Go LTP correlation (silk_find_LTP_FLP) and
// LTP gain quantization (silk_quant_LTP_gains_FLP) with libopus 1.6.1 on
// identical float32 residuals, lags, and cumulative-gain state. Every float
// checkpoint is compared by exact float32 bits and every fixed-point value by
// exact integer; no epsilon is used.
func TestSILKQ2LTPOracle(t *testing.T) {
	type fixture struct {
		name         string
		fsKHz        int
		nbSubfr      int
		lagMs        float64
		pulseGain    float64
		noiseGain    float64
		decay        float64
		sumLogGainQ7 int32
		seed         uint32
	}
	fixtures := []fixture{
		{name: "nb-strong-voiced-20ms", fsKHz: 8, nbSubfr: 4, lagMs: 5.0, pulseGain: 6000, noiseGain: 300, decay: 0.998, sumLogGainQ7: 0, seed: 1},
		{name: "nb-weak-voiced-10ms", fsKHz: 8, nbSubfr: 2, lagMs: 8.5, pulseGain: 1500, noiseGain: 900, decay: 1.0, sumLogGainQ7: 1200, seed: 2},
		{name: "mb-voiced-20ms", fsKHz: 12, nbSubfr: 4, lagMs: 6.25, pulseGain: 4000, noiseGain: 500, decay: 0.999, sumLogGainQ7: 3000, seed: 3},
		{name: "mb-noisy-10ms", fsKHz: 12, nbSubfr: 2, lagMs: 12.0, pulseGain: 800, noiseGain: 2000, decay: 1.0, sumLogGainQ7: 5000, seed: 4},
		{name: "wb-strong-voiced-20ms", fsKHz: 16, nbSubfr: 4, lagMs: 4.0, pulseGain: 9000, noiseGain: 200, decay: 0.997, sumLogGainQ7: 0, seed: 5},
		{name: "wb-long-lag-20ms", fsKHz: 16, nbSubfr: 4, lagMs: 17.5, pulseGain: 3000, noiseGain: 700, decay: 1.0, sumLogGainQ7: 2500, seed: 6},
		{name: "wb-unvoiced-like-10ms", fsKHz: 16, nbSubfr: 2, lagMs: 3.0, pulseGain: 100, noiseGain: 3000, decay: 1.0, sumLogGainQ7: 700, seed: 7},
		{name: "wb-gain-capped-20ms", fsKHz: 16, nbSubfr: 4, lagMs: 7.0, pulseGain: 12000, noiseGain: 50, decay: 1.0, sumLogGainQ7: 5300, seed: 8},
	}

	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			subfrLen := 5 * fx.fsKHz
			frameLen := fx.nbSubfr * subfrLen
			ltpMem := 20 * fx.fsKHz
			total := ltpMem + frameLen + ltpOrder

			// Deterministic residual: a decaying pulse train at the pitch
			// period plus uniform noise, quantized to float32 like silk_float.
			lag := int(math.Round(fx.lagMs * float64(fx.fsKHz)))
			res := make([]float64, total)
			state := fx.seed
			next := func() float64 {
				state = state*1664525 + 1013904223
				return float64(int32(state))/2147483648.0
			}
			amp := fx.pulseGain
			for i := range res {
				v := fx.noiseGain * next()
				if i%lag == 0 {
					v += amp
					amp *= fx.decay
				}
				res[i] = float64(float32(v))
			}
			lags := make([]int, fx.nbSubfr)
			for k := range lags {
				lags[k] = lag + (k%2)*2 - 1
			}

			res32 := make([]float32, len(res))
			for i, v := range res {
				res32[i] = float32(v)
			}
			cXX, cxX, err := cgoref.SILKFindLTP(res32, ltpMem, lags, subfrLen)
			if err != nil {
				t.Fatalf("libopus find_LTP: %v", err)
			}
			goXX, goxX := silkFindLTPFLP(res, ltpMem, lags, subfrLen, fx.nbSubfr)
			for k := 0; k < fx.nbSubfr; k++ {
				for i := 0; i < ltpOrder*ltpOrder; i++ {
					if float32(goXX[k][i]) != cXX[k*25+i] {
						t.Fatalf("XX[%d][%d]: C=%.9g (%08x) Go=%.9g (%08x)", k, i,
							cXX[k*25+i], math.Float32bits(cXX[k*25+i]), goXX[k][i], math.Float32bits(float32(goXX[k][i])))
					}
				}
				for i := 0; i < ltpOrder; i++ {
					if float32(goxX[k][i]) != cxX[k*5+i] {
						t.Fatalf("xX[%d][%d]: C=%.9g (%08x) Go=%.9g (%08x)", k, i,
							cxX[k*5+i], math.Float32bits(cxX[k*5+i]), goxX[k][i], math.Float32bits(float32(goxX[k][i])))
					}
				}
			}

			cq, err := cgoref.SILKQuantLTPGains(cXX, cxX, subfrLen, fx.nbSubfr, fx.sumLogGainQ7)
			if err != nil {
				t.Fatalf("libopus quant_LTP_gains: %v", err)
			}
			perIdx, indices, sumLogGain, predGainDB := silkQuantLTPGains(goXX, goxX, subfrLen, fx.nbSubfr, fx.sumLogGainQ7)
			if perIdx != cq.PeriodicityIndex {
				t.Fatalf("periodicity index: C=%d Go=%d", cq.PeriodicityIndex, perIdx)
			}
			for k := 0; k < fx.nbSubfr; k++ {
				if indices[k] != cq.CodebookIndices[k] {
					t.Fatalf("codebook index[%d]: C=%d Go=%d", k, cq.CodebookIndices[k], indices[k])
				}
			}
			if sumLogGain != cq.SumLogGainQ7 {
				t.Fatalf("sum_log_gain_Q7: C=%d Go=%d", cq.SumLogGainQ7, sumLogGain)
			}
			if float32(predGainDB) != cq.PredGainDB {
				t.Fatalf("pred_gain_dB: C=%.9g Go=%.9g", cq.PredGainDB, predGainDB)
			}
			taps := ltpCoeffsForPerSubframe(perIdx, indices)
			for k := 0; k < fx.nbSubfr; k++ {
				for i := 0; i < ltpOrder; i++ {
					want := cq.B[k*5+i]
					got := float32(taps[k][i]) * (1.0 / 16384.0)
					if got != want {
						t.Fatalf("B[%d][%d]: C=%.9g Go=%.9g", k, i, want, got)
					}
				}
			}
			t.Logf("per=%d indices=%v sumLogGain=%d->%d predGain=%.4f dB", perIdx, indices, fx.sumLogGainQ7, sumLogGain, predGainDB)
		})
	}
}
