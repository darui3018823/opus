package celt

import (
	"math"

	"github.com/darui3018823/opus/internal/dsp"
)

// Float32-faithful port of the libopus 1.6.1 tonality / music analysis
// (src/analysis.c, src/mlp.c) that the Opus encoder runs at complexity >= 7.
// Its AnalysisInfo steers the CELT prefilter gain, the dynalloc leak boost,
// the VBR target, the allocation trim and the coded signal bandwidth. All
// arithmetic is float32 except where analysis.c promotes to double (log,
// sqrt, floor, the 7.5 comparison); accumulations are sequential.

const (
	analysisNBFrames     = 8
	analysisNBTBands     = 18
	analysisBufSize      = 720 // 30 ms at 24 kHz
	analysisCountMax     = 10000
	analysisDetectSize   = 100
	analysisNBTonalSkip  = 9
	analysisLeakBands    = 19
	analysisMaxNeurons   = 32
	analysisTransitionPn = 10
)

var analysisTBands = [analysisNBTBands + 1]int{4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 80, 96, 112, 136, 160, 192, 240}

var analysisStdFeatureBias = [9]float32{5.684947, 3.475288, 1.770634, 1.599784, 3.773215, 2.163313, 1.260756, 1.116868, 1.918795}

// AnalysisInfo is libopus AnalysisInfo: the per-frame result of the
// tonality analysis.
type AnalysisInfo struct {
	Valid               bool
	Tonality            float32
	TonalitySlope       float32
	Noisiness           float32
	Activity            float32
	MusicProb           float32
	MusicProbMin        float32
	MusicProbMax        float32
	Bandwidth           int
	ActivityProbability float32
	MaxPitchRatio       float32
	LeakBoost           [analysisLeakBands]uint8
}

// TonalityAnalysis is libopus TonalityAnalysisState.
type TonalityAnalysis struct {
	fs               int
	angle            [240]float32
	dAngle           [240]float32
	d2Angle          [240]float32
	inmem            [analysisBufSize]float32
	memFill          int
	prevBandTonality [analysisNBTBands]float32
	prevTonality     float32
	prevBandwidth    int
	e                [analysisNBFrames][analysisNBTBands]float32
	logE             [analysisNBFrames][analysisNBTBands]float32
	lowE             [analysisNBTBands]float32
	highE            [analysisNBTBands]float32
	meanE            [analysisNBTBands + 1]float32
	mem              [32]float32
	cmean            [8]float32
	std              [9]float32
	eTracker         float32
	lowECount        float32
	eCount           int
	count            int
	analysisOffset   int
	writePos         int
	readPos          int
	readSubframe     int
	hpEnerAccum      float32
	initialized      bool
	rnnState         [analysisMaxNeurons]float32
	downmixState     [3]float32
	info             [analysisDetectSize]AnalysisInfo
	fftRe, fftIm     []float32
	tonality, noisin []float32
	dmTmp            []float32 // downmix scratch
}

// NewTonalityAnalysis is tonality_analysis_init.
func NewTonalityAnalysis(fs int) *TonalityAnalysis {
	t := &TonalityAnalysis{}
	t.fs = fs
	t.Reset()
	return t
}

// Reset is tonality_analysis_reset: everything but the sample rate.
func (t *TonalityAnalysis) Reset() {
	fs := t.fs
	*t = TonalityAnalysis{fs: fs}
	t.fftRe = make([]float32, 480)
	t.fftIm = make([]float32, 480)
	t.tonality = make([]float32, 240)
	t.noisin = make([]float32, 240)
}

// Initialized reports whether the analysis has consumed input since the
// last reset (libopus st->analysis.initialized).
func (t *TonalityAnalysis) Initialized() bool { return t.initialized }

// silkResamplerDown2HP is the float silk_resampler_down2_hp: a 2:1
// decimating all-pass pair that also returns the high-pass energy.
func silkResamplerDown2HP(S *[3]float32, out []float32, in []float32, inLen int) float32 {
	len2 := inLen / 2
	var hpEner float32
	for k := 0; k < len2; k++ {
		in32 := in[2*k]
		// All-pass section for even input sample.
		Y := in32 - S[0]
		X := float32(0.6074371) * Y
		out32 := S[0] + X
		S[0] = in32 + X
		out32HP := out32
		in32 = in[2*k+1]
		// All-pass section for odd input sample, and add to output of
		// previous section.
		Y = in32 - S[1]
		X = float32(0.15063) * Y
		out32 = out32 + S[1]
		out32 = out32 + X
		S[1] = in32 + X

		Y = -in32 - S[2]
		X = float32(0.15063) * Y
		out32HP = out32HP + S[2]
		out32HP = out32HP + X
		S[2] = -in32 + X

		hpEner += float32(out32HP * out32HP)
		out[k] = float32(0.5) * out32
	}
	return hpEner
}

