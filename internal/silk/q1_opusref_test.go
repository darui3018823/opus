//go:build opusref

package silk

import (
	"math"
	"strings"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

type silkQ1Fixture struct {
	name       string
	rate       int
	frameMs    int
	frame      int
	voiced     bool
	complexity int
	mode       string
	wantInterp bool
}

func TestSILKQ1LPCNLSFOracle(t *testing.T) {
	if version := cgoref.Version(); !strings.Contains(version, "1.6.1") {
		t.Fatalf("SILK Q1 oracle requires libopus 1.6.1, got %q", version)
	}
	fixtures := []silkQ1Fixture{
		{name: "nb-unvoiced-reset-10ms", rate: 8000, frameMs: 10, frame: 0, complexity: 5, mode: "mono-silk"},
		{name: "mb-voiced-reset-20ms", rate: 12000, frameMs: 20, frame: 0, voiced: true, complexity: 5, mode: "mono-silk"},
		{name: "wb-unvoiced-steady-20ms-no-interp", rate: 16000, frameMs: 20, frame: 2, complexity: 3, mode: "mono-silk"},
		{name: "nb-voiced-steady-20ms-interp", rate: 8000, frameMs: 20, frame: 3, voiced: true, complexity: 5, mode: "mono-silk", wantInterp: true},
		{name: "mb-unvoiced-steady-10ms", rate: 12000, frameMs: 10, frame: 4, complexity: 5, mode: "mono-silk"},
		{name: "wb-voiced-steady-20ms-interp", rate: 16000, frameMs: 20, frame: 5, voiced: true, complexity: 8, mode: "mono-silk", wantInterp: true},
	}

	seenInterpolation := false
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			if runSILKQ1Fixture(t, fixture) {
				seenInterpolation = true
			}
		})
	}
	if !seenInterpolation {
		t.Fatal("Q1 fixture matrix did not exercise active NLSF interpolation")
	}
}

