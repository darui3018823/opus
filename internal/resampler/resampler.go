// Package resampler provides high-quality sample rate conversion for Opus.
// This implementation is based on libopus's polyphase FIR resampler.
package resampler

import (
	"errors"
	"math"
)

// Supported sample rates for Opus
const (
	Rate8kHz  = 8000
	Rate12kHz = 12000
	Rate16kHz = 16000
	Rate24kHz = 24000
	Rate48kHz = 48000
)

// Quality levels for resampling
const (
	QualityMin     = 0  // Fastest, lower quality
	QualityDefault = 4  // Balanced
	QualityMax     = 10 // Slowest, highest quality
)

// Resampler performs high-quality sample rate conversion using polyphase FIR filtering.
type Resampler struct {
	inRate      int // Input sample rate
	outRate     int // Output sample rate
	numChannels int // Number of channels
	quality     int // Quality level (0-10)

	// Filter parameters
	filterLen  int     // Length of each polyphase filter
	oversample int     // Oversampling factor
	cutoff     float64 // Cutoff frequency

	// Filter coefficients (polyphase structure)
	coeffs []float64

	// State buffers for each channel
	mem [][]float64 // [channel][filter_len]

	// Fractional position tracking
	lastSample  []int    // Last input sample used per channel
	sampFracNum []uint32 // Fractional numerator
	sampFracDen uint32   // Fractional denominator
}

// NewResampler creates a new resampler for converting between sample rates.
func NewResampler(inRate, outRate, numChannels, quality int) (*Resampler, error) {
	if !isValidRate(inRate) {
		return nil, errors.New("resampler: invalid input sample rate")
	}
	if !isValidRate(outRate) {
		return nil, errors.New("resampler: invalid output sample rate")
	}
	if numChannels < 1 || numChannels > 255 {
		return nil, errors.New("resampler: invalid number of channels")
	}
	if quality < QualityMin || quality > QualityMax {
		return nil, errors.New("resampler: invalid quality level")
	}

	r := &Resampler{
		inRate:      inRate,
		outRate:     outRate,
		numChannels: numChannels,
		quality:     quality,
	}

	// Calculate GCD for rational resampling
	gcd := gcd(inRate, outRate)
	r.sampFracDen = uint32(outRate / gcd)

	// Determine filter parameters based on quality
	r.setFilterParams()

	// Generate filter coefficients
	r.generateCoeffs()

	// Initialize state buffers
	r.mem = make([][]float64, numChannels)
	for i := range r.mem {
		r.mem[i] = make([]float64, r.filterLen)
	}

	r.lastSample = make([]int, numChannels)
	r.sampFracNum = make([]uint32, numChannels)

	return r, nil
}

// setFilterParams determines filter parameters based on quality level.
func (r *Resampler) setFilterParams() {
	// Quality determines filter length and oversample factor
	// Higher quality = longer filter = better frequency response
	switch r.quality {
	case 0, 1:
		r.filterLen = 16
		r.oversample = 4
		r.cutoff = 0.80
	case 2, 3:
		r.filterLen = 32
		r.oversample = 8
		r.cutoff = 0.85
	case 4, 5, 6:
		r.filterLen = 48
		r.oversample = 16
		r.cutoff = 0.90
	case 7, 8:
		r.filterLen = 64
		r.oversample = 32
		r.cutoff = 0.92
	default: // 9, 10
		r.filterLen = 80
		r.oversample = 64
		r.cutoff = 0.94
	}
}

// generateCoeffs generates polyphase FIR filter coefficients using windowed sinc.
func (r *Resampler) generateCoeffs() {
	// Total number of coefficients
	totalLen := r.filterLen * r.oversample
	r.coeffs = make([]float64, totalLen)

	// Determine cutoff as fraction of Nyquist of the lower rate
	// Use the minimum of input/output rates
	cutoff := r.cutoff
	if r.outRate < r.inRate {
		// Downsampling: cutoff relative to output Nyquist
		cutoff = r.cutoff * float64(r.outRate) / float64(r.inRate)
	}

	// Generate windowed sinc filter
	center := float64(totalLen-1) / 2.0
	for i := 0; i < totalLen; i++ {
		// Distance from center (in units of input samples)
		x := (float64(i) - center) / float64(r.oversample)

		// Sinc function: sin(pi*cutoff*x) / (pi*x)
		var sinc float64
		if math.Abs(x) < 1e-10 {
			sinc = cutoff
		} else {
			pix := math.Pi * x * cutoff
			sinc = math.Sin(pix) / (math.Pi * x)
		}

		// Kaiser window
		kaiser := kaiserWindow(float64(i)/float64(totalLen-1), computeBeta(r.quality))

		r.coeffs[i] = sinc * kaiser
	}

	// Normalize to unit gain at DC
	sum := 0.0
	for i := 0; i < r.filterLen; i++ {
		// Sum one complete polyphase filter (phase 0)
		sum += r.coeffs[i*r.oversample]
	}

	if math.Abs(sum) > 1e-10 {
		scale := 1.0 / sum
		for i := range r.coeffs {
			r.coeffs[i] *= scale
		}
	}
}

// kaiserWindow computes Kaiser window value.
func kaiserWindow(x, beta float64) float64 {
	// x should be in [0, 1]
	// Kaiser window: I0(beta * sqrt(1 - (2x-1)^2)) / I0(beta)
	arg := 2*x - 1
	val := 1 - arg*arg
	if val < 0 {
		val = 0
	}
	return besselI0(beta*math.Sqrt(val)) / besselI0(beta)
}

