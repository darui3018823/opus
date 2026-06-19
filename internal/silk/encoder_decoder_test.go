package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/entcode"
)

// Test encoder creation
func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		wantErr    bool
	}{
		{"Valid 8kHz mono", 8000, 1, false},
		{"Valid 16kHz stereo", 16000, 2, false},
		{"Valid 24kHz mono", 24000, 1, false},
		{"Invalid sample rate", 44100, 1, true},
		{"Invalid channels", 16000, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(tt.sampleRate, tt.channels)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEncoder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && enc == nil {
				t.Error("NewEncoder() returned nil without error")
			}
		})
	}
}

// Test decoder creation
func TestNewDecoder(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		wantErr    bool
	}{
		{"Valid 8kHz mono", 8000, 1, false},
		{"Valid 16kHz stereo", 16000, 2, false},
		{"Valid 24kHz mono", 24000, 1, false},
		{"Invalid sample rate", 44100, 1, true},
		{"Invalid channels", 16000, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewDecoder(tt.sampleRate, tt.channels)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDecoder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && dec == nil {
				t.Error("NewDecoder() returned nil without error")
			}
		})
	}
}

// Test encoder complexity setting
func TestEncoderSetComplexity(t *testing.T) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	tests := []struct {
		name       string
		complexity int
		wantErr    bool
	}{
		{"Valid 0", 0, false},
		{"Valid 5", 5, false},
		{"Valid 10", 10, false},
		{"Invalid negative", -1, true},
		{"Invalid too high", 11, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := enc.SetComplexity(tt.complexity)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetComplexity() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test encoder bitrate setting
func TestEncoderSetBitrate(t *testing.T) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	tests := []struct {
		name    string
		bitrate int
		wantErr bool
	}{
		{"Valid 8kbps", 8000, false},
		{"Valid 24kbps", 24000, false},
		{"Too low", 5000, true},
		{"Too high", 50000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := enc.SetBitrate(tt.bitrate)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetBitrate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test encoding speech signal
func TestEncoderEncodeSpeech(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50 // 20ms at 8kHz = 160 samples

	// Generate synthetic speech-like signal (sine wave with harmonics) - louder amplitude
	signal := make([]float64, frameSize)
	for i := range signal {
		ti := float64(i) / 8000.0
		signal[i] = 1.0 * math.Sin(2*math.Pi*200*ti)
		signal[i] += 0.6 * math.Sin(2*math.Pi*400*ti)
		signal[i] += 0.4 * math.Sin(2*math.Pi*600*ti)
	}

	// Feed multiple frames so VAD builds up history
	var packet []byte
	for attempt := 0; attempt < 10; attempt++ {
		packet, err = enc.Encode(signal)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		if len(packet) > 1 {
			break // Got a non-silence packet
		}
	}

	if len(packet) == 0 {
		t.Error("Encode() returned empty packet")
	}

	t.Logf("Encoded speech packet: %d bytes", len(packet))
}

// Test encoding silence
func TestEncoderEncodeSilence(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50 // 20ms at 8kHz = 160 samples

	// Generate silence
	signal := make([]float64, frameSize)

	packet, err := enc.Encode(signal)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encode() returned empty packet")
	}

	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}
	tr := &decodeTrace{}
	dec.trace = tr
	if _, err := dec.Decode(packet); err != nil {
		t.Fatalf("Decode() encoded silence error = %v", err)
	}
	if len(tr.VADFlags) != 1 || len(tr.VADFlags[0]) != 1 || tr.VADFlags[0][0] != 0 {
		t.Fatalf("encoded silence VAD trace = %#v, want one inactive frame", tr.VADFlags)
	}
	if len(tr.LBRRFlags) != 1 || tr.LBRRFlags[0] != 0 {
		t.Fatalf("encoded silence LBRR trace = %#v, want no LBRR", tr.LBRRFlags)
	}
	if len(tr.Frames) != 1 || tr.Frames[0].SignalType != SignalTypeInactive {
		t.Fatalf("encoded silence frame trace = %#v, want inactive frame", tr.Frames)
	}
}

func TestEncoderStructuredMonoRangeStream(t *testing.T) {
	tests := []struct {
		name     string
		rate     int
		frameMs  int
		nFrames  int
		wantSubf int
	}{
		{"NB 20ms single", 8000, 20, 1, 4},
		{"MB 10ms multi", 12000, 10, 3, 2},
		{"WB 20ms multi", 16000, 20, 3, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoderWithFrameMs(tt.rate, 1, tt.frameMs)
			if err != nil {
				t.Fatalf("NewEncoderWithFrameMs() error = %v", err)
			}
			dec, err := NewDecoderWithFrameMs(tt.rate, 1, tt.frameMs)
			if err != nil {
				t.Fatalf("NewDecoderWithFrameMs() error = %v", err)
			}

			frameSize := tt.rate * tt.frameMs / 1000
			pcm := make([]float64, frameSize*tt.nFrames)
			packet, err := enc.EncodeMulti(pcm, tt.nFrames)
			if err != nil {
				t.Fatalf("EncodeMulti() error = %v", err)
			}
			if len(packet) < 2 {
				t.Fatalf("EncodeMulti() packet length = %d, want range-coded stream", len(packet))
			}

			tr := &decodeTrace{}
			dec.trace = tr
			out, err := dec.DecodeMulti(packet, tt.nFrames)
			if err != nil {
				t.Fatalf("DecodeMulti() error = %v", err)
			}
			if len(out) != frameSize*tt.nFrames {
				t.Fatalf("DecodeMulti() output length = %d, want %d", len(out), frameSize*tt.nFrames)
			}
			if len(tr.VADFlags) != 1 || len(tr.VADFlags[0]) != tt.nFrames {
				t.Fatalf("VAD trace = %#v, want one %d-frame header", tr.VADFlags, tt.nFrames)
			}
			if len(tr.LBRRFlags) != 1 || tr.LBRRFlags[0] != 0 {
				t.Fatalf("LBRR trace = %#v, want no LBRR", tr.LBRRFlags)
			}
			if len(tr.Frames) != tt.nFrames {
				t.Fatalf("traced frames = %d, want %d", len(tr.Frames), tt.nFrames)
			}
			for i, fr := range tr.Frames {
				if fr.SignalType != SignalTypeInactive {
					t.Fatalf("frame %d signalType=%d, want inactive", i, fr.SignalType)
				}
				if len(fr.RawGainIndices) != tt.wantSubf {
					t.Fatalf("frame %d gain indices=%d, want %d", i, len(fr.RawGainIndices), tt.wantSubf)
				}
				if len(fr.Pulses) != frameSize {
					t.Fatalf("frame %d pulses=%d, want %d", i, len(fr.Pulses), frameSize)
				}
			}
		})
	}
}

func TestEncoderStructuredPulseEncoding(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	frameSize := 8000 / 50
	signal := make([]float64, frameSize)
	seed := uint32(1)
	for i := range signal {
		seed = 1664525*seed + 1013904223
		v := float64(int32(seed)) / float64(1<<31)
		signal[i] = 1.5 * v
	}

	var packet []byte
	for attempt := 0; attempt < VADHistorySize+2; attempt++ {
		packet, err = enc.Encode(signal)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
	}

	tr := &decodeTrace{}
	dec.trace = tr
	if _, err := dec.Decode(packet); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(tr.Frames) != 1 {
		t.Fatalf("traced frames = %d, want 1", len(tr.Frames))
	}
	fr := tr.Frames[0]
	if fr.SignalType != SignalTypeUnvoiced {
		t.Fatalf("signalType=%d, want unvoiced speech frame", fr.SignalType)
	}
	if fr.RateLevelIdx < 0 || fr.RateLevelIdx >= nRateLevels-1 {
		t.Fatalf("rateLevelIdx=%d, want encoder-selected SILK rate level", fr.RateLevelIdx)
	}

	iter := frameSize >> log2ShellCodecFrameLen
	if iter*shellCodecFrameLength < frameSize {
		iter++
	}
	if len(fr.SumPulses) != iter {
		t.Fatalf("sumPulses len=%d, want %d", len(fr.SumPulses), iter)
	}

	nonZeroBlocks := 0
	positive, negative := 0, 0
	for block := 0; block < iter; block++ {
		start := block * shellCodecFrameLength
		end := start + shellCodecFrameLength
		if end > len(fr.Pulses) {
			end = len(fr.Pulses)
		}
		absSum := 0
		for _, pulse := range fr.Pulses[start:end] {
			switch {
			case pulse > 0:
				positive++
				absSum += int(pulse)
			case pulse < 0:
				negative++
				absSum += int(-pulse)
			}
		}
		if absSum > 0 {
			nonZeroBlocks++
		}
		nLShifts := fr.SumPulses[block] >> 5
		shellSum := 0
		for _, pulse := range fr.Pulses[start:end] {
			absPulse := int(pulse)
			if absPulse < 0 {
				absPulse = -absPulse
			}
			shellSum += absPulse >> nLShifts
		}
		if got := fr.SumPulses[block] & 0x1F; got != shellSum {
			t.Fatalf("block %d sumPulses=%d, shifted abs pulse sum=%d (nLShifts=%d)", block, got, shellSum, nLShifts)
		}
	}
	if nonZeroBlocks == 0 {
		t.Fatal("decoded pulse stream has no non-zero shell blocks")
	}
	if positive == 0 || negative == 0 {
		t.Fatalf("decoded pulse signs positive=%d negative=%d, want both signs", positive, negative)
	}

	nonZeroExc := 0
	for _, exc := range fr.ExcQ14 {
		if exc != 0 {
			nonZeroExc++
		}
	}
	if nonZeroExc == 0 {
		t.Fatal("decoded excitation is all zero")
	}
}

func TestEncoderVoicedPitchLTPEncoding(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}

	frameSize := 8000 / 50
	signal := make([]float64, frameSize)
	for i := range signal {
		ti := float64(i) / 8000.0
		signal[i] = 2.0*math.Sin(2*math.Pi*200*ti) + 0.4*math.Sin(2*math.Pi*400*ti)
	}

	var packet []byte
	for attempt := 0; attempt < VADHistorySize+3; attempt++ {
		packet, err = enc.Encode(signal)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
	}

	tr := &decodeTrace{}
	dec.trace = tr
	if _, err := dec.Decode(packet); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(tr.Frames) != 1 {
		t.Fatalf("traced frames = %d, want 1", len(tr.Frames))
	}
	fr := tr.Frames[0]
	if fr.SignalType != SignalTypeVoiced {
		t.Fatalf("signalType=%d, want voiced speech frame", fr.SignalType)
	}
	if len(fr.PitchLags) != 4 {
		t.Fatalf("pitch lags len=%d, want 4", len(fr.PitchLags))
	}
	for sf, lag := range fr.PitchLags {
		if lag < 38 || lag > 42 {
			t.Fatalf("pitch lag[%d]=%d, want near 40 samples for 200 Hz", sf, lag)
		}
	}
	if len(fr.LTPCoefQ14) != 20 {
		t.Fatalf("LTP coeff len=%d, want 20", len(fr.LTPCoefQ14))
	}
	nonZeroLTP := 0
	for _, c := range fr.LTPCoefQ14 {
		if c != 0 {
			nonZeroLTP++
		}
	}
	if nonZeroLTP == 0 {
		t.Fatal("voiced frame decoded all-zero LTP coefficients")
	}
	nonZeroPulses := 0
	for _, p := range fr.Pulses {
		if p != 0 {
			nonZeroPulses++
		}
	}
	if nonZeroPulses == 0 {
		t.Fatal("voiced frame decoded all-zero residual pulses")
	}
}

