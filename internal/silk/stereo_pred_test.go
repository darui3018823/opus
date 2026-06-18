package silk

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/entcode"
)

func TestSilkStereoFindPredictorLeastSquares(t *testing.T) {
	x := make([]int16, 320)
	y := make([]int16, len(x))
	for i := range x {
		v := int16(12000 * math.Sin(2*math.Pi*float64(i)/37))
		x[i] = v
		y[i] = int16(3 * int32(v) / 4)
	}

	var amp [4]int32
	gotQ13 := silkStereoFindPredictor(&amp, 0, x, y, len(x), stereoSmoothCoefQ16(false))
	wantQ13 := int32(0.75 * (1 << 13))
	if diff := abs32(gotQ13 - wantQ13); diff > 2 {
		t.Fatalf("predictor Q13=%d, want %d (+/-2)", gotQ13, wantQ13)
	}
	if amp[0] <= 0 {
		t.Fatalf("smoothed mid amplitude was not updated: %v", amp)
	}
	if amp[1] >= amp[0]/20 {
		t.Fatalf("proportional target left excessive residual amplitude: mid=%d residual=%d", amp[0], amp[1])
	}
}

func TestSilkStereoFindPredictorFLPTwoBasis(t *testing.T) {
	const n = 320
	mid := make([]float64, n)
	side := make([]float64, n)
	for i := range mid {
		mid[i] = 12000*math.Sin(2*math.Pi*float64(i)/37) +
			3500*math.Sin(2*math.Pi*float64(i)/5)
	}
	for i := range side {
		prev := mid[i]
		if i > 0 {
			prev = mid[i-1]
		}
		next := mid[i]
		if i+1 < n {
			next = mid[i+1]
		}
		lp := 0.25 * (prev + 2*mid[i] + next)
		side[i] = 0.55*lp - 0.20*mid[i]
	}

	gotQ13, scale := silkStereoFindPredictorFLP(mid, side, 0.01)
	if scale != 1<<13 {
		t.Fatalf("scale=%d, want %d", scale, 1<<13)
	}
	// The helper returns decoder form [LP-HP, HP], which here is
	// approximately [0.55, -0.20] before codebook quantization.
	if math.Abs(float64(gotQ13[0])/float64(scale)-0.55) > 0.04 {
		t.Fatalf("LP predictor=%d/%d, want approximately 0.55", gotQ13[0], scale)
	}
	if math.Abs(float64(gotQ13[1])/float64(scale)+0.20) > 0.04 {
		t.Fatalf("center predictor=%d/%d, want approximately -0.20", gotQ13[1], scale)
	}
}

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

func TestStereoLRToMSReducesCorrelatedSideEnergy(t *testing.T) {
	const (
		fsKHz       = 16
		frameLength = 20 * fsKHz
	)
	var state stereoPredState
	var secondSide []float64
	var secondPCM []float64
	var secondIx [2][3]int8

	for frame := 0; frame < 2; frame++ {
		pcm := make([]float64, frameLength*2)
		for i := 0; i < frameLength; i++ {
			n := frame*frameLength + i
			s := 0.7 * math.Sin(2*math.Pi*180*float64(n)/(fsKHz*1000))
			pcm[2*i] = 0.8 * s
			pcm[2*i+1] = 0.2 * s
		}
		_, side, ix := state.lrToMS(pcm, fsKHz, frameLength)
		if frame == 1 {
			secondPCM = pcm
			secondSide = side
			secondIx = ix
		}
	}

	predQ13 := stereoPredQ13FromIndices(secondIx)
	if predQ13 == [2]int32{} {
		t.Fatalf("correlated panned signal selected zero stereo predictors: indices=%v", secondIx)
	}

	start := stereoInterpLenMs * fsKHz
	var rawEnergy, residualEnergy float64
	for i := start; i < frameLength; i++ {
		rawSide := 0.5 * (secondPCM[2*i] - secondPCM[2*i+1])
		rawEnergy += rawSide * rawSide
		residualEnergy += secondSide[i] * secondSide[i]
	}
	if residualEnergy >= 0.10*rawEnergy {
		t.Fatalf("stereo predictor did not sufficiently reduce side energy: residual/raw=%.4f predictors=%v indices=%v",
			residualEnergy/rawEnergy, predQ13, secondIx)
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