// besselI0 computes modified Bessel function of the first kind, order 0.
func besselI0(x float64) float64 {
	// Series approximation
	sum := 1.0
	term := 1.0
	x2 := x * x / 4.0

	for k := 1; k < 50; k++ {
		term *= x2 / float64(k*k)
		sum += term
		if term < 1e-12*sum {
			break
		}
	}

	return sum
}

// computeBeta computes Kaiser window beta parameter from quality.
func computeBeta(quality int) float64 {
	// Higher quality = higher beta = narrower transition band
	return 3.0 + float64(quality)*0.5
}

// Process resamples input samples to output samples.
// Input should be interleaved if multi-channel.
func (r *Resampler) Process(input []float64) []float64 {
	if len(input) == 0 {
		return nil
	}

	inputLen := len(input) / r.numChannels

	// Estimate output length
	outputLen := int(uint64(inputLen) * uint64(r.outRate) / uint64(r.inRate))
	output := make([]float64, 0, outputLen*r.numChannels+r.numChannels*2)

	// Process each channel
	for ch := 0; ch < r.numChannels; ch++ {
		// Extract channel samples
		chInput := make([]float64, inputLen)
		for i := 0; i < inputLen; i++ {
			chInput[i] = input[i*r.numChannels+ch]
		}

		// Resample this channel
		chOutput := r.processChannel(ch, chInput)

		// Interleave output
		for i := 0; i < len(chOutput); i++ {
			if i*r.numChannels+ch >= len(output) {
				// Extend output buffer
				for len(output) <= i*r.numChannels+ch {
					output = append(output, 0)
				}
			}
			output[i*r.numChannels+ch] = chOutput[i]
		}
	}

	return output
}

// processChannel resamples a single channel.
func (r *Resampler) processChannel(ch int, input []float64) []float64 {
	inputLen := len(input)
	if inputLen == 0 {
		return nil
	}

	// Calculate expected output length
	// Calculate expected output length
	outputLen := (inputLen * r.outRate) / r.inRate
	// Pre-allocate to exact size plus small margin for rounding
	output := make([]float64, 0, outputLen+2)

	// Steps for rational resampling tracking
	// step = inRate / outRate = stepNum / stepDen
	stepNum := int(uint64(r.inRate) * uint64(r.sampFracDen) / uint64(r.outRate))
	stepDen := int(r.sampFracDen)

	// Restore state
	intPos := r.lastSample[ch]
	frac := int(r.sampFracNum[ch])

	for intPos < inputLen {
		// Select polyphase filter based on fractional position
		// phaseIdx = floor(frac * oversample / stepDen)
		phaseIdx := int((uint64(frac) * uint64(r.oversample)) / uint64(stepDen))
		if phaseIdx >= r.oversample {
			phaseIdx = r.oversample - 1
		}

		// Compute output using FIR filter
		outSample := 0.0
		halfLen := r.filterLen / 2

		for j := 0; j < r.filterLen; j++ {
			// Tap position (centered around intPos)
			tapPos := intPos - halfLen + j
			var tapVal float64

			if tapPos >= 0 && tapPos < inputLen {
				tapVal = input[tapPos]
			} else if tapPos < 0 {
				// Use memory from previous block
				memIdx := len(r.mem[ch]) + tapPos
				if memIdx >= 0 && memIdx < len(r.mem[ch]) {
					tapVal = r.mem[ch][memIdx]
				}
			}

			// Get coefficient
			coeffIdx := j*r.oversample + phaseIdx
			if coeffIdx < len(r.coeffs) {
				outSample += tapVal * r.coeffs[coeffIdx]
			}
		}

		output = append(output, outSample)

		// Advance time
		frac += stepNum
		intPos += frac / stepDen
		frac %= stepDen
	}

	// Save state
	r.lastSample[ch] = intPos - inputLen
	r.sampFracNum[ch] = uint32(frac)

	// Update memory with last samples
	memLen := len(r.mem[ch])
	if inputLen >= memLen {
		copy(r.mem[ch], input[inputLen-memLen:])
	} else {
		// Shift and append
		copy(r.mem[ch], r.mem[ch][inputLen:])
		copy(r.mem[ch][memLen-inputLen:], input)
	}

	return output
}

// Reset clears the resampler state.
func (r *Resampler) Reset() {
	for i := range r.mem {
		for j := range r.mem[i] {
			r.mem[i][j] = 0
		}
	}
	for i := range r.lastSample {
		r.lastSample[i] = 0
	}
	for i := range r.sampFracNum {
		r.sampFracNum[i] = 0
	}
}

// CopyStateFrom copies the complete configured and streaming state from src.
func (r *Resampler) CopyStateFrom(src *Resampler) {
	if src == nil || src == r {
		return
	}
	*r = *src
	r.coeffs = append([]float64(nil), src.coeffs...)
	r.mem = make([][]float64, len(src.mem))
	for ch := range src.mem {
		r.mem[ch] = append([]float64(nil), src.mem[ch]...)
	}
	r.lastSample = append([]int(nil), src.lastSample...)
	r.sampFracNum = append([]uint32(nil), src.sampFracNum...)
}

// Helper functions

func isValidRate(rate int) bool {
	return rate == Rate8kHz || rate == Rate12kHz || rate == Rate16kHz ||
		rate == Rate24kHz || rate == Rate48kHz
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