// downmixAndResample is downmix_and_resample with downmix_float: the
// channels of the float input (interleaved, C channels, samples in [-1, 1])
// are summed, scaled to the CELT signal domain, capped at +6 dBFS, halved
// for stereo and decimated to 24 kHz.
func downmixAndResample(x []float64, y []float32, S *[3]float32, subframe, offset, C, fs int, scratch *[]float32) float32 {
	if subframe == 0 {
		return 0
	}
	if fs == 48000 {
		subframe *= 2
		offset *= 2
	} else if fs == 16000 {
		subframe = subframe * 2 / 3
		offset = offset * 2 / 3
	}
	if len(*scratch) < 4*subframe {
		*scratch = make([]float32, 4*subframe)
	}
	tmp := (*scratch)[:subframe]
	for j := 0; j < subframe; j++ {
		tmp[j] = float32(x[(j+offset)*C]) * 32768
	}
	for c := 1; c < C; c++ {
		for j := 0; j < subframe; j++ {
			tmp[j] += float32(x[(j+offset)*C+c]) * 32768
		}
	}
	// Cap signal to +6 dBFS to avoid problems in the analysis.
	for j := 0; j < subframe; j++ {
		if tmp[j] < -65536 {
			tmp[j] = -65536
		}
		if tmp[j] > 65536 {
			tmp[j] = 65536
		}
		if tmp[j] != tmp[j] {
			tmp[j] = 0
		}
	}
	if C == 2 {
		for j := 0; j < subframe; j++ {
			tmp[j] = float32(0.5) * tmp[j]
		}
	}
	var ret float32
	switch fs {
	case 48000:
		ret = silkResamplerDown2HP(S, y, tmp, subframe)
	case 24000:
		copy(y, tmp[:subframe])
	case 16000:
		tmp3x := (*scratch)[subframe : 4*subframe]
		for j := 0; j < subframe; j++ {
			tmp3x[3*j] = tmp[j]
			tmp3x[3*j+1] = tmp[j]
			tmp3x[3*j+2] = tmp[j]
		}
		silkResamplerDown2HP(S, y, tmp3x, 3*subframe)
	}
	ret *= float32(1.0 / 32768 / 32768)
	return ret
}

// fastAtan2f is libopus fast_atan2f.
func fastAtan2f(y, x float32) float32 {
	const (
		cA = float32(0.43157974)
		cB = float32(0.67848403)
		cC = float32(0.08595542)
		cE = float32(math.Pi / 2)
	)
	x2 := x * x
	y2 := y * y
	// For very small values, we don't care about the answer, so we can just
	// return 0.
	if x2+y2 < 1e-18 {
		return 0
	}
	if x2 < y2 {
		den := float32(y2+float32(cB*x2)) * float32(y2+float32(cC*x2))
		s := cE
		if y < 0 {
			s = -cE
		}
		return float32(float32(float32(-x*y)*float32(y2+float32(cA*x2)))/den) + s
	}
	den := float32(x2+float32(cB*y2)) * float32(x2+float32(cC*y2))
	s := cE
	if y < 0 {
		s = -cE
	}
	t := float32(0)
	if float32(x*y) < 0 {
		t = -cE
	} else {
		t = cE
	}
	return float32(float32(float32(float32(x*y)*float32(x2+float32(cA*y2)))/den)+s) - t
}

// float2int is libopus float2int (lrintf / cvtss2si): round to nearest,
// ties to even.
func float2int(x float32) int {
	return int(math.RoundToEven(float64(x)))
}

func isDigitalSilence32(pcm []float32, lsbDepth int) bool {
	var sampleMax float32
	for _, v := range pcm {
		if v < 0 {
			v = -v
		}
		if v > sampleMax {
			sampleMax = v
		}
	}
	return sampleMax <= float32(1)/float32(int32(1)<<uint(lsbDepth))
}