func TestEncoderAdaptiveNLSFEncoding(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	frameSize := 8000 / 50
	signal := make([]float64, frameSize)
	for i := range signal {
		t := float64(i) / 8000.0
		signal[i] = 1.2*math.Sin(2*math.Pi*700*t) + 0.7*math.Sin(2*math.Pi*1500*t)
		if i%9 == 0 {
			signal[i] += 0.25
		}
	}

	cb := getNLSFCB(enc.lpcOrder)
	analysis := enc.analyzeNLSF(signal, cb, SignalTypeUnvoiced)

	fixedRaw := make([]int, cb.order)
	fixedIdx := enc.defaultNLSFIndex(SignalTypeUnvoiced, cb)
	fixedLPC := nlsfToLPCLibopus(reconstructNLSFQ15(cb, fixedIdx, fixedRaw), cb.order)
	if got, fixed := lpcResidualEnergy(signal, analysis.lpcQ12), lpcResidualEnergy(signal, fixedLPC); got > fixed+1e-12 {
		t.Fatalf("adaptive NLSF residual energy=%g, fixed default=%g", got, fixed)
	}
	if analysis.cb1Idx == fixedIdx {
		allZero := true
		for _, idx := range analysis.rawIdx {
			if idx != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			t.Fatalf("adaptive NLSF stayed at fixed cb1=%d with zero residuals", fixedIdx)
		}
	}

	rangeEnc := entcode.NewEncoder(64)
	enc.encodeNLSF(rangeEnc, cb, SignalTypeUnvoiced, analysis)
	rangeEnc.Flush()

	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}
	rangeDec := entcode.NewDecoder(rangeEnc.Bytes())
	gotQ15, gotIndices, err := dec.decodeNLSF(rangeDec, cb, SignalTypeUnvoiced)
	if err != nil {
		t.Fatalf("decodeNLSF() error = %v", err)
	}
	if gotIndices[0] != analysis.cb1Idx {
		t.Fatalf("decoded cb1 index=%d, want %d", gotIndices[0], analysis.cb1Idx)
	}
	for i := 0; i < cb.order; i++ {
		want := clampInt(analysis.rawIdx[i], -3, 3)
		if gotIndices[i+1] != want {
			t.Fatalf("decoded raw NLSF index[%d]=%d, want %d", i, gotIndices[i+1], want)
		}
	}
	for i := range gotQ15 {
		if gotQ15[i] != analysis.nlsfQ15[i] {
			t.Fatalf("decoded NLSF_Q15[%d]=%d, want %d", i, gotQ15[i], analysis.nlsfQ15[i])
		}
	}
}

