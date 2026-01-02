package silk

import (
	"fmt"
)

// Decoder represents a SILK decoder instance
type Decoder struct {
	sampleRate   int       // Sample rate (8000, 12000, 16000, 24000)
	frameSize    int       // Frame size in samples
	channels     int       // Number of channels (1 or 2)
	lpcOrder     int       // LPC order based on bandwidth
	prevOutput   []float64 // Previous frame output for PLC
	prevLPC      []float64 // Previous LPC coefficients
	prevPitchLag int       // Previous pitch lag
	prevGains    []float64 // Previous subframe gains
	plcCount     int       // Packet loss concealment counter
}

// NewDecoder creates a new SILK decoder
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
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

	return &Decoder{
		sampleRate:   sampleRate,
		frameSize:    frameSize,
		channels:     channels,
		lpcOrder:     lpcOrder,
		prevOutput:   make([]float64, frameSize),
		prevLPC:      make([]float64, lpcOrder),
		prevPitchLag: 100,
		prevGains:    []float64{1.0, 1.0, 1.0, 1.0},
		plcCount:     0,
	}, nil
}

// Decode decodes a frame of audio data
func (d *Decoder) Decode(packet []byte) ([]float64, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("empty packet")
	}

	// Check for silence packet
	if len(packet) == 1 && packet[0] == 0x00 {
		return d.decodeSilence()
	}

	// Unpack packet
	isSpeech, nlsfIndices, pitchLag, gainIndices, err := d.unpackFrame(packet)
	if err != nil {
		// Packet loss concealment
		return d.concealPacketLoss()
	}

	if !isSpeech {
		return d.decodeSilence()
	}

	// Dequantize NLSF
	nlsf := DequantizeNLSF(nlsfIndices)
	if nlsf == nil {
		return d.concealPacketLoss()
	}

	// Convert NLSF to LPC
	lpcCoeffs := LSFToLPC(nlsf)
	if lpcCoeffs == nil {
		return d.concealPacketLoss()
	}

	// Dequantize gains
	gains := DequantizeSubframeGains(gainIndices)

	// Generate excitation signal (simplified - would normally decode residual from bitstream)
	excitation := d.generateExcitation(pitchLag, gains)

	// Apply LPC synthesis
	output := SynthesizeLPC(excitation, lpcCoeffs)

	// Update state
	d.prevOutput = output
	d.prevLPC = lpcCoeffs
	d.prevPitchLag = pitchLag
	d.prevGains = gains
	d.plcCount = 0

	// For stereo, duplicate to both channels
	if d.channels == 2 {
		stereo := make([]float64, len(output)*2)
		for i, sample := range output {
			stereo[i*2] = sample
			stereo[i*2+1] = sample
		}
		return stereo, nil
	}

	return output, nil
}

// decodeSilence returns a silent frame
func (d *Decoder) decodeSilence() ([]float64, error) {
	output := make([]float64, d.frameSize*d.channels)
	return output, nil
}

// concealPacketLoss performs packet loss concealment
func (d *Decoder) concealPacketLoss() ([]float64, error) {
	d.plcCount++
	
	// Fade out over multiple lost packets
	fadeGain := 1.0 / float64(d.plcCount+1)
	
	// Generate using previous parameters
	excitation := d.generateExcitation(d.prevPitchLag, d.prevGains)
	
	// Apply fade
	for i := range excitation {
		excitation[i] *= fadeGain
	}
	
	// Synthesize with previous LPC
	output := SynthesizeLPC(excitation, d.prevLPC)
	
	// For stereo, duplicate to both channels
	if d.channels == 2 {
		stereo := make([]float64, len(output)*2)
		for i, sample := range output {
			stereo[i*2] = sample
			stereo[i*2+1] = sample
		}
		return stereo, nil
	}
	
	return output, nil
}

// unpackFrame unpacks encoded parameters from bitstream
func (d *Decoder) unpackFrame(packet []byte) (bool, []int, int, []int, error) {
	if len(packet) < 10 {
		return false, nil, 0, nil, fmt.Errorf("packet too short: %d bytes", len(packet))
	}

	// Voice activity
	isSpeech := packet[0] != 0x00

	// NLSF indices (2 stages, 2 bytes each)
	nlsfIndices := []int{
		int(packet[1])<<8 | int(packet[2]),
		int(packet[3])<<8 | int(packet[4]),
	}

	// Pitch lag (2 bytes)
	pitchLag := int(packet[5])<<8 | int(packet[6])

	// Validate pitch lag
	if pitchLag < MinPitchLag || pitchLag > MaxPitchLag {
		return false, nil, 0, nil, fmt.Errorf("invalid pitch lag: %d", pitchLag)
	}

	// Gain indices (4 subframes)
	gainIndices := []int{
		int(packet[7]),
		int(packet[8]),
		int(packet[9]),
	}
	
	// Add 4th index if packet is long enough
	if len(packet) > 10 {
		gainIndices = append(gainIndices, int(packet[10]))
	} else {
		gainIndices = append(gainIndices, 0)
	}

	return isSpeech, nlsfIndices, pitchLag, gainIndices, nil
}

// generateExcitation generates excitation signal from pitch and gains
func (d *Decoder) generateExcitation(pitchLag int, gains []float64) []float64 {
	excitation := make([]float64, d.frameSize)
	
	// Number of subframes
	numSubframes := len(gains)
	subframeSize := d.frameSize / numSubframes
	
	// Generate pitch-based excitation for each subframe
	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSize
		end := start + subframeSize
		gain := gains[sf]
		
		// Ensure minimum gain
		if gain < 0.01 {
			gain = 0.01
		}
		
		for i := start; i < end && i < d.frameSize; i++ {
			// Use pitch lag to generate periodic component
			if i >= pitchLag && pitchLag > 0 {
				excitation[i] = excitation[i-pitchLag] * 0.8
			}
			
			// Add white noise component with better scaling
			noise := (float64(i*7+sf*13) / 100.0) // Pseudo-random
			noise = noise - float64(int(noise))   // Fractional part [0, 1)
			noise = (noise - 0.5) * 2.0            // Scale to [-1, 1]
			
			// Combine periodic and noise components
			excitation[i] += noise * gain
		}
	}
	
	return excitation
}

// Reset resets the decoder state
func (d *Decoder) Reset() {
	for i := range d.prevOutput {
		d.prevOutput[i] = 0
	}
	for i := range d.prevLPC {
		d.prevLPC[i] = 0
	}
	d.prevPitchLag = 100
	d.prevGains = []float64{1.0, 1.0, 1.0, 1.0}
	d.plcCount = 0
}

// DequantizeSubframeGains dequantizes subframe gain indices
func DequantizeSubframeGains(indices []int) []float64 {
	gains := make([]float64, len(indices))
	
	for i, index := range indices {
		// Dequantize from 3 dB step
		gainDB := float64(index) * 3.0
		gains[i] = DBToLinear(gainDB)
	}
	
	return gains
}