// tonalityAnalysis is tonality_analysis for one 20 ms (at the input rate)
// chunk of x starting at offset.
func (t *TonalityAnalysis) tonalityAnalysis(x []float64, length, offset, C, lsbDepth int) {
	const N = 480
	const N2 = 240
	A := t.angle[:]
	dA := t.dAngle[:]
	d2A := t.d2Angle[:]
	tonality := t.tonality
	noisiness := t.noisin
	var bandTonality [analysisNBTBands]float32
	var logE [analysisNBTBands]float32
	var BFCC [8]float32
	var features [25]float32
	pi4 := float32(math.Pi * math.Pi * math.Pi * math.Pi)
	var slope float32
	var tonality2 [240]float32
	var midE [8]float32
	var bandLog2 [analysisNBTBands + 1]float32
	var leakageFrom, leakageTo [analysisNBTBands + 1]float32
	var isMasked [analysisNBTBands + 1]bool

	if !t.initialized {
		t.memFill = 240
		t.initialized = true
	}
	alpha := float32(1) / float32(minInt(10, 1+t.count))
	alphaE := float32(1) / float32(minInt(25, 1+t.count))
	// Noise floor related decay for bandwidth detection: -2.2 dB/second.
	alphaE2 := float32(1) / float32(minInt(100, 1+t.count))
	if t.count <= 1 {
		alphaE2 = 1
	}
	if t.fs == 48000 {
		// len and offset are now at 24 kHz.
		length /= 2
		offset /= 2
	} else if t.fs == 16000 {
		length = 3 * length / 2
		offset = 3 * offset / 2
	}
	t.hpEnerAccum += downmixAndResample(x, t.inmem[t.memFill:], &t.downmixState,
		minInt(length, analysisBufSize-t.memFill), offset, C, t.fs, &t.dmTmp)
	if t.memFill+length < analysisBufSize {
		t.memFill += length
		// Don't have enough to update the analysis.
		return
	}
	hpEner := t.hpEnerAccum
	info := &t.info[t.writePos]
	t.writePos++
	if t.writePos >= analysisDetectSize {
		t.writePos -= analysisDetectSize
	}
	isSilence := isDigitalSilence32(t.inmem[:], lsbDepth)

	re, im := t.fftRe, t.fftIm
	for i := 0; i < N2; i++ {
		w := analysisWindow[i]
		re[i] = w * t.inmem[i]
		im[i] = w * t.inmem[N2+i]
		re[N-i-1] = w * t.inmem[N-i-1]
		im[N-i-1] = w * t.inmem[N+N2-i-1]
	}
	copy(t.inmem[:240], t.inmem[analysisBufSize-240:])
	remaining := length - (analysisBufSize - t.memFill)
	t.hpEnerAccum = downmixAndResample(x, t.inmem[240:], &t.downmixState,
		remaining, offset+analysisBufSize-t.memFill, C, t.fs, &t.dmTmp)
	t.memFill = 240 + remaining
	if isSilence {
		// On silence, copy the previous analysis.
		prevPos := t.writePos - 2
		if prevPos < 0 {
			prevPos += analysisDetectSize
		}
		*info = t.info[prevPos]
		return
	}
	dsp.OpusFFTScaled32(re, im)
	if re[0] != re[0] {
		info.Valid = false
		return
	}
	halfOverPi := float32(0.5 / math.Pi)
	for i := 1; i < N2; i++ {
		X1r := re[i] + re[N-i]
		X1i := im[i] - im[N-i]
		X2r := im[i] + im[N-i]
		X2i := re[N-i] - re[i]

		angle := halfOverPi * fastAtan2f(X1i, X1r)
		dAngle := angle - A[i]
		d2Angle := dAngle - dA[i]

		angle2 := halfOverPi * fastAtan2f(X2i, X2r)
		dAngle2 := angle2 - angle
		d2Angle2 := dAngle2 - dAngle

		mod1 := d2Angle - float32(float2int(d2Angle))
		noisiness[i] = abs32(mod1)
		mod1 *= mod1
		mod1 *= mod1

		mod2 := d2Angle2 - float32(float2int(d2Angle2))
		noisiness[i] += abs32(mod2)
		mod2 *= mod2
		mod2 *= mod2

		avgMod := float32(0.25) * float32(float32(d2A[i]+mod1)+2*mod2)
		// This introduces an extra delay of 2 frames in the detection.
		tonality[i] = float32(1)/(1+float32(float32(float32(40*16)*pi4)*avgMod)) - float32(0.015)
		// No delay on this detection, but it's less reliable.
		tonality2[i] = float32(1)/(1+float32(float32(float32(40*16)*pi4)*mod2)) - float32(0.015)

		A[i] = angle2
		dA[i] = dAngle2
		d2A[i] = mod2
	}
	for i := 2; i < N2-1; i++ {
		tt := tonality2[i]
		if m := max32(tonality2[i-1], tonality2[i+1]); m < tt {
			tt = m
		}
		tonality[i] = float32(0.9) * max32(tonality[i], tt-float32(0.1))
	}
	var frameTonality, maxFrameTonality float32
	info.Activity = 0
	var frameNoisiness, frameStationarity float32
	if t.count == 0 {
		for b := 0; b < analysisNBTBands; b++ {
			t.lowE[b] = 1e10
			t.highE[b] = -1e10
		}
	}
	var relativeE, frameLoudness float32
	binEnergy := func(i int) float32 {
		return float32(float32(float32(re[i]*re[i])+float32(re[N-i]*re[N-i]))+float32(im[i]*im[i])) + float32(im[N-i]*im[N-i])
	}
	const scaleEner = float32(1.0 / 32768 / 32768)
	// The energy of the very first band is special because of DC.
	{
		X1r := 2 * re[0]
		X2r := 2 * im[0]
		E := float32(X1r*X1r) + float32(X2r*X2r)
		for i := 1; i < 4; i++ {
			E += binEnergy(i)
		}
		E = scaleEner * E
		bandLog2[0] = float32(0.5*1.442695) * float32(math.Log(float64(E+1e-10)))
	}
	for b := 0; b < analysisNBTBands; b++ {
		var E, tE, nE float32
		for i := analysisTBands[b]; i < analysisTBands[b+1]; i++ {
			binE := binEnergy(i)
			binE = scaleEner * binE
			E += binE
			tE += float32(binE * max32(0, tonality[i]))
			nE += float32(float32(binE*2) * (float32(0.5) - noisiness[i]))
		}
		// Check for extreme band energies that could cause NaNs later.
		if !(E < 1e9) || E != E {
			info.Valid = false
			return
		}
		t.e[t.eCount][b] = E
		frameNoisiness += nE / (float32(1e-15) + E)

		frameLoudness += float32(math.Sqrt(float64(E + 1e-10)))
		logE[b] = float32(math.Log(float64(E + 1e-10)))
		bandLog2[b+1] = float32(0.5*1.442695) * float32(math.Log(float64(E+1e-10)))
		t.logE[t.eCount][b] = logE[b]
		if t.count == 0 {
			t.highE[b] = logE[b]
			t.lowE[b] = logE[b]
		}
		if float64(t.highE[b]) > float64(t.lowE[b])+7.5 {
			if t.highE[b]-logE[b] > logE[b]-t.lowE[b] {
				t.highE[b] -= 0.01
			} else {
				t.lowE[b] += 0.01
			}
		}
		if logE[b] > t.highE[b] {
			t.highE[b] = logE[b]
			t.lowE[b] = max32(t.highE[b]-15, t.lowE[b])
		} else if logE[b] < t.lowE[b] {
			t.lowE[b] = logE[b]
			t.highE[b] = min32(t.lowE[b]+15, t.highE[b])
		}
		relativeE += (logE[b] - t.lowE[b]) / (float32(1e-5) + (t.highE[b] - t.lowE[b]))

		var L1, L2 float32
		for i := 0; i < analysisNBFrames; i++ {
			L1 += float32(math.Sqrt(float64(t.e[i][b])))
			L2 += t.e[i][b]
		}
		stationarity := L1 / float32(math.Sqrt(1e-15+float64(float32(analysisNBFrames)*L2)))
		if stationarity > 0.99 {
			stationarity = 0.99
		}
		stationarity *= stationarity
		stationarity *= stationarity
		frameStationarity += stationarity
		bandTonality[b] = max32(tE/(float32(1e-15)+E), float32(stationarity*t.prevBandTonality[b]))
		frameTonality += bandTonality[b]
		if b >= analysisNBTBands-analysisNBTonalSkip {
			frameTonality -= bandTonality[b-analysisNBTBands+analysisNBTonalSkip]
		}
		maxFrameTonality = max32(maxFrameTonality, float32(float32(1+float32(float32(0.03)*float32(b-analysisNBTBands)))*frameTonality))
		slope += float32(bandTonality[b] * float32(b-8))
		t.prevBandTonality[b] = bandTonality[b]
	}

	const leakageOffset = float32(2.5)
	const leakageSlope = float32(2)
	leakageFrom[0] = bandLog2[0]
	leakageTo[0] = bandLog2[0] - leakageOffset
	for b := 1; b < analysisNBTBands+1; b++ {
		leakSlope := float32(leakageSlope*float32(analysisTBands[b]-analysisTBands[b-1])) / 4
		leakageFrom[b] = min32(leakageFrom[b-1]+leakSlope, bandLog2[b])
		leakageTo[b] = max32(leakageTo[b-1]-leakSlope, bandLog2[b]-leakageOffset)
	}
	for b := analysisNBTBands - 2; b >= 0; b-- {
		leakSlope := float32(leakageSlope*float32(analysisTBands[b+1]-analysisTBands[b])) / 4
		leakageFrom[b] = min32(leakageFrom[b+1]+leakSlope, leakageFrom[b])
		leakageTo[b] = max32(leakageTo[b+1]-leakSlope, leakageTo[b])
	}
	for b := 0; b < analysisNBTBands+1; b++ {
		// leak_boost[] is made up of two terms. The first, based on
		// leakage_to[], represents the boost needed to overcome the amount
		// of analysis leakage cause in a weaker band b by louder
		// neighbouring bands. The second, based on leakage_from[], applies
		// to a loud band b for which the quantization noise causes synthesis
		// leakage to the weaker neighbouring bands.
		boost := max32(0, leakageTo[b]-bandLog2[b]) + max32(0, bandLog2[b]-(leakageFrom[b]+leakageOffset))
		v := int(math.Floor(0.5 + float64(float32(64)*boost)))
		if v > 255 {
			v = 255
		}
		info.LeakBoost[b] = uint8(v)
	}

	var specVariability float32
	for i := 0; i < analysisNBFrames; i++ {
		mindist := float32(1e15)
		for j := 0; j < analysisNBFrames; j++ {
			var dist float32
			for k := 0; k < analysisNBTBands; k++ {
				tmp := t.logE[i][k] - t.logE[j][k]
				dist += float32(tmp * tmp)
			}
			if j != i {
				mindist = min32(mindist, dist)
			}
		}
		specVariability += mindist
	}
	specVariability = float32(math.Sqrt(float64(specVariability / analysisNBFrames / analysisNBTBands)))
	var bandwidthMask float32
	bandwidth := 0
	var maxE float32
	noiseFloor := float32(5.7e-4) / float32(int32(1)<<uint(maxInt(0, lsbDepth-8)))
	noiseFloor *= noiseFloor
	var belowMaxPitch, aboveMaxPitch float32
	b := 0
	for ; b < analysisNBTBands; b++ {
		var E float32
		bandStart := analysisTBands[b]
		bandEnd := analysisTBands[b+1]
		for i := bandStart; i < bandEnd; i++ {
			E += binEnergy(i)
		}
		E = scaleEner * E
		maxE = max32(maxE, E)
		if bandStart < 64 {
			belowMaxPitch += E
		} else {
			aboveMaxPitch += E
		}
		t.meanE[b] = max32(float32((1-alphaE2)*t.meanE[b]), E)
		Em := max32(E, t.meanE[b])
		// Consider the band "active" only if it is less than 90 dB below the
		// peak band and above the PCM quantization noise floor. We use b+1
		// because the first CELT band isn't included in tbands[].
		if float32(E*1e9) > maxE && (Em > float32(float32(3*noiseFloor)*float32(bandEnd-bandStart)) || E > float32(noiseFloor*float32(bandEnd-bandStart))) {
			bandwidth = b + 1
		}
		// Check if the band is masked (see below).
		maskCoef := float32(0.05)
		if t.prevBandwidth >= b+1 {
			maskCoef = 0.01
		}
		isMasked[b] = E < float32(maskCoef*bandwidthMask)
		// Use a simple follower with 13 dB/Bark slope for spreading function.
		bandwidthMask = max32(float32(0.05)*bandwidthMask, E)
	}
	// Special case for the last two bands, for which we don't have spectrum
	// but only the energy above 12 kHz.
	if t.fs == 48000 {
		E := hpEner * float32(1.0/(60*60))
		noiseRatio := float32(30)
		if t.prevBandwidth == 20 {
			noiseRatio = 10
		}
		aboveMaxPitch += E
		t.meanE[b] = max32(float32((1-alphaE2)*t.meanE[b]), E)
		Em := max32(E, t.meanE[b])
		if Em > float32(float32(float32(3*noiseRatio)*noiseFloor)*160) || E > float32(float32(noiseRatio*noiseFloor)*160) {
			bandwidth = 20
		}
		maskCoef := float32(0.05)
		if t.prevBandwidth == 20 {
			maskCoef = 0.01
		}
		isMasked[b] = E < float32(maskCoef*bandwidthMask)
	}
	if aboveMaxPitch > belowMaxPitch {
		info.MaxPitchRatio = belowMaxPitch / aboveMaxPitch
	} else {
		info.MaxPitchRatio = 1
	}
	// In some cases, resampling aliasing can create a small amount of energy
	// in the first band being cut. So if the last band is masked, we don't
	// include it.
	if bandwidth == 20 && isMasked[analysisNBTBands] {
		bandwidth -= 2
	} else if bandwidth > 0 && bandwidth <= analysisNBTBands && isMasked[bandwidth-1] {
		bandwidth--
	}
	if t.count <= 2 {
		bandwidth = 20
	}
	frameLoudness = 20 * float32(math.Log10(float64(frameLoudness)))
	t.eTracker = max32(t.eTracker-float32(0.003), frameLoudness)
	t.lowECount *= 1 - alphaE
	if frameLoudness < t.eTracker-30 {
		t.lowECount += alphaE
	}

	for i := 0; i < 8; i++ {
		var sum float32
		for b := 0; b < 16; b++ {
			sum += float32(analysisDCTTable[i*16+b] * logE[b])
		}
		BFCC[i] = sum
	}
	for i := 0; i < 8; i++ {
		var sum float32
		for b := 0; b < 16; b++ {
			sum += float32(float32(analysisDCTTable[i*16+b]*float32(0.5)) * (t.highE[b] + t.lowE[b]))
		}
		midE[i] = sum
	}

	frameStationarity /= analysisNBTBands
	relativeE /= analysisNBTBands
	if t.count < 10 {
		relativeE = 0.5
	}
	frameNoisiness /= analysisNBTBands
	info.Activity = frameNoisiness + float32((1-frameNoisiness)*relativeE)
	frameTonality = maxFrameTonality / float32(analysisNBTBands-analysisNBTonalSkip)
	frameTonality = max32(frameTonality, float32(t.prevTonality*float32(0.8)))
	t.prevTonality = frameTonality

	slope /= 8 * 8
	info.TonalitySlope = slope

	t.eCount = (t.eCount + 1) % analysisNBFrames
	t.count = minInt(t.count+1, analysisCountMax)
	info.Tonality = frameTonality

	for i := 0; i < 4; i++ {
		features[i] = float32(float32(float32(float32(-0.12299)*(BFCC[i]+t.mem[i+24]))+float32(float32(0.49195)*(t.mem[i]+t.mem[i+16])))+float32(float32(0.69693)*t.mem[i+8])) - float32(float32(1.4349)*t.cmean[i])
	}
	for i := 0; i < 4; i++ {
		t.cmean[i] = float32((1-alpha)*t.cmean[i]) + float32(alpha*BFCC[i])
	}
	for i := 0; i < 4; i++ {
		features[4+i] = float32(float32(0.63246)*(BFCC[i]-t.mem[i+24])) + float32(float32(0.31623)*(t.mem[i]-t.mem[i+16]))
	}
	for i := 0; i < 3; i++ {
		features[8+i] = float32(float32(float32(0.53452)*(BFCC[i]+t.mem[i+24]))-float32(float32(0.26726)*(t.mem[i]+t.mem[i+16]))) - float32(float32(0.53452)*t.mem[i+8])
	}
	if t.count > 5 {
		for i := 0; i < 9; i++ {
			t.std[i] = float32((1-alpha)*t.std[i]) + float32(float32(alpha*features[i])*features[i])
		}
	}
	for i := 0; i < 4; i++ {
		features[i] = BFCC[i] - midE[i]
	}
	for i := 0; i < 8; i++ {
		t.mem[i+24] = t.mem[i+16]
		t.mem[i+16] = t.mem[i+8]
		t.mem[i+8] = t.mem[i]
		t.mem[i] = BFCC[i]
	}
	for i := 0; i < 9; i++ {
		features[11+i] = float32(math.Sqrt(float64(t.std[i]))) - analysisStdFeatureBias[i]
	}
	features[18] = specVariability - float32(0.78)
	features[20] = info.Tonality - float32(0.154723)
	features[21] = info.Activity - float32(0.724643)
	features[22] = frameStationarity - float32(0.743717)
	features[23] = info.TonalitySlope + float32(0.069216)
	features[24] = t.lowECount - float32(0.067930)

	var layerOut [analysisMaxNeurons]float32
	var frameProbs [2]float32
	analysisComputeDense(&mlpLayer0, layerOut[:], features[:])
	analysisComputeGRU(&mlpLayer1, t.rnnState[:], layerOut[:])
	analysisComputeDense(&mlpLayer2, frameProbs[:], t.rnnState[:])

	// Probability of speech or music vs noise.
	info.ActivityProbability = frameProbs[1]
	info.MusicProb = frameProbs[0]

	info.Bandwidth = bandwidth
	t.prevBandwidth = bandwidth
	info.Noisiness = frameNoisiness
	info.Valid = true
}