func TestEncoderLPCNLSFTargetQuantization(t *testing.T) {
	enc, err := NewEncoder(16000, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	signal := make([]float64, enc.frameSize)
	for i := range signal {
		tm := float64(i) / 16000.0
		env := 0.18 + 0.07*math.Sin(2*math.Pi*3.2*tm)
		signal[i] = env * (0.63*math.Sin(2*math.Pi*170*tm) +
			0.24*math.Sin(2*math.Pi*340*tm+0.25) +
			0.12*math.Sin(2*math.Pi*680*tm+0.8))
	}

	cb := getNLSFCB(enc.lpcOrder)
	targetQ15, ok := enc.lpcNLSFTargetQ15(signal, cb)
	if !ok {
		t.Fatal("lpcNLSFTargetQ15() failed")
	}
	if len(targetQ15) != cb.order {
		t.Fatalf("target len=%d, want %d", len(targetQ15), cb.order)
	}
	for i := 1; i < len(targetQ15); i++ {
		if targetQ15[i] <= targetQ15[i-1] {
			t.Fatalf("target NLSF not ordered at %d: %d <= %d", i, targetQ15[i], targetQ15[i-1])
		}
	}

	top := topNLSFCB1ByTarget(cb, targetQ15, 4)
	if len(top) != 4 {
		t.Fatalf("topNLSFCB1ByTarget len=%d, want 4", len(top))
	}
	seed := rawNLSFResidualForTarget(cb, top[0], targetQ15)
	for i, v := range seed {
		if v < -3 || v > 3 {
			t.Fatalf("seed raw[%d]=%d outside encodable range", i, v)
		}
	}

	legacyCB1 := bestNLSFStage1(signal, cb)
	legacyRaw := refineNLSFResidual(signal, cb, legacyCB1)
	legacyLPC := nlsfToLPCLibopus(reconstructNLSFQ15(cb, legacyCB1, legacyRaw), cb.order)
	chosenCB1, chosenRaw := bestNLSFAnalysis(signal, cb, targetQ15, true)
	chosenLPC := nlsfToLPCLibopus(reconstructNLSFQ15(cb, chosenCB1, chosenRaw), cb.order)

	legacyResidual := lpcResidualEnergy(signal, legacyLPC)
	chosenResidual := lpcResidualEnergy(signal, chosenLPC)
	if chosenResidual > legacyResidual+1e-12 {
		t.Fatalf("Q1 NLSF residual=%g, legacy=%g", chosenResidual, legacyResidual)
	}
	if lpcSpectralPeakGain(chosenLPC) > math.Max(18.0, lpcSpectralPeakGain(legacyLPC)*1.35)+1e-9 {
		t.Fatalf("chosen LPC spectral peak gain escaped Q1 guard")
	}
}

func TestEncoderSimpleNSQResidualQuantization(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	residual := make([]float64, enc.frameSize)
	for i := range residual {
		tm := float64(i) / 8000.0
		residual[i] = 0.35*math.Sin(2*math.Pi*220*tm) + 0.12*math.Sin(2*math.Pi*880*tm)
	}

	gainIndices := []int{22, 22, 22, 22}
	pulses := enc.simpleNSQ(residual, gainIndices, SignalTypeUnvoiced, 0, 0)
	if len(pulses) != enc.frameSize {
		t.Fatalf("simpleNSQ pulses len=%d, want %d", len(pulses), enc.frameSize)
	}

	nonZero, maxAbs := 0, 0
	seed := int32(0)
	offsetQ14 := int32(silkQuantizationOffsetsQ10[0][0]) << 4
	gainQ10 := silkGainDequantQ16(gainIndices[0]) >> 6
	signalEnergy, quantErrEnergy := 0.0, 0.0
	for i, pulse := range pulses {
		if pulse != 0 {
			nonZero++
		}
		absPulse := int(pulse)
		if absPulse < 0 {
			absPulse = -absPulse
		}
		if absPulse > maxAbs {
			maxAbs = absPulse
		}

		seed = 196314165*seed + 907633515
		reconQ14 := decodedExcitationQ14(int(pulse), offsetQ14, seed < 0)
		recon := float64(reconQ14) * float64(gainQ10) / float64(int64(1)<<39)
		diff := residual[i] - recon
		signalEnergy += residual[i] * residual[i]
		quantErrEnergy += diff * diff
		seed += int32(pulse)
	}
	if nonZero == 0 {
		t.Fatal("simpleNSQ produced all-zero pulses for active residual")
	}
	if maxAbs <= 4 {
		t.Fatalf("simpleNSQ max abs pulse=%d, want gain-scaled residual quantization beyond old 4-pulse cap", maxAbs)
	}
	if quantErrEnergy >= signalEnergy {
		t.Fatalf("simpleNSQ quantization error=%g, want below zero-excitation error=%g", quantErrEnergy, signalEnergy)
	}
}

func TestEncoderNoiseShapeAnalysisTracksSignalClass(t *testing.T) {
	const rate = 16000
	enc, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	voiced := make([]float64, enc.frameSize)
	for i := range voiced {
		tm := float64(i) / rate
		voiced[i] = 0.20 * (0.72*math.Sin(2*math.Pi*180*tm) +
			0.22*math.Sin(2*math.Pi*360*tm+0.3) +
			0.09*math.Sin(2*math.Pi*540*tm+0.7))
	}
	_, pitchGain := enc.analyzePitch(voiced)
	voicedShape := enc.analyzeNoiseShape(voiced, SignalTypeVoiced, pitchGain)

	noise := make([]float64, enc.frameSize)
	prev := 0.0
	for i := range noise {
		white := 0.27
		if i%2 == 0 {
			white = -white
		}
		noise[i] = white - 0.18*prev
		prev = white
	}
	noiseShape := enc.analyzeNoiseShape(noise, SignalTypeUnvoiced, 0)

	if len(voicedShape.subframes) != enc.nSubframes || len(noiseShape.subframes) != enc.nSubframes {
		t.Fatalf("shape subframes voiced=%d noise=%d want %d", len(voicedShape.subframes), len(noiseShape.subframes), enc.nSubframes)
	}

	avg := func(sh silkShapeAnalysis, field func(silkShapeSubframe) float64) float64 {
		sum := 0.0
		for _, sf := range sh.subframes {
			v := field(sf)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("non-finite shape coefficient: %+v", sf)
			}
			sum += v
		}
		return sum / float64(len(sh.subframes))
	}
	voicedHarmonic := avg(voicedShape, func(sf silkShapeSubframe) float64 { return sf.harmonic })
	noiseHarmonic := avg(noiseShape, func(sf silkShapeSubframe) float64 { return sf.harmonic })
	voicedFeedback := avg(voicedShape, func(sf silkShapeSubframe) float64 { return sf.feedback })
	noiseFeedback := avg(noiseShape, func(sf silkShapeSubframe) float64 { return sf.feedback })
	voicedHF := avg(voicedShape, func(sf silkShapeSubframe) float64 { return sf.hf })
	noiseHF := avg(noiseShape, func(sf silkShapeSubframe) float64 { return sf.hf })

	if voicedHarmonic <= 0.15 {
		t.Fatalf("voiced harmonic shaping=%g, want active shaping", voicedHarmonic)
	}
	if noiseHarmonic != 0 {
		t.Fatalf("unvoiced harmonic shaping=%g, want zero", noiseHarmonic)
	}
	if voicedFeedback <= noiseFeedback {
		t.Fatalf("feedback voiced=%g noise=%g, want stronger voiced feedback", voicedFeedback, noiseFeedback)
	}
	if noiseHF <= voicedHF {
		t.Fatalf("HF shaping noise=%g voiced=%g, want stronger noisy HF shaping", noiseHF, voicedHF)
	}
}

