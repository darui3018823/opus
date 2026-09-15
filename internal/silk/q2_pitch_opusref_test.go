//go:build opusref

package silk

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// q2PitchFixture describes one injected silk_find_pitch_lags_FLP call.
type q2PitchFixture struct {
	name             string
	fsKHz            int
	nbSubfr          int
	complexity       int // encoder complexity 0..10
	f0               float64
	f0Drift          float64
	harmonics        int
	amp              float64
	noise            float64
	prevLag          int
	ltpCorrIn        float32
	speechActivityQ8 int
	prevSignalType   int
	inputTiltQ15     int
	seed             uint32
}

func q2PitchSynth(fx q2PitchFixture, n int) []float64 {
	fs := float64(fx.fsKHz * 1000)
	x := make([]float64, n)
	state := fx.seed
	next := func() float64 {
		state = state*1664525 + 1013904223
		return float64(int32(state)) / 2147483648.0
	}
	phase := 0.0
	for i := range x {
		f0 := fx.f0 + fx.f0Drift*float64(i)/float64(n)
		phase += 2 * math.Pi * f0 / fs
		v := 0.0
		for h := 1; h <= fx.harmonics; h++ {
			v += math.Sin(float64(h)*phase) / float64(h)
		}
		v = fx.amp*v + fx.noise*next()
		x[i] = float64(float32(v))
	}
	return x
}

