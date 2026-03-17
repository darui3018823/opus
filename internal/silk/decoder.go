package silk

import (
	"fmt"
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// Signal types per RFC 6716 Section 4.2.7.3
const (
	SignalTypeInactive = 0
	SignalTypeUnvoiced = 1
	SignalTypeVoiced   = 2
)

// ICDF tables for SILK frame decoding (RFC 6716 Section 4.2.7)
// These are inverse CDF tables used with the range decoder.
// With ftb=8 (ft=256), values are uint8.

// Signal type and quantization offset type (combined, 6 symbols)
var icdfSignalTypeQOffset = []uint8{231, 196, 151, 90, 36, 0}

// VAD flag probability (2 symbols: 0=no speech, 1=speech)
var icdfVAD = []uint8{128, 0}

// LBRR flag probability
var icdfLBRR = []uint8{26, 0}

// NLSF stage 1 index (32 entries, uniform)
var icdfNLSFStage1 [32]uint8

// NLSF stage 2 index (8 entries, uniform)
var icdfNLSFStage2 [8]uint8

// Pitch lag high bits (3 bits = 8 values)
var icdfPitchHighBits [8]uint8

// LTP filter index (3 codebooks)
var icdfLTPFilter = []uint8{171, 86, 0}

// Gain index (first subframe, 32 levels)
var icdfGainFirst [32]uint8

// Gain index (delta, 41 levels centered at 0)
var icdfGainDelta [41]uint8

// Excitation pulse count per subframe
var icdfExcPulseCount [19]uint8

func init() {
	// Initialize uniform ICDF tables
	for i := 0; i < 32; i++ {
		v := 256 - (i+1)*256/32
		if v < 0 {
			v = 0
		}
		icdfNLSFStage1[i] = uint8(v)
	}
	icdfNLSFStage1[31] = 0

	for i := 0; i < 8; i++ {
		v := 256 - (i+1)*256/8
		if v < 0 {
			v = 0
		}
		icdfNLSFStage2[i] = uint8(v)
	}
	icdfNLSFStage2[7] = 0

	for i := 0; i < 8; i++ {
		v := 256 - (i+1)*256/8
		if v < 0 {
			v = 0
		}
		icdfPitchHighBits[i] = uint8(v)
	}
	icdfPitchHighBits[7] = 0

	for i := 0; i < 32; i++ {
		v := 256 - (i+1)*256/32
		if v < 0 {
			v = 0
		}
		icdfGainFirst[i] = uint8(v)
	}
	icdfGainFirst[31] = 0

	for i := 0; i < 41; i++ {
		v := 256 - (i+1)*256/41
		if v < 0 {
			v = 0
		}
		icdfGainDelta[i] = uint8(v)
	}
	icdfGainDelta[40] = 0

	for i := 0; i < 19; i++ {
		v := 256 - (i+1)*256/19
		if v < 0 {
			v = 0
		}
		icdfExcPulseCount[i] = uint8(v)
	}
	icdfExcPulseCount[18] = 0
}

// Decoder represents a SILK decoder instance
type Decoder struct {
	sampleRate   int       // Sample rate (8000, 12000, 16000, 24000)
	frameSize    int       // Frame size in samples
	channels     int       // Number of channels (1 or 2)
	lpcOrder     int       // LPC order based on bandwidth
	prevOutput   []float64 // Previous frame output for PLC and LPC history
	prevLPC      []float64 // Previous LPC coefficients
	prevNLSF     []float64 // Previous NLSF for interpolation
	prevPitchLag int       // Previous pitch lag
	prevGains    []float64 // Previous subframe gains
	plcCount     int       // Packet loss concealment counter
	excSeed      uint32    // Deterministic seed for excitation noise
}

// NewDecoder creates a new SILK decoder
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	if sampleRate != 8000 && sampleRate != 12000 && sampleRate != 16000 && sampleRate != 24000 {
		return nil, fmt.Errorf("invalid sample rate: %d (must be 8000, 12000, 16000, or 24000)", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channels: %d (must be 1 or 2)", channels)
	}

	lpcOrder := 10
	if sampleRate >= 12000 {
		lpcOrder = 12
	}
	if sampleRate >= 16000 {
		lpcOrder = 16
	}

	frameSize := sampleRate / 50 // 20ms

	prevNLSF := make([]float64, lpcOrder)
	for i := range prevNLSF {
		prevNLSF[i] = math.Pi * float64(i+1) / float64(lpcOrder+1)
	}

	return &Decoder{
		sampleRate:   sampleRate,
		frameSize:    frameSize,
		channels:     channels,
		lpcOrder:     lpcOrder,
		prevOutput:   make([]float64, frameSize),
		prevLPC:      make([]float64, lpcOrder),
		prevNLSF:     prevNLSF,
		prevPitchLag: 100,
		prevGains:    []float64{1.0, 1.0, 1.0, 1.0},
		plcCount:     0,
		excSeed:      12345,
	}, nil
}

// Decode decodes a SILK frame from range-coded data.
func (d *Decoder) Decode(packet []byte) ([]float64, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("empty packet")
	}

	// Single-byte silence indicator
	if len(packet) == 1 && packet[0] == 0x00 {
		return d.decodeSilence()
	}

	// Need at least a few bytes for range decoder
	if len(packet) < 2 {
		return d.concealPacketLoss()
	}

	// Create range decoder
	dec := entcode.NewDecoder(packet)
	if dec.Error() != nil {
		return d.concealPacketLoss()
	}

	output, err := d.decodeFrame(dec)
	if err != nil {
		return d.concealPacketLoss()
	}

	return output, nil
}