func runSILKQ1Fixture(t *testing.T, fixture silkQ1Fixture) bool {
	t.Helper()
	order := 10
	if fixture.rate >= 16000 {
		order = 16
	}
	nbSubfr := 4
	if fixture.frameMs == 10 {
		nbSubfr = 2
	}
	frameLen := fixture.rate * fixture.frameMs / 1000
	subframeLengths := equalSubframeLengths(frameLen, nbSubfr)
	x32 := makeSILKQ1Signal(fixture, frameLen+fixture.rate/25)
	x64 := make([]float64, len(x32))
	for i := range x32 {
		x64[i] = float64(x32[i])
	}
	inv32 := make([]float32, nbSubfr)
	inv64 := make([]float64, nbSubfr)
	flatLTP32 := make([]float32, nbSubfr*ltpOrder)
	ltp64 := make([][]float64, nbSubfr)
	pitchLags := make([]int, nbSubfr)
	for sf := 0; sf < nbSubfr; sf++ {
		inv32[sf] = float32(0.72 + 0.07*float64((sf+fixture.frame)%4))
		inv64[sf] = float64(inv32[sf])
		pitchLags[sf] = fixture.rate/100 + (sf & 1)
		ltp64[sf] = make([]float64, ltpOrder)
		if fixture.voiced {
			coeffs := [...]float32{0.04, 0.12, 0.56, 0.15, 0.05}
			for j := range coeffs {
				flatLTP32[sf*ltpOrder+j] = coeffs[j]
				ltp64[sf][j] = float64(coeffs[j])
			}
		}
	}

	cPre, err := cgoref.SILKBuildLPCInPre(x32, subframeLengths, inv32, flatLTP32, pitchLags, order, fixture.voiced)
	if err != nil {
		t.Fatal(err)
	}
	goPre := buildLPCInPre(x64, subframeLengths, inv64, ltp64, pitchLags, order, fixture.voiced)
	compareSILKQ1FloatStage(t, fixture, "LPC_in_pre", cPre, goPre)

	prevNLSF := makeSILKQ1PreviousNLSF(order, fixture.frame)
	firstAfterReset := fixture.frame == 0
	useInterpolated := fixture.complexity >= 4
	minInvGain := float32(lpcMinInvGain(0.75, 0.68, firstAfterReset))
	subfrLength := subframeLengths[0] + order
	if fixture.wantInterp {
		firstHalfA, _ := silkBurgModifiedFLP(goPre[:2*subfrLength], float64(minInvGain), subfrLength, 2, order)
		prevNLSF = silkA2NLSFFLP(firstHalfA, order)
	}
	speechQ8 := 128
	signalType := SignalTypeUnvoiced
	if fixture.voiced {
		speechQ8 = 208
		signalType = SignalTypeVoiced
	}
	want, err := cgoref.SILKQ1Analyze(cgoref.SILKQ1Input{
		LPCInPre:         cPre,
		SubframeLength:   subfrLength,
		Subframes:        nbSubfr,
		Order:            order,
		MinInvGain:       minInvGain,
		UseInterpolated:  useInterpolated,
		FirstAfterReset:  firstAfterReset,
		PrevNLSFQ15:      prevNLSF,
		SpeechActivityQ8: speechQ8,
		NLSFSurvivors:    q1NLSFSurvivors(fixture.complexity),
		SignalType:       signalType,
	})
	if err != nil {
		t.Fatal(err)
	}

	burgA, burgResidual := silkBurgModifiedFLP(goPre, float64(minInvGain), subfrLength, nbSubfr, order)
	compareSILKQ1FloatStage(t, fixture, "Burg LPC coefficients", want.BurgA, burgA)
	compareSILKQ1FloatStage(t, fixture, "Burg residual energy", []float32{want.BurgResidual}, []float64{burgResidual})

	targetNLSF, interpFactor := silkFindLPCFLP(goPre, float64(minInvGain), subfrLength, nbSubfr, order,
		useInterpolated, firstAfterReset, prevNLSF)
	compareSILKQ1Int16Stage(t, fixture, "A2NLSF result", want.A2NLSFQ15, targetNLSF)
	compareSILKQ1IntStage(t, fixture, "NLSF interpolation factor", []int{want.InterpolationFactor}, []int{interpFactor})

	enc, err := NewEncoderWithFrameMs(fixture.rate, 1, fixture.frameMs)
	if err != nil {
		t.Fatal(err)
	}
	enc.complexity = fixture.complexity
	enc.speechActivity = float64(speechQ8) / 256.0
	compareSILKQ1Int64Stage(t, fixture, "NLSF mu Q20", []int64{int64(want.NLSFMuQ20)}, []int64{int64(enc.nlsfMuQ20())})
	gotWeights := silkNLSFWeightsLaroia(targetNLSF)
	if interpFactor < 4 {
		interpTarget := make([]int16, order)
		silkInterpolate(interpTarget, prevNLSF, targetNLSF, interpFactor, order)
		interpWeights := silkNLSFWeightsLaroia(interpTarget)
		iSqrQ15 := int32(interpFactor*interpFactor) << 11
		for i := range gotWeights {
			gotWeights[i] = int16((int32(gotWeights[i]) >> 1) +
				((int32(interpWeights[i]) * iSqrQ15) >> 16))
		}
	}
	compareSILKQ1Int16Stage(t, fixture, "NLSF weights Q2", want.NLSFWeightsQ2, gotWeights)
	gotStabilized := append([]int16(nil), targetNLSF...)
	silkNLSFStabilize(gotStabilized, getNLSFCB(order).deltaMinQ15, order)
	compareSILKQ1Int16Stage(t, fixture, "stabilized NLSF Q15", want.StabilizedNLSFQ15, gotStabilized)
	gotStage1Errors := silkNLSFVQErrors(gotStabilized, getNLSFCB(order))
	compareSILKQ1Int64Stage(t, fixture, "NLSF stage-1 VQ errors", want.Stage1ErrorsQ24, gotStage1Errors)
	gotRes1, gotWAdj1 := silkQ1CandidateInputs(gotStabilized, gotWeights, getNLSFCB(order), 1)
	compareSILKQ1Int16Stage(t, fixture, "NLSF candidate residual Q10", want.Candidate1ResQ10, gotRes1)
	compareSILKQ1Int16Stage(t, fixture, "NLSF candidate weights Q5", want.Candidate1WAdjQ5, gotWAdj1)
	ecIx1, predQ81 := nlsfUnpack(getNLSFCB(order), 1)
	predQ81Int := make([]int, len(predQ81))
	for i := range predQ81 {
		predQ81Int[i] = int(predQ81[i])
	}
	compareSILKQ1IntStage(t, fixture, "NLSF candidate entropy table index", want.Candidate1ECIx, ecIx1)
	compareSILKQ1IntStage(t, fixture, "NLSF candidate predictor Q8", want.Candidate1PredQ8, predQ81Int)
	gotRatesQ5 := make([]int, len(getNLSFCB(order).cb2RatesQ5))
	for i, value := range getNLSFCB(order).cb2RatesQ5 {
		gotRatesQ5[i] = int(value)
	}
	compareSILKQ1IntStage(t, fixture, "NLSF residual entropy rates Q5", want.ECRatesQ5, gotRatesQ5)
	compareSILKQ1IntStage(t, fixture, "NLSF quantization constants", []int{int(want.QuantStepSizeQ16), int(want.InvQuantStepSizeQ6)}, []int{int(getNLSFCB(order).quantStepSizeQ16), int(getNLSFCB(order).invQuantStepSizeQ6)})
	gotIndices1, _ := silkNLSFDelDecQuant(gotRes1, gotWAdj1, predQ81, ecIx1,
		getNLSFCB(order).cb2RatesQ5, getNLSFCB(order).quantStepSizeQ16,
		getNLSFCB(order).invQuantStepSizeQ6, enc.nlsfMuQ20())
	compareSILKQ1IntStage(t, fixture, "NLSF candidate residual path", want.Candidate1Indices, gotIndices1)
	gotCandidateRD, gotCandidateQuantRD := silkQ1CandidateRD(targetNLSF, gotWeights, getNLSFCB(order), enc.nlsfMuQ20(), signalType)
	wantCandidateRD := want.CandidateRDQ25[:getNLSFCB(order).nEntries]
	wantCandidateQuantRD := want.CandidateQuantRDQ25[:getNLSFCB(order).nEntries]
	compareSILKQ1Int32Stage(t, fixture, "NLSF candidate quantizer RD Q25", wantCandidateQuantRD, gotCandidateQuantRD)
	compareSILKQ1Int32Stage(t, fixture, "NLSF candidate RD Q25", wantCandidateRD, gotCandidateRD)
	gotStage1, gotResidual, gotNLSF, gotPred := enc.silkProcessNLSFs(
		getNLSFCB(order), targetNLSF, prevNLSF, interpFactor, signalType)
	compareSILKQ1IntStage(t, fixture, "NLSF stage-1 index", []int{want.Stage1Index}, []int{gotStage1})
	compareSILKQ1IntStage(t, fixture, "NLSF residual indices", want.ResidualIndices, gotResidual)
	compareSILKQ1Int16Stage(t, fixture, "quantized NLSF Q15", want.QuantizedNLSFQ15, gotNLSF)

	gotInterpolated := gotNLSF
	if interpFactor < 4 {
		gotInterpolated = make([]int16, order)
		silkInterpolate(gotInterpolated, prevNLSF, gotNLSF, interpFactor, order)
	}
	compareSILKQ1Int16Stage(t, fixture, "interpolated NLSF", want.InterpolatedNLSFQ15, gotInterpolated)
	compareSILKQ1Int16Stage(t, fixture, "PredCoef_Q12[0]", want.PredCoef0Q12, gotPred[0])
	compareSILKQ1Int16Stage(t, fixture, "PredCoef_Q12[1]", want.PredCoef1Q12, gotPred[1])
	if fixture.wantInterp && interpFactor == 4 {
		t.Fatalf("fixture=%s sample_rate=%d channel/mode=%s frame=%d did not activate requested interpolation",
			fixture.name, fixture.rate, fixture.mode, fixture.frame)
	}
	t.Logf("Q1 stages match: fixture=%s rate=%d mode=%s frame=%d interp=%d", fixture.name, fixture.rate, fixture.mode, fixture.frame, interpFactor)
	return interpFactor < 4
}