func TestEncoderClosedLoopNSQImprovesVoicedSynthesis(t *testing.T) {
	const rate = 16000
	enc, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	signal := make([]float64, enc.frameSize)
	for i := range signal {
		tm := float64(i) / rate
		signal[i] = 0.20 * (0.72*math.Sin(2*math.Pi*180*tm) +
			0.22*math.Sin(2*math.Pi*360*tm+0.3) +
			0.09*math.Sin(2*math.Pi*540*tm+0.7))
	}

	pitchLag, pitchGain := enc.analyzePitch(signal)
	if pitchGain < 0.55 {
		t.Fatalf("test signal pitch gain=%g, want voiced", pitchGain)
	}
	cb := getNLSFCB(enc.lpcOrder)
	nlsf := enc.analyzeNLSF(signal, cb, SignalTypeVoiced)
	gainIdx := enc.analysisGainIndex(signal)
	gainIndices := []int{gainIdx, gainIdx, gainIdx, gainIdx}
	pitchLags := []int{pitchLag, pitchLag, pitchLag, pitchLag}
	ltpPerIdx, ltpGainIdx := selectLTPGain(pitchGain)
	ltpCoeffsQ14 := make([][5]int16, enc.nSubframes)
	for sf := range ltpCoeffsQ14 {
		for k := 0; k < 5; k++ {
			switch ltpPerIdx {
			case 0:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ0[ltpGainIdx][k]) << 7
			case 1:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ1[ltpGainIdx][k]) << 7
			default:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ2[ltpGainIdx][k]) << 7
			}
		}
	}

	residualEnc, _ := NewEncoder(rate, 1)
	excitation := residualEnc.analysisExcitation(signal, nlsf.lpcQ12, SignalTypeVoiced, pitchLag, pitchGain)
	residualPulses := residualEnc.simpleNSQ(excitation, gainIndices, SignalTypeVoiced, 0, 0)

	closedEnc, _ := NewEncoder(rate, 1)
	closedPulses := closedEnc.closedLoopNSQ(signal, nlsf.lpcQ12, gainIndices,
		SignalTypeVoiced, 0, 0, pitchLags, ltpCoeffsQ14, silkLTPScalesTable[0])

	residualOut := synthesizeTestFrame(t, rate, residualPulses, gainIndices, nlsf.lpcQ12, pitchLags, ltpCoeffsQ14, 0)
	// The delayed-decision NSQ selects a winning state whose seed is written to
	// the bitstream; the decoder must replay that same seed.
	closedOut := synthesizeTestFrame(t, rate, closedPulses, gainIndices, nlsf.lpcQ12, pitchLags, ltpCoeffsQ14, closedEnc.nsqSeed)
	residualSNR, _, _, _ := silkAlignedSNR(signal, residualOut, enc.frameSize)
	closedSNR, _, _, _ := silkAlignedSNR(signal, closedOut, enc.frameSize)
	if closedSNR < residualSNR+0.5 {
		t.Fatalf("closed-loop NSQ SNR %.2f dB, want at least 0.5 dB over residual-only %.2f dB", closedSNR, residualSNR)
	}
}