// Run is run_analysis: feed one Opus frame (interleaved float samples in
// [-1, 1], C channels, at the analysis sample rate) and return the
// AnalysisInfo for it.
func (t *TonalityAnalysis) Run(pcm []float64, frameSize, C, lsbDepth int) AnalysisInfo {
	analysisFrameSize := frameSize
	analysisFrameSize -= analysisFrameSize & 1
	// Avoid overflow/wrap-around of the analysis buffer.
	analysisFrameSize = minInt((analysisDetectSize-5)*t.fs/50, analysisFrameSize)
	pcmLen := analysisFrameSize - t.analysisOffset
	offset := t.analysisOffset
	for pcmLen > 0 {
		t.tonalityAnalysis(pcm, minInt(t.fs/50, pcmLen), offset, C, lsbDepth)
		offset += t.fs / 50
		pcmLen -= t.fs / 50
	}
	t.analysisOffset = analysisFrameSize
	t.analysisOffset -= frameSize
	return t.getInfo(frameSize)
}

// getInfo is tonality_get_info.
func (t *TonalityAnalysis) getInfo(length int) AnalysisInfo {
	pos := t.readPos
	currLookahead := t.writePos - t.readPos
	if currLookahead < 0 {
		currLookahead += analysisDetectSize
	}
	t.readSubframe += length / (t.fs / 400)
	for t.readSubframe >= 8 {
		t.readSubframe -= 8
		t.readPos++
	}
	if t.readPos >= analysisDetectSize {
		t.readPos -= analysisDetectSize
	}
	// On long frames, look at the second analysis window rather than the first.
	if length > t.fs/50 && pos != t.writePos {
		pos++
		if pos == analysisDetectSize {
			pos = 0
		}
	}
	if pos == t.writePos {
		pos--
	}
	if pos < 0 {
		pos = analysisDetectSize - 1
	}
	pos0 := pos
	out := t.info[pos]
	if !out.Valid {
		return out
	}
	tonalityMax := out.Tonality
	tonalityAvg := out.Tonality
	tonalityCount := 1
	// Look at the neighbouring frames and pick largest bandwidth found (to
	// be safe).
	bandwidthSpan := 6
	// If possible, look ahead for a tone to compensate for the delay in the
	// tone detector.
	for i := 0; i < 3; i++ {
		pos++
		if pos == analysisDetectSize {
			pos = 0
		}
		if pos == t.writePos {
			break
		}
		tonalityMax = max32(tonalityMax, t.info[pos].Tonality)
		tonalityAvg += t.info[pos].Tonality
		tonalityCount++
		out.Bandwidth = maxInt(out.Bandwidth, t.info[pos].Bandwidth)
		bandwidthSpan--
	}
	pos = pos0
	// Look back in time to see if any has a wider bandwidth than the current
	// frame.
	for i := 0; i < bandwidthSpan; i++ {
		pos--
		if pos < 0 {
			pos = analysisDetectSize - 1
		}
		if pos == t.writePos {
			break
		}
		out.Bandwidth = maxInt(out.Bandwidth, t.info[pos].Bandwidth)
	}
	out.Tonality = max32(tonalityAvg/float32(tonalityCount), tonalityMax-float32(0.2))

	mpos, vpos := pos0, pos0
	// If we have enough look-ahead, compensate for the ~5-frame delay in the
	// music prob and ~1 frame delay in the VAD prob.
	if currLookahead > 15 {
		mpos += 5
		if mpos >= analysisDetectSize {
			mpos -= analysisDetectSize
		}
		vpos++
		if vpos >= analysisDetectSize {
			vpos -= analysisDetectSize
		}
	}
	probMin := float32(1)
	probMax := float32(0)
	vadProb := t.info[vpos].ActivityProbability
	probCount := max32(0.1, vadProb)
	probAvg := float32(max32(0.1, vadProb) * t.info[mpos].MusicProb)
	for {
		mpos++
		if mpos == analysisDetectSize {
			mpos = 0
		}
		if mpos == t.writePos {
			break
		}
		vpos++
		if vpos == analysisDetectSize {
			vpos = 0
		}
		if vpos == t.writePos {
			break
		}
		posVad := t.info[vpos].ActivityProbability
		probMin = min32((probAvg-float32(analysisTransitionPn*(vadProb-posVad)))/probCount, probMin)
		probMax = max32((probAvg+float32(analysisTransitionPn*(vadProb-posVad)))/probCount, probMax)
		probCount += max32(0.1, posVad)
		probAvg += float32(max32(0.1, posVad) * t.info[mpos].MusicProb)
	}
	out.MusicProb = probAvg / probCount
	probMin = min32(probAvg/probCount, probMin)
	probMax = max32(probAvg/probCount, probMax)
	probMin = max32(probMin, 0)
	probMax = min32(probMax, 1)

	// If we don't have enough look-ahead, do our best to make a decent
	// decision.
	if currLookahead < 10 {
		pmin := probMin
		pmax := probMax
		pos = pos0
		// Look for min/max in the past.
		for i := 0; i < minInt(t.count-1, 15); i++ {
			pos--
			if pos < 0 {
				pos = analysisDetectSize - 1
			}
			pmin = min32(pmin, t.info[pos].MusicProb)
			pmax = max32(pmax, t.info[pos].MusicProb)
		}
		// Bias against switching on active audio.
		pmin = max32(0, pmin-float32(float32(0.1)*vadProb))
		pmax = min32(1, pmax+float32(float32(0.1)*vadProb))
		probMin += float32((1 - float32(float32(0.1)*float32(currLookahead))) * (pmin - probMin))
		probMax += float32((1 - float32(float32(0.1)*float32(currLookahead))) * (pmax - probMax))
	}
	out.MusicProbMin = probMin
	out.MusicProbMax = probMax
	return out
}