func q1NLSFSurvivors(complexity int) int {
	switch {
	case complexity < 1:
		return 2
	case complexity < 2:
		return 3
	case complexity < 3:
		return 2
	case complexity < 4:
		return 4
	case complexity < 6:
		return 6
	case complexity < 8:
		return 8
	default:
		return 16
	}
}

func makeSILKQ1PreviousNLSF(order, frame int) []int16 {
	out := make([]int16, order)
	for i := range out {
		base := (i + 1) * 32768 / (order + 1)
		shift := ((i*37+frame*53)%181 - 90)
		out[i] = int16(base + shift)
	}
	silkNLSFStabilize(out, getNLSFCB(order).deltaMinQ15, order)
	return out
}

func makeSILKQ1Signal(fixture silkQ1Fixture, length int) []float32 {
	out := make([]float32, length)
	start := fixture.frame * fixture.rate * fixture.frameMs / 1000
	if fixture.voiced {
		for i := range out {
			n := float64(start + i - (length - fixture.rate*fixture.frameMs/1000))
			tm := n / float64(fixture.rate)
			fundamental := 105.0 + float64(fixture.rate/4000)*23.0
			envelope := 0.62 + 0.18*math.Sin(2*math.Pi*2.7*tm+0.13*float64(fixture.frame))
			v := envelope * (0.42*math.Sin(2*math.Pi*fundamental*tm) +
				0.17*math.Sin(2*math.Pi*2*fundamental*tm+0.31) +
				0.08*math.Sin(2*math.Pi*3*fundamental*tm+0.77))
			out[i] = float32(v)
		}
		return out
	}
	state := uint32(0x9e3779b9 ^ uint32(fixture.rate) ^ uint32(fixture.frame*7919))
	var prev float32
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		white := float32(int32(state>>8)-1<<23) / float32(1<<25)
		prev = 0.61*prev + 0.39*white
		out[i] = prev
	}
	return out
}