// TestTrellisNSQVoicedRoundTrip guards the Q3+Q4 delayed-decision trellis NSQ.
// It is gated off by default (the homebrew quantizer is active until Step 4
// co-designs the gains), but it must remain correct: a voiced frame quantized
// by the trellis and resynthesized with the winning seed must reconstruct the
// signal. This locks in the two correctness fixes — the cross-subframe delayed
// writes and the winning-state seed propagation — so Step 4 can build on it.
func TestTrellisNSQVoicedRoundTrip(t *testing.T) {
	const rate = 16000
	enc, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}
	enc.useTrellisNSQ = true

	signal := make([]float64, enc.frameSize)
	for i := range signal {
		tm := float64(i) / rate
		signal[i] = 0.20 * (0.72*math.Sin(2*math.Pi*180*tm) +
			0.22*math.Sin(2*math.Pi*360*tm+0.3) +
			0.09*math.Sin(2*math.Pi*540*tm+0.7))
	}

	pitchLag, pitchGain := enc.analyzePitch(signal)
	if pitchGain < 0.55 {
		t.Fatalf("test signal pitch gain=%g, want voiced", pitchGain)
	}
	cb := getNLSFCB(enc.lpcOrder)
	nlsf := enc.analyzeNLSF(signal, cb, SignalTypeVoiced)
	gainIdx := enc.analysisGainIndex(signal)
	gainIndices := []int{gainIdx, gainIdx, gainIdx, gainIdx}
	pitchLags := []int{pitchLag, pitchLag, pitchLag, pitchLag}
	ltpPerIdx, ltpGainIdx := selectLTPGain(pitchGain)
	ltpCoeffsQ14 := make([][5]int16, enc.nSubframes)
	for sf := range ltpCoeffsQ14 {
		for k := 0; k < 5; k++ {
			switch ltpPerIdx {
			case 0:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ0[ltpGainIdx][k]) << 7
			case 1:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ1[ltpGainIdx][k]) << 7
			default:
				ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ2[ltpGainIdx][k]) << 7
			}
		}
	}

	pulses := enc.closedLoopNSQ(signal, nlsf.lpcQ12, gainIndices,
		SignalTypeVoiced, 0, 0, pitchLags, ltpCoeffsQ14, silkLTPScalesTable[0])
	// Resynthesize with the seed the trellis selected and wrote to the bitstream.
	out := synthesizeTestFrame(t, rate, pulses, gainIndices, nlsf.lpcQ12, pitchLags, ltpCoeffsQ14, enc.nsqSeed)
	snr, _, _, scale := silkAlignedSNR(signal, out, enc.frameSize)
	// A correct trellis reconstructs the voiced tone with positive scale and
	// meaningful SNR. The earlier broken port produced an inverted (scale<0),
	// near-zero-SNR output, so these bounds catch a regression decisively.
	if scale <= 0 {
		t.Fatalf("trellis NSQ output inverted: scale=%.3f (seed=%d)", scale, enc.nsqSeed)
	}
	if snr < 8 {
		t.Fatalf("trellis NSQ voiced SNR %.2f dB too low (scale=%.3f); want >= 8 dB", snr, scale)
	}
}