// TestSILKQ2PitchOracle compares the float32 port of silk_find_pitch_lags_FLP
// and silk_pitch_analysis_core_FLP with libopus 1.6.1 on identical input
// buffers, including the la_pitch look-ahead. Every float checkpoint is
// compared by float32 bits.
func TestSILKQ2PitchOracle(t *testing.T) {
	fixtures := []q2PitchFixture{
		{name: "nb-c5-voiced-20ms", fsKHz: 8, nbSubfr: 4, complexity: 5, f0: 120, harmonics: 6, amp: 3000, noise: 100, prevLag: 0, speechActivityQ8: 220, prevSignalType: 1, inputTiltQ15: 3000, seed: 11},
		{name: "nb-c5-voiced-prevlag", fsKHz: 8, nbSubfr: 4, complexity: 5, f0: 125, f0Drift: 5, harmonics: 6, amp: 3000, noise: 100, prevLag: 64, ltpCorrIn: 0.71, speechActivityQ8: 240, prevSignalType: 2, inputTiltQ15: -2000, seed: 12},
		{name: "nb-c0-10ms", fsKHz: 8, nbSubfr: 2, complexity: 0, f0: 200, harmonics: 4, amp: 2500, noise: 200, prevLag: 40, ltpCorrIn: 0.5, speechActivityQ8: 200, prevSignalType: 2, inputTiltQ15: 0, seed: 13},
		{name: "mb-c5-voiced-20ms", fsKHz: 12, nbSubfr: 4, complexity: 5, f0: 150, harmonics: 8, amp: 4000, noise: 150, prevLag: 0, speechActivityQ8: 250, prevSignalType: 1, inputTiltQ15: 1000, seed: 14},
		{name: "mb-c8-voiced-prevlag", fsKHz: 12, nbSubfr: 4, complexity: 8, f0: 95, f0Drift: -3, harmonics: 10, amp: 5000, noise: 300, prevLag: 126, ltpCorrIn: 0.8, speechActivityQ8: 255, prevSignalType: 2, inputTiltQ15: 4000, seed: 15},
		{name: "wb-c5-voiced-20ms", fsKHz: 16, nbSubfr: 4, complexity: 5, f0: 180, harmonics: 10, amp: 6000, noise: 200, prevLag: 0, speechActivityQ8: 230, prevSignalType: 1, inputTiltQ15: 2500, seed: 16},
		{name: "wb-c10-voiced-prevlag", fsKHz: 16, nbSubfr: 4, complexity: 10, f0: 110, f0Drift: 8, harmonics: 12, amp: 7000, noise: 400, prevLag: 146, ltpCorrIn: 0.85, speechActivityQ8: 255, prevSignalType: 2, inputTiltQ15: -1500, seed: 17},
		{name: "wb-c2-10ms", fsKHz: 16, nbSubfr: 2, complexity: 2, f0: 240, harmonics: 5, amp: 3500, noise: 250, prevLag: 66, ltpCorrIn: 0.6, speechActivityQ8: 210, prevSignalType: 2, inputTiltQ15: 500, seed: 18},
		{name: "wb-c5-noise-unvoiced", fsKHz: 16, nbSubfr: 4, complexity: 5, f0: 100, harmonics: 1, amp: 100, noise: 4000, prevLag: 0, speechActivityQ8: 150, prevSignalType: 1, inputTiltQ15: 0, seed: 19},
		{name: "wb-c5-long-lag", fsKHz: 16, nbSubfr: 4, complexity: 5, f0: 60, harmonics: 14, amp: 5000, noise: 150, prevLag: 0, speechActivityQ8: 240, prevSignalType: 1, inputTiltQ15: 800, seed: 20},
	}

	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			e := &Encoder{complexity: fx.complexity, lpcOrder: 16}
			if fx.fsKHz < 16 {
				e.lpcOrder = 10
			}
			peComplexity, order, thres1 := e.pitchEstParams()
			ltpMem := peLtpMemLengthMs * fx.fsKHz
			frameLen := fx.nbSubfr * peSubfrLengthMs * fx.fsKHz
			laPitch := 2 * fx.fsKHz
			winLen := findPitchLPCWinMs * fx.fsKHz
			if fx.nbSubfr != peMaxNbSubfr {
				winLen = findPitchLPCWinMs2SF * fx.fsKHz
			}
			xBuf := q2PitchSynth(fx, ltpMem+frameLen+laPitch)
			xBuf32 := make([]float32, len(xBuf))
			for i, v := range xBuf {
				xBuf32[i] = float32(v)
			}
			thresholdQ16 := int(math.Round(thres1 * 65536))

			cres, err := cgoref.SILKFindPitchLags(cgoref.SILKFindPitchLagsInput{
				XBuf: xBuf32, LTPMemLength: ltpMem, FrameLength: frameLen, LAPitch: laPitch,
				PitchLPCWinLength: winLen, Order: order, FsKHz: fx.fsKHz, NbSubfr: fx.nbSubfr,
				PEComplexity: peComplexity, SpeechActivityQ8: fx.speechActivityQ8,
				PrevSignalType: fx.prevSignalType, InputTiltQ15: fx.inputTiltQ15,
				PitchEstThresholdQ16: thresholdQ16, PrevLag: fx.prevLag, LTPCorrIn: fx.ltpCorrIn, RunCore: true,
			})
			if err != nil {
				t.Fatalf("libopus find_pitch_lags: %v", err)
			}
			g := silkFindPitchLagsFLP32(xBuf, laPitch, winLen, order, fx.fsKHz, fx.nbSubfr, peComplexity,
				fx.speechActivityQ8, fx.prevSignalType, fx.inputTiltQ15, thresholdQ16, fx.prevLag, float64(fx.ltpCorrIn), true)

			cmpF := func(label string, i int, c float32, gv float64) {
				t.Helper()
				if float32(gv) != c {
					t.Fatalf("%s[%d]: C=%.9g (%08x) Go=%.9g (%08x)", label, i, c, math.Float32bits(c), gv, math.Float32bits(float32(gv)))
				}
			}
			for i := 0; i <= order; i++ {
				cmpF("auto_corr", i, cres.AutoCorr[i], g.autoCorr[i])
			}
			cmpF("res_nrg", 0, cres.ResNrg, g.resNrg)
			cmpF("pred_gain", 0, cres.PredGain, g.predGain)
			for i := 0; i < order; i++ {
				cmpF("refl_coef", i, cres.ReflCoef[i], g.reflCoef[i])
			}
			for i := 0; i < order; i++ {
				cmpF("A", i, cres.A[i], g.a[i])
			}
			for i := range cres.Residual {
				cmpF("res", i, cres.Residual[i], g.res[i])
			}
			cmpF("thrhld", 0, cres.Threshold, g.threshold)
			if g.voiced != cres.Core.Voiced {
				t.Fatalf("voiced: C=%v Go=%v", cres.Core.Voiced, g.voiced)
			}
			cmpF("LTPCorr", 0, cres.Core.LTPCorr, g.ltpCorr)
			if g.lagIndex != cres.Core.LagIndex || g.contourIndex != cres.Core.ContourIndex {
				t.Fatalf("indices: C lag=%d contour=%d Go lag=%d contour=%d", cres.Core.LagIndex, cres.Core.ContourIndex, g.lagIndex, g.contourIndex)
			}
			for k := 0; k < fx.nbSubfr; k++ {
				if g.pitchL[k] != cres.Core.PitchOut[k] {
					t.Fatalf("pitchL[%d]: C=%d Go=%d", k, cres.Core.PitchOut[k], g.pitchL[k])
				}
			}
			t.Logf("voiced=%v lagIndex=%d contour=%d pitchL=%v LTPCorr=%.6f", g.voiced, g.lagIndex, g.contourIndex, g.pitchL, g.ltpCorr)
		})
	}
}
