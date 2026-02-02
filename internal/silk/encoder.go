package silk

import (
	"fmt"
	"math"
)

// Encoder represents a SILK encoder instance
type Encoder struct {
	sampleRate   int       // Sample rate (8000, 12000, 16000, 24000)
	frameSize    int       // Frame size in samples
	channels     int       // Number of channels (1 or 2)
	lpcOrder     int       // LPC order based on bandwidth
	complexity   int       // Complexity (0-10)
	bitrate      int       // Target bitrate in bps
	vad          *VAD      // Voice activity detector
	prevEnergy   float64   // Previous frame energy for smoothing
	prevLPC      []float64 // Previous LPC coefficients
	prevPitchLag int       // Previous pitch lag
}

// NewEncoder creates a new SILK encoder
func NewEncoder(sampleRate, channels int) (*Encoder, error) {
	if sampleRate != 8000 && sampleRate != 12000 && sampleRate != 16000 && sampleRate != 24000 {
		return nil, fmt.Errorf("invalid sample rate: %d (must be 8000, 12000, 16000, or 24000)", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channels: %d (must be 1 or 2)", channels)
	}

	// Determine LPC order based on bandwidth
	lpcOrder := 10 // Default for narrowband
	if sampleRate >= 12000 {
		lpcOrder = 12 // Medium band
	}
	if sampleRate >= 16000 {
		lpcOrder = 16 // Wideband
	}

	// Default frame size: 20ms
	frameSize := sampleRate / 50 // 20ms

	return &Encoder{
		sampleRate:   sampleRate,
		frameSize:    frameSize,
		channels:     channels,
		lpcOrder:     lpcOrder,
		complexity:   5, // Default complexity
		bitrate:      sampleRate * channels * 16 / 8, // Default bitrate
		vad:          NewVAD(),
		prevEnergy:   1.0,
		prevLPC:      make([]float64, lpcOrder),
		prevPitchLag: 100,
	}, nil
}

// SetComplexity sets the computational complexity (0-10)
func (e *Encoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return fmt.Errorf("complexity must be between 0 and 10, got %d", complexity)
	}
	e.complexity = complexity
	return nil
}

// SetBitrate sets the target bitrate in bps
func (e *Encoder) SetBitrate(bitrate int) error {
	// SILK typical range: 6-40 kbps
	if bitrate < 6000 || bitrate > 40000 {
		return fmt.Errorf("bitrate must be between 6000 and 40000 bps, got %d", bitrate)
	}
	e.bitrate = bitrate
	return nil
}

// Encode encodes a frame of audio samples
func (e *Encoder) Encode(pcm []float64) ([]byte, error) {
	if len(pcm) != e.frameSize*e.channels {
		return nil, fmt.Errorf("invalid PCM length: got %d, expected %d", len(pcm), e.frameSize*e.channels)
	}

	// For stereo, process only the first channel (or mix)
	signal := pcm
	if e.channels == 2 {
		// Extract left channel for simplicity
		signal = make([]float64, e.frameSize)
		for i := 0; i < e.frameSize; i++ {
			signal[i] = pcm[i*2]
		}
	}

	// Voice activity detection
	isSpeech := e.vad.Detect(signal)
	if !isSpeech {
		// Return minimal packet for silence
		return e.encodeSilence()
	}

	// LPC analysis
	lpcCoeffs := AnalyzeLPC(signal, e.lpcOrder)
	if lpcCoeffs == nil {
		return nil, fmt.Errorf("LPC analysis failed")
	}

	// Convert to NLSF for quantization
	nlsf := LPCToLSF(lpcCoeffs)
	if nlsf == nil {
		return nil, fmt.Errorf("LPC to LSF conversion failed")
	}

	// Quantize NLSF
	quantizedNLSF, nlsfIndices := QuantizeNLSF(nlsf)
	if quantizedNLSF == nil {
		return nil, fmt.Errorf("NLSF quantization failed")
	}

	// Compute LPC residual
	residual := ComputeResidual(signal, lpcCoeffs)

	// Pitch analysis on residual
	pitchLag, pitchGain := DetectPitch(residual, MinPitchLag, MaxPitchLag)
	
	// Apply pitch prediction
	pitchResidual := ApplyPitchPrediction(residual, pitchLag, pitchGain)

	// Compute subframe gains
	subframeGains := ComputeSubframeGains(pitchResidual, 4)
	
	// Quantize gains
	quantizedGains, gainIndices := QuantizeSubframeGains(subframeGains)

	// Pack encoded data
	packet := e.packFrame(isSpeech, nlsfIndices, pitchLag, gainIndices, quantizedGains)

	// Update state for next frame
	e.prevLPC = lpcCoeffs
	e.prevPitchLag = pitchLag
	e.prevEnergy = computeEnergy(signal)

	return packet, nil
}

// encodeSilence creates a minimal packet for silence
func (e *Encoder) encodeSilence() ([]byte, error) {
	// Minimal silence packet: 1 byte indicating silence
	return []byte{0x00}, nil
}

// packFrame packs encoded parameters into a bitstream
func (e *Encoder) packFrame(isSpeech bool, nlsfIndices []int, pitchLag int, gainIndices []int, gains []float64) []byte {
	// Simplified packing (actual implementation would use range coder)
	packet := make([]byte, 0, 32)
	
	// Header: voice activity (1 bit = 1 byte for simplicity)
	if isSpeech {
		packet = append(packet, 0x01)
	} else {
		packet = append(packet, 0x00)
	}
	
	// NLSF indices (2 bytes per stage)
	for _, idx := range nlsfIndices {
		packet = append(packet, byte(idx>>8), byte(idx))
	}
	
	// Pitch lag (2 bytes)
	packet = append(packet, byte(pitchLag>>8), byte(pitchLag))
	
	// Gain indices (1 byte per subframe)
	for _, idx := range gainIndices {
		packet = append(packet, byte(idx))
	}
	
	return packet
}

// Reset resets the encoder state
func (e *Encoder) Reset() {
	e.vad.Reset()
	e.prevEnergy = 1.0
	for i := range e.prevLPC {
		e.prevLPC[i] = 0
	}
	e.prevPitchLag = 100
}

// computeEnergy computes signal energy
func computeEnergy(signal []float64) float64 {
	if len(signal) == 0 {
		return 0
	}
	energy := 0.0
	for _, s := range signal {
		energy += s * s
	}
	return energy / float64(len(signal))
}

// QuantizeSubframeGains quantizes subframe gains
func QuantizeSubframeGains(gains []float64) ([]float64, []int) {
	quantized := make([]float64, len(gains))
	indices := make([]int, len(gains))
	
	for i, g := range gains {
		// Convert to dB
		gainDB := LinearToDB(g)
		
		// Quantize to nearest 3 dB step
		step := 3.0
		index := int(math.Round(gainDB / step))
		
		// Clamp to valid range
		if index < -20 {
			index = -20
		}
		if index > 13 {
			index = 13
		}
		
		indices[i] = index
		quantized[i] = DBToLinear(float64(index) * step)
	}
	
	return quantized, indices
}