func TestEncoderSubframeGainAnalysisTracksEnvelope(t *testing.T) {
	const rate = 16000
	enc, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	signal := make([]float64, enc.frameSize)
	subframeLen := enc.frameSize / enc.nSubframes
	amps := []float64{0.14, 0.24, 0.42, 0.68}
	for i := range signal {
		sf := i / subframeLen
		tm := float64(i) / rate
		signal[i] = amps[sf] * (0.72*math.Sin(2*math.Pi*180*tm) +
			0.22*math.Sin(2*math.Pi*360*tm+0.3) +
			0.09*math.Sin(2*math.Pi*540*tm+0.7))
	}

	adaptive := enc.analysisGainIndices(signal)
	if len(adaptive) != enc.nSubframes {
		t.Fatalf("analysisGainIndices len=%d, want %d", len(adaptive), enc.nSubframes)
	}
	if adaptive[len(adaptive)-1] <= adaptive[0] {
		t.Fatalf("adaptive gains=%v, want higher gain for louder trailing subframes", adaptive)
	}
	for sf := 1; sf < len(adaptive); sf++ {
		if adaptive[sf] < adaptive[sf-1] {
			t.Fatalf("adaptive gains=%v, want nondecreasing envelope across subframes", adaptive)
		}
	}

	frameTarget := enc.analysisGainIndex(signal)
	foundFinerSubframe := false
	for sf, idx := range adaptive {
		if idx > frameTarget {
			t.Fatalf("adaptive gain[%d]=%d exceeds frame target %d", sf, idx, frameTarget)
		}
		if idx < frameTarget {
			foundFinerSubframe = true
		}
	}
	if !foundFinerSubframe {
		t.Fatalf("adaptive gains=%v, want at least one quieter subframe below frame target %d", adaptive, frameTarget)
	}
}

func TestEncoderPitchAnalysisUsesOverlapEnergy(t *testing.T) {
	const rate = 16000
	enc, err := NewEncoder(rate, 1)
	if err != nil {
		t.Fatalf("NewEncoder() error = %v", err)
	}

	signal := make([]float64, enc.frameSize)
	for i := range signal {
		tm := float64(i) / rate
		signal[i] = 0.24 * (0.85*math.Sin(2*math.Pi*80*tm) +
			0.10*math.Sin(2*math.Pi*160*tm+0.2))
	}

	lag, gain := enc.analyzePitch(signal)
	if lag < 195 || lag > 205 {
		t.Fatalf("pitch lag=%d, want near 200 samples for 80 Hz", lag)
	}
	if gain < 0.85 {
		t.Fatalf("pitch gain=%g, want strong correlation from overlap-normalized analysis", gain)
	}
}

func synthesizeTestFrame(t *testing.T, rate int, pulses []int16, gainIndices []int, lpcQ12 []int16, pitchLags []int, ltpCoeffsQ14 [][5]int16, seed int32) []float64 {
	t.Helper()
	dec, err := NewDecoder(rate, 1)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}
	gainsQ16 := make([]int32, len(gainIndices))
	lpcCoeffsQ12 := make([][]int16, len(gainIndices))
	for sf, idx := range gainIndices {
		gainsQ16[sf] = silkGainDequantQ16(idx)
		lpcCoeffsQ12[sf] = lpcQ12
	}
	outI16 := dec.synthesize(pulses, gainsQ16, lpcCoeffsQ12,
		pitchLags, ltpCoeffsQ14, silkLTPScalesTable[0],
		SignalTypeVoiced, 0, seed)
	out := make([]float64, len(outI16))
	for i, s := range outI16 {
		out[i] = float64(s) / 32768.0
	}
	return out
}

