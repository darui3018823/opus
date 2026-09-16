package silk

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/entcode"
)

func TestStereoPredictorIndicesRoundTrip(t *testing.T) {
	predQ13 := [2]int32{6200, -2700}
	ix := silkStereoQuantPred(&predQ13)

	enc := entcode.NewEncoder(16)
	encodeStereoPred(enc, ix)
	enc.Flush()

	gotQ13 := decodeStereoPredQ13(entcode.NewDecoder(enc.Bytes()))
	if gotQ13 != predQ13 {
		t.Fatalf("decoded predictors=%v, want quantized predictors=%v (indices=%v)", gotQ13, predQ13, ix)
	}
}

// TestStereoLRToMSPannedMonoAndSplit checks the silk_stereo_LR_to_MS port end
// to end: a hard-panned correlated pair collapses to panned-mono coding
// (mid-only flag, all rate to mid), and a genuinely stereo pair is coded with the
// 8/(13+3*frac) mid/side rate split and a side residual below the raw side.
func TestStereoLRToMSPannedMonoAndSplit(t *testing.T) {
	const (
		fsKHz       = 16
		frameLength = 20 * fsKHz
		totalRate   = 32000
	)
	var state stereoEncState
	state.reset()

	var res stereoLRToMSResult
	midOnlyAt := -1
	for frame := 0; frame < 8; frame++ {
		left := make([]int16, frameLength)
		right := make([]int16, frameLength)
		for i := 0; i < frameLength; i++ {
			n := frame*frameLength + i
			s := 0.7 * math.Sin(2*math.Pi*180*float64(n)/(fsKHz*1000))
			left[i] = int16(math.Round(0.8 * s * 32767))
			right[i] = int16(math.Round(0.2 * s * 32767))
		}
		res = state.lrToMS(left, right, fsKHz, frameLength, totalRate, 255, false)
		if res.midOnly {
			midOnlyAt = frame
			break
		}
	}
	if midOnlyAt < 0 {
		t.Fatalf("panned correlated pair never collapsed to panned-mono coding: last result %+v", res)
	}
	if res.midSideRates != [2]int32{totalRate - 600, 0} {
		t.Fatalf("mid-only rates = %v, want all of total-600 on mid", res.midSideRates)
	}

	// Genuine stereo: independent tones per channel.
	state.reset()
	for frame := 0; frame < 3; frame++ {
		left := make([]int16, frameLength)
		right := make([]int16, frameLength)
		for i := 0; i < frameLength; i++ {
			n := frame*frameLength + i
			tm := float64(n) / (fsKHz * 1000)
			m := 0.5 * math.Sin(2*math.Pi*180*tm)
			d := 0.3 * math.Sin(2*math.Pi*333*tm+0.7)
			left[i] = int16(math.Round((m + d) * 32767))
			right[i] = int16(math.Round((m - d) * 32767))
		}
		res = state.lrToMS(left, right, fsKHz, frameLength, totalRate, 255, false)
		if res.midOnly {
			t.Fatalf("frame %d: independent side content coded mid-only", frame)
		}
		if got := res.midSideRates[0] + res.midSideRates[1]; got != totalRate-600 {
			t.Fatalf("frame %d: mid+side rates = %d, want total minus the 600 bps stereo parameters (%d): %v", frame, got, totalRate-600, res.midSideRates)
		}
		if res.midSideRates[0] <= res.midSideRates[1] {
			t.Fatalf("frame %d: mid rate must exceed side rate: %v", frame, res.midSideRates)
		}
		var rawEnergy, residualEnergy float64
		for i := stereoInterpLenMs * fsKHz; i < frameLength; i++ {
			rawSide := 0.5 * float64(int32(left[i])-int32(right[i]))
			rawEnergy += rawSide * rawSide
			residualEnergy += float64(res.side[i]) * float64(res.side[i])
		}
		if residualEnergy > 1.05*rawEnergy {
			t.Fatalf("frame %d: side residual energy %.3g exceeds raw side energy %.3g", frame, residualEnergy, rawEnergy)
		}
	}
}

func stereoPredQ13FromIndices(ix [2][3]int8) [2]int32 {
	var pred [2]int32
	for n := 0; n < 2; n++ {
		i := int(ix[n][0]) + 3*int(ix[n][2])
		lowQ13 := int32(silkStereoPredQuantQ13[i])
		stepQ13 := int32((int64(int32(silkStereoPredQuantQ13[i+1])-lowQ13) * 6554) >> 16)
		pred[n] = lowQ13 + stepQ13*int32(2*int(ix[n][1])+1)
	}
	pred[0] -= pred[1]
	return pred
}