// --- src/mlp.c ---

type analysisDenseLayer struct {
	bias         []int8
	inputWeights []int8
	nbInputs     int
	nbNeurons    int
	sigmoid      bool
}

type analysisGRULayer struct {
	bias             []int8
	inputWeights     []int8
	recurrentWeights []int8
	nbInputs         int
	nbNeurons        int
}

var (
	mlpLayer0 = analysisDenseLayer{bias: mlpLayer0Bias[:], inputWeights: mlpLayer0Weights[:], nbInputs: 25, nbNeurons: 32}
	mlpLayer1 = analysisGRULayer{bias: mlpLayer1Bias[:], inputWeights: mlpLayer1Weights[:], recurrentWeights: mlpLayer1RecurWeights[:], nbInputs: 32, nbNeurons: 24}
	mlpLayer2 = analysisDenseLayer{bias: mlpLayer2Bias[:], inputWeights: mlpLayer2Weights[:], nbInputs: 24, nbNeurons: 2, sigmoid: true}
)

const mlpWeightsScale = float32(1.0 / 128)

func tansigApprox(x float32) float32 {
	const (
		N0 = float32(952.52801514)
		N1 = float32(96.39235687)
		N2 = float32(0.60863042)
		D0 = float32(952.72399902)
		D1 = float32(413.36801147)
		D2 = float32(11.88600922)
	)
	X2 := x * x
	num := float32(float32(float32(N2*X2)+N1)*X2) + N0
	den := float32(float32(float32(D2*X2)+D1)*X2) + D0
	num = float32(num*x) / den
	if num < -1 {
		return -1
	}
	if num > 1 {
		return 1
	}
	return num
}