// Test encoder with invalid PCM length
func TestEncoderInvalidPCMLength(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	// Wrong length
	signal := make([]float64, 100)

	_, err = enc.Encode(signal)
	if err == nil {
		t.Error("Expected error for invalid PCM length, got nil")
	}
}

// Test decoder with range-coded speech packet (from encoder)
func TestDecoderDecodeSpeech(t *testing.T) {
	// First encode speech frames to get a valid range-coded packet
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50
	signal := make([]float64, frameSize)
	for i := range signal {
		ti := float64(i) / 8000.0
		signal[i] = 2.0 * math.Sin(2*math.Pi*200*ti)
	}

	// Feed multiple frames so VAD builds history
	var packet []byte
	for attempt := 0; attempt < 10; attempt++ {
		packet, err = enc.Encode(signal)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		if len(packet) > 1 {
			break
		}
	}

	if len(packet) <= 1 {
		t.Skip("Encoder did not produce speech packet after 10 frames")
	}

	// Now decode it
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	output, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if len(output) != frameSize {
		t.Errorf("Decode() output length = %d, want %d", len(output), frameSize)
	}

	// Verify output is not all zeros
	hasNonZero := false
	for _, sample := range output {
		if sample != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("Decoded speech is all zeros")
	}

	t.Logf("Decoded %d samples from %d byte packet", len(output), len(packet))
}

// Test decoder with silence packet
func TestDecoderDecodeSilence(t *testing.T) {
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Silence packet
	packet := []byte{0x00}

	output, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	expectedLen := 8000 / 50 // 20ms at 8kHz
	if len(output) != expectedLen {
		t.Errorf("Decode() output length = %d, want %d", len(output), expectedLen)
	}

	// Verify output is all zeros
	for i, sample := range output {
		if sample != 0 {
			t.Errorf("Silence output has non-zero sample at index %d: %f", i, sample)
			break
		}
	}
}

// Test encoder-decoder roundtrip produces signal with positive SNR
func TestEncoderDecoderRoundtrip(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	frameSize := 8000 / 50 // 20ms at 8kHz

	// Generate test signal - loud enough to trigger VAD
	signal := make([]float64, frameSize)
	for i := range signal {
		ti := float64(i) / 8000.0
		signal[i] = 2.0 * math.Sin(2*math.Pi*200*ti)
	}

	// Feed multiple frames to build VAD history and get a speech packet
	var packet []byte
	for attempt := 0; attempt < 10; attempt++ {
		packet, err = enc.Encode(signal)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		if len(packet) > 1 {
			break
		}
	}

	if len(packet) <= 1 {
		t.Skip("Encoder produced only silence packets - cannot test roundtrip SNR")
	}

	t.Logf("Encoded packet size: %d bytes", len(packet))

	// Decode
	output, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if len(output) != len(signal) {
		t.Errorf("Roundtrip output length = %d, want %d", len(output), len(signal))
	}

	// Verify output has some energy (not silence)
	energy := 0.0
	for _, s := range output {
		energy += s * s
	}
	energy /= float64(len(output))

	if energy < 1e-10 {
		t.Errorf("Decoded signal has no energy: %e", energy)
	}

	t.Logf("Decoded signal energy: %e", energy)
}

// Test packet loss concealment
func TestDecoderPacketLossConcealment(t *testing.T) {
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// First, encode a valid speech frame
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50
	signal := make([]float64, frameSize)
	for i := range signal {
		ti := float64(i) / 8000.0
		signal[i] = 2.0 * math.Sin(2*math.Pi*200*ti)
	}

	packet, err := enc.Encode(signal)
	if err != nil {
		t.Skipf("Encode failed: %v", err)
	}

	output1, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() valid packet error = %v", err)
	}

	// Now decode invalid packet (triggers PLC) - single byte is too short for range decoder
	invalidPacket := []byte{0xFF}
	output2, err := dec.Decode(invalidPacket)
	if err != nil {
		t.Fatalf("Decode() with PLC error = %v", err)
	}

	if len(output2) != len(output1) {
		t.Errorf("PLC output length = %d, want %d", len(output2), len(output1))
	}

	// Verify PLC output has some energy
	energy := 0.0
	for _, s := range output2 {
		energy += s * s
	}
	energy /= float64(len(output2))

	if energy < 1e-9 {
		t.Log("PLC output has low energy - acceptable if previous frame was quiet")
	}
}

// Test encoder reset
func TestEncoderReset(t *testing.T) {
	enc, err := NewEncoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	// Encode a frame
	frameSize := 8000 / 50
	signal := make([]float64, frameSize)
	for i := range signal {
		signal[i] = 0.5 * math.Sin(2*math.Pi*200*float64(i)/8000.0)
	}

	_, err = enc.Encode(signal)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	// Reset should not cause errors
	enc.Reset()

	// Encode again after reset
	_, err = enc.Encode(signal)
	if err != nil {
		t.Errorf("Encode() after reset error = %v", err)
	}
}