func compareSILKQ1FloatStage(t *testing.T, fixture silkQ1Fixture, stage string, want []float32, got []float64) {
	t.Helper()
	if len(want) != len(got) {
		failSILKQ1Stage(t, fixture, stage, -1, len(want), len(got), len(got)-len(want))
	}
	for i := range want {
		got32 := float32(got[i])
		wantBits := math.Float32bits(want[i])
		gotBits := math.Float32bits(got32)
		if wantBits != gotBits {
			failSILKQ1Stage(t, fixture, stage, i, want[i], got32, float64(got32)-float64(want[i]))
		}
	}
}

func compareSILKQ1IntStage(t *testing.T, fixture silkQ1Fixture, stage string, want, got []int) {
	t.Helper()
	if len(want) != len(got) {
		failSILKQ1Stage(t, fixture, stage, -1, len(want), len(got), len(got)-len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			failSILKQ1Stage(t, fixture, stage, i, want[i], got[i], got[i]-want[i])
		}
	}
}

func compareSILKQ1Int16Stage(t *testing.T, fixture silkQ1Fixture, stage string, want, got []int16) {
	t.Helper()
	if len(want) != len(got) {
		failSILKQ1Stage(t, fixture, stage, -1, len(want), len(got), len(got)-len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			failSILKQ1Stage(t, fixture, stage, i, want[i], got[i], int(got[i])-int(want[i]))
		}
	}
}