func sigmoidApprox(x float32) float32 {
	return float32(0.5) + float32(float32(0.5)*tansigApprox(float32(0.5)*x))
}

func gemmAccum(out []float32, weights []int8, rows, cols, colStride int, x []float32) {
	for i := 0; i < rows; i++ {
		for j := 0; j < cols; j++ {
			out[i] += float32(float32(weights[j*colStride+i]) * x[j])
		}
	}
}

func analysisComputeDense(layer *analysisDenseLayer, output, input []float32) {
	M := layer.nbInputs
	N := layer.nbNeurons
	for i := 0; i < N; i++ {
		output[i] = float32(layer.bias[i])
	}
	gemmAccum(output, layer.inputWeights, N, M, N, input)
	for i := 0; i < N; i++ {
		output[i] *= mlpWeightsScale
	}
	if layer.sigmoid {
		for i := 0; i < N; i++ {
			output[i] = sigmoidApprox(output[i])
		}
	} else {
		for i := 0; i < N; i++ {
			output[i] = tansigApprox(output[i])
		}
	}
}

func analysisComputeGRU(gru *analysisGRULayer, state, input []float32) {
	var tmp, z, r, h [analysisMaxNeurons]float32
	M := gru.nbInputs
	N := gru.nbNeurons
	stride := 3 * N
	// Compute update gate.
	for i := 0; i < N; i++ {
		z[i] = float32(gru.bias[i])
	}
	gemmAccum(z[:], gru.inputWeights, N, M, stride, input)
	gemmAccum(z[:], gru.recurrentWeights, N, N, stride, state)
	for i := 0; i < N; i++ {
		z[i] = sigmoidApprox(mlpWeightsScale * z[i])
	}
	// Compute reset gate.
	for i := 0; i < N; i++ {
		r[i] = float32(gru.bias[N+i])
	}
	gemmAccum(r[:], gru.inputWeights[N:], N, M, stride, input)
	gemmAccum(r[:], gru.recurrentWeights[N:], N, N, stride, state)
	for i := 0; i < N; i++ {
		r[i] = sigmoidApprox(mlpWeightsScale * r[i])
	}
	// Compute output.
	for i := 0; i < N; i++ {
		h[i] = float32(gru.bias[2*N+i])
	}
	for i := 0; i < N; i++ {
		tmp[i] = state[i] * r[i]
	}
	gemmAccum(h[:], gru.inputWeights[2*N:], N, M, stride, input)
	gemmAccum(h[:], gru.recurrentWeights[2*N:], N, N, stride, tmp[:])
	for i := 0; i < N; i++ {
		h[i] = float32(z[i]*state[i]) + float32((1-z[i])*tansigApprox(mlpWeightsScale*h[i]))
	}
	for i := 0; i < N; i++ {
		state[i] = h[i]
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