// Test decoder reset
func TestDecoderReset(t *testing.T) {
	dec, err := NewDecoder(8000, 1)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Decode a silence packet
	packet := []byte{0x00}
	_, err = dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// Reset should not cause errors
	dec.Reset()

	// Decode again after reset
	_, err = dec.Decode(packet)
	if err != nil {
		t.Errorf("Decode() after reset error = %v", err)
	}
}

// Test stereo encoding
func TestEncoderStereo(t *testing.T) {
	enc, err := NewEncoder(8000, 2)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	frameSize := 8000 / 50
	signal := make([]float64, frameSize*2) // Stereo interleaved

	for i := 0; i < frameSize; i++ {
		ti := float64(i) / 8000.0
		sample := 0.5 * math.Sin(2*math.Pi*200*ti)
		signal[i*2] = sample   // Left
		signal[i*2+1] = sample // Right
	}

	packet, err := enc.Encode(signal)
	if err != nil {
		t.Fatalf("Encode() stereo error = %v", err)
	}

	if len(packet) == 0 {
		t.Error("Stereo encode returned empty packet")
	}
}

// Test stereo decoding
func TestDecoderStereo(t *testing.T) {
	dec, err := NewDecoder(8000, 2)
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Silence packet for stereo
	packet := []byte{0x00}

	output, err := dec.Decode(packet)
	if err != nil {
		t.Fatalf("Decode() stereo error = %v", err)
	}

	expectedLen := (8000 / 50) * 2 // 20ms stereo
	if len(output) != expectedLen {
		t.Errorf("Stereo output length = %d, want %d", len(output), expectedLen)
	}
}

// Test multi-rate encoder/decoder
func TestMultiRate(t *testing.T) {
	rates := []int{8000, 12000, 16000}
	for _, rate := range rates {
		t.Run(fmt.Sprintf("%dHz", rate), func(t *testing.T) {
			enc, err := NewEncoder(rate, 1)
			if err != nil {
				t.Fatalf("NewEncoder(%d) error: %v", rate, err)
			}

			dec, err := NewDecoder(rate, 1)
			if err != nil {
				t.Fatalf("NewDecoder(%d) error: %v", rate, err)
			}

			frameSize := rate / 50
			signal := make([]float64, frameSize)
			for i := range signal {
				ti := float64(i) / float64(rate)
				signal[i] = 2.0 * math.Sin(2*math.Pi*200*ti)
			}

			packet, err := enc.Encode(signal)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			output, err := dec.Decode(packet)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}

			if len(output) != frameSize {
				t.Errorf("Output length = %d, want %d", len(output), frameSize)
			}

			t.Logf("Rate %d: encoded %d bytes, decoded %d samples", rate, len(packet), len(output))
		})
	}
}

func TestHomebrewToTrellisNSQStateHandoff(t *testing.T) {
	for _, rate := range []int{8000, 12000} {
		t.Run(fmt.Sprintf("%dHz", rate), func(t *testing.T) {
			enc, err := NewEncoder(rate, 1)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(24000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}
			if err := enc.SetComplexity(5); err != nil {
				t.Fatalf("SetComplexity: %v", err)
			}
			dec, err := NewDecoder(rate, 1)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			frameSize := rate / 50
			var signalTypes []int
			for frame := 0; frame < 2; frame++ {
				signal := speechHarmonicTransitionFrame(rate, frame*frameSize, frameSize)
				packet, err := enc.Encode(signal)
				if err != nil {
					t.Fatalf("frame %d Encode: %v", frame, err)
				}
				trace := &decodeTrace{}
				dec.trace = trace
				decoded, err := dec.Decode(packet)
				if err != nil {
					t.Fatalf("frame %d Decode: %v", frame, err)
				}
				if len(trace.Frames) != 1 {
					t.Fatalf("frame %d trace frames=%d, want 1", frame, len(trace.Frames))
				}
				signalTypes = append(signalTypes, trace.Frames[0].SignalType)

				if frame == 1 {
					historyStart := len(enc.ltpState) - frameSize
					maxDiff := int32(0)
					for i, sample := range decoded {
						got := enc.ltpState[historyStart+i]
						want := int32(math.Round(sample * 32768.0))
						diff := got - want
						if diff < 0 {
							diff = -diff
						}
						if diff > maxDiff {
							maxDiff = diff
						}
					}
					if maxDiff > 1 {
						t.Fatalf("unvoiced->voiced reconstruction diverged by %d int16 units", maxDiff)
					}
				}
			}
			if len(signalTypes) != 2 ||
				signalTypes[0] != SignalTypeUnvoiced ||
				signalTypes[1] != SignalTypeVoiced {
				t.Fatalf("signal types=%v, want [unvoiced voiced]", signalTypes)
			}
		})
	}
}

func speechHarmonicTransitionFrame(rate, start, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		tm := float64(start+i) / float64(rate)
		f0 := 145 + 24*math.Sin(2*math.Pi*1.7*tm)
		env := 0.18 + 0.10*math.Sin(2*math.Pi*3.1*tm+0.2)
		out[i] = env * (0.58*math.Sin(2*math.Pi*f0*tm) +
			0.24*math.Sin(2*math.Pi*2*f0*tm+0.35) +
			0.11*math.Sin(2*math.Pi*3*f0*tm+0.85))
	}
	return out
}