func compareSILKQ1Int64Stage(t *testing.T, fixture silkQ1Fixture, stage string, want, got []int64) {
	t.Helper()
	if len(want) != len(got) {
		failSILKQ1Stage(t, fixture, stage, -1, len(want), len(got), len(got)-len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			failSILKQ1Stage(t, fixture, stage, i, want[i], got[i], got[i]-want[i])
		}
	}
}

func compareSILKQ1Int32Stage(t *testing.T, fixture silkQ1Fixture, stage string, want, got []int32) {
	t.Helper()
	if len(want) != len(got) {
		failSILKQ1Stage(t, fixture, stage, -1, len(want), len(got), len(got)-len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			failSILKQ1Stage(t, fixture, stage, i, want[i], got[i], int64(got[i])-int64(want[i]))
		}
	}
}

func silkQ1CandidateRD(target []int16, weights []int16, cb *nlsfCBParams, muQ20 int32, signalType int) ([]int32, []int32) {
	stabilized := append([]int16(nil), target...)
	silkNLSFStabilize(stabilized, cb.deltaMinQ15, cb.order)
	out := make([]int32, cb.nEntries)
	quant := make([]int32, cb.nEntries)
	for cb1 := 0; cb1 < cb.nEntries; cb1++ {
		resQ10 := make([]int16, cb.order)
		wAdjQ5 := make([]int16, cb.order)
		for i := 0; i < cb.order; i++ {
			cb1Q15 := int32(cb.cb1Q8[cb1*cb.order+i]) << 7
			weightQ9 := int32(cb.cb1WghtQ9[cb1*cb.order+i])
			resQ10[i] = int16(((int32(stabilized[i]) - cb1Q15) * weightQ9) >> 14)
			wAdjQ5[i] = int16(silkDIV32VarQ(int32(weights[i]), weightQ9*weightQ9, 21))
		}
		ecIx, predQ8 := nlsfUnpack(cb, cb1)
		_, rd := silkNLSFDelDecQuant(resQ10, wAdjQ5, predQ8, ecIx, cb.cb2RatesQ5,
			cb.quantStepSizeQ16, cb.invQuantStepSizeQ6, muQ20)
		quant[cb1] = int32(rd)
		icdf := cb.cb1ICDF[(signalType>>1)*cb.nEntries:]
		probQ8 := int32(256 - int(icdf[0]))
		if cb1 > 0 {
			probQ8 = int32(icdf[cb1-1]) - int32(icdf[cb1])
		}
		bitsQ7 := int32((8 << 7) - silkLin2Log(probQ8))
		out[cb1] = int32(rd) + int32(int16(bitsQ7))*int32(int16(muQ20>>2))
	}
	return out, quant
}

func silkQ1CandidateInputs(target, weights []int16, cb *nlsfCBParams, cb1 int) ([]int16, []int16) {
	resQ10 := make([]int16, cb.order)
	wAdjQ5 := make([]int16, cb.order)
	for i := 0; i < cb.order; i++ {
		cb1Q15 := int32(cb.cb1Q8[cb1*cb.order+i]) << 7
		weightQ9 := int32(cb.cb1WghtQ9[cb1*cb.order+i])
		resQ10[i] = int16(((int32(target[i]) - cb1Q15) * weightQ9) >> 14)
		wAdjQ5[i] = int16(silkDIV32VarQ(int32(weights[i]), weightQ9*weightQ9, 21))
	}
	return resQ10, wAdjQ5
}

func failSILKQ1Stage(t *testing.T, fixture silkQ1Fixture, stage string, index int, want, got, diff any) {
	t.Helper()
	t.Fatalf("first SILK Q1 stage mismatch: fixture=%s sample_rate=%d channel/mode=%s frame=%d stage=%s first_index=%d C=%v Go=%v diff=%v",
		fixture.name, fixture.rate, fixture.mode, fixture.frame, stage, index, want, got, diff)
}