// decodeFrame decodes a SILK frame using the range decoder per RFC 6716 Section 4.2.
func (d *Decoder) decodeFrame(dec *entcode.Decoder) ([]float64, error) {
	// 1. Decode VAD flag (1 bit via ICDF)
	vadFlag := dec.DecodeIcdf(icdfVAD[:], 8)

	// 2. Decode LBRR flag
	_ = dec.DecodeIcdf(icdfLBRR[:], 8) // lbrrFlag - consumed but not used in basic decoder

	// 3. Decode signal type and quantization offset type
	sigQOffIdx := dec.DecodeIcdf(icdfSignalTypeQOffset[:], 8)
	signalType := sigQOffIdx / 2 // 0=inactive, 1=unvoiced, 2=voiced
	_ = sigQOffIdx % 2           // quantization offset type (0 or 1)

	if vadFlag == 0 && signalType == SignalTypeInactive {
		return d.decodeSilence()
	}

	// 4. Decode NLSF indices
	nlsfIdx1 := dec.DecodeIcdf(icdfNLSFStage1[:], 8)
	nlsfIdx2 := dec.DecodeIcdf(icdfNLSFStage2[:], 8)
	nlsfIndices := []int{nlsfIdx1, nlsfIdx2}

	// Dequantize NLSF using codebooks
	nlsf := DequantizeNLSF(nlsfIndices, d.lpcOrder)

	// NLSF interpolation with previous frame
	interpFactor := 0.5
	interpNLSF := InterpolateNLSF(d.prevNLSF, nlsf, interpFactor)
	if interpNLSF == nil {
		interpNLSF = nlsf
	}

	// Convert NLSF to LPC
	lpcCoeffs := LSFToLPC(interpNLSF)
	if lpcCoeffs == nil {
		return nil, fmt.Errorf("NLSF to LPC conversion failed")
	}

	// 5. Decode pitch lag (if voiced)
	pitchLag := d.prevPitchLag
	var ltpCoeffs [5]float64 // LTP filter taps

	if signalType == SignalTypeVoiced {
		// Decode pitch lag: high bits + low bits
		pitchHigh := dec.DecodeIcdf(icdfPitchHighBits[:], 8)
		pitchLow := int(dec.DecodeBits(uint(6))) // 6 bits for fine resolution

		// Reconstruct pitch lag
		pitchLag = pitchHigh*64 + pitchLow + MinPitchLag
		if pitchLag < MinPitchLag {
			pitchLag = MinPitchLag
		}
		if pitchLag > MaxPitchLag {
			pitchLag = MaxPitchLag
		}

		// Decode LTP filter index
		ltpIdx := dec.DecodeIcdf(icdfLTPFilter[:], 8)
		// Use predefined LTP coefficients based on filter index
		switch ltpIdx {
		case 0:
			ltpCoeffs = [5]float64{0.0, 0.0, 0.5, 0.0, 0.0}
		case 1:
			ltpCoeffs = [5]float64{0.0, 0.1, 0.6, 0.1, 0.0}
		case 2:
			ltpCoeffs = [5]float64{0.05, 0.15, 0.5, 0.15, 0.05}
		}
	}

	// 6. Decode subframe gains
	numSubframes := 4
	subframeSize := d.frameSize / numSubframes
	gains := make([]float64, numSubframes)

	// First subframe: absolute gain index
	gainIdx0 := dec.DecodeIcdf(icdfGainFirst[:], 8)
	gains[0] = decodeGainLevel(gainIdx0)

	// Subsequent subframes: delta gain
	for sf := 1; sf < numSubframes; sf++ {
		deltaIdx := dec.DecodeIcdf(icdfGainDelta[:], 8)
		// Delta is centered at 20 (i.e., deltaIdx - 20 gives signed delta)
		deltaDB := float64(deltaIdx-20) * 1.5
		gains[sf] = gains[sf-1] * DBToLinear(deltaDB)
		if gains[sf] < 0.001 {
			gains[sf] = 0.001
		}
		if gains[sf] > 100.0 {
			gains[sf] = 100.0
		}
	}

	// 7. Decode excitation
	excitation := make([]float64, d.frameSize)

	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSize
		end := start + subframeSize
		if end > d.frameSize {
			end = d.frameSize
		}

		// Decode pulse count for this subframe
		pulseCount := dec.DecodeIcdf(icdfExcPulseCount[:], 8)

		// Generate excitation: combination of decoded pulses and noise
		for i := start; i < end; i++ {
			// Pitch-periodic component (LTP)
			if signalType == SignalTypeVoiced && pitchLag > 0 && i >= pitchLag {
				ltpSum := 0.0
				for k := -2; k <= 2; k++ {
					idx := i - pitchLag + k
					if idx >= 0 && idx < i {
						ltpSum += ltpCoeffs[k+2] * excitation[idx]
					} else if idx < 0 && len(d.prevOutput)+idx >= 0 {
						ltpSum += ltpCoeffs[k+2] * d.prevOutput[len(d.prevOutput)+idx]
					}
				}
				excitation[i] += ltpSum
			}

			// Stochastic component: decoded pulses mapped to excitation
			d.excSeed = d.excSeed*1664525 + 1013904223
			noise := float64(int32(d.excSeed)) / float64(1<<31)

			if pulseCount > 0 {
				excitation[i] += noise * gains[sf] * (1.0 + float64(pulseCount)*0.1)
			} else {
				excitation[i] += noise * gains[sf] * 0.5
			}
		}
	}

	// 8. LPC synthesis with history from previous frame
	lpc := NewLPCAnalysis(len(lpcCoeffs))
	copy(lpc.coeffs, lpcCoeffs)
	output := lpc.SynthesizeWithHistory(excitation, d.prevOutput)

	// Update decoder state
	d.prevOutput = make([]float64, len(output))
	copy(d.prevOutput, output)
	d.prevLPC = lpcCoeffs
	d.prevNLSF = nlsf
	d.prevPitchLag = pitchLag
	d.prevGains = gains
	d.plcCount = 0

	// Stereo expansion
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

// decodeGainLevel converts a gain index (0..31) to a linear gain value.
func decodeGainLevel(index int) float64 {
	gainDB := GainMinDB + float64(index)*(GainMaxDB-GainMinDB)/31.0
	gain := DBToLinear(gainDB)
	if gain < 0.001 {
		gain = 0.001
	}
	return gain
}

func (d *Decoder) decodeSilence() ([]float64, error) {
	return make([]float64, d.frameSize*d.channels), nil
}

func (d *Decoder) concealPacketLoss() ([]float64, error) {
	d.plcCount++
	fadeGain := 1.0 / float64(d.plcCount+1)

	excitation := d.generatePLCExcitation(d.prevPitchLag, d.prevGains)
	for i := range excitation {
		excitation[i] *= fadeGain
	}

	lpc := NewLPCAnalysis(len(d.prevLPC))
	copy(lpc.coeffs, d.prevLPC)
	output := lpc.SynthesizeWithHistory(excitation, d.prevOutput)

	d.prevOutput = make([]float64, len(output))
	copy(d.prevOutput, output)

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

// generatePLCExcitation generates excitation for packet loss concealment
func (d *Decoder) generatePLCExcitation(pitchLag int, gains []float64) []float64 {
	excitation := make([]float64, d.frameSize)
	numSubframes := len(gains)
	if numSubframes == 0 {
		numSubframes = 4
		gains = []float64{0.5, 0.5, 0.5, 0.5}
	}
	subframeSize := d.frameSize / numSubframes

	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSize
		end := start + subframeSize
		gain := gains[sf]
		if gain < 0.01 {
			gain = 0.01
		}

		for i := start; i < end && i < d.frameSize; i++ {
			if i >= pitchLag && pitchLag > 0 {
				excitation[i] = excitation[i-pitchLag] * 0.8
			}

			d.excSeed = d.excSeed*1664525 + 1013904223
			noise := float64(int32(d.excSeed)) / float64(1<<31)
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
	for i := range d.prevNLSF {
		d.prevNLSF[i] = math.Pi * float64(i+1) / float64(d.lpcOrder+1)
	}
	d.prevPitchLag = 100
	d.prevGains = []float64{1.0, 1.0, 1.0, 1.0}
	d.plcCount = 0
	d.excSeed = 12345
}

// DequantizeSubframeGains dequantizes subframe gain indices
func DequantizeSubframeGains(indices []int) []float64 {
	gains := make([]float64, len(indices))
	for i, index := range indices {
		gainDB := float64(index) * 3.0
		gains[i] = DBToLinear(gainDB)
	}
	return gains
}
