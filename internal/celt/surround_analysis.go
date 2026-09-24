package celt

import (
	"fmt"

	"github.com/darui3018823/opus/internal/dsp"
)

// SurroundAnalyzer is libopus's surround_analysis (float build): the
// multichannel masking analysis of opus_multistream_encode_native for
// mapping family 1. It holds the per-channel pre-emphasis and MDCT overlap
// memories; Analyze returns each channel's 21 band signal-to-mask ratios
// (celt_glog, log2 amplitude), channel-major.
type SurroundAnalyzer struct {
	sampleRate int
	channels   int
	overlap    [][]float64 // float32 values
	preemphMem []float64   // float32 values
	modes      map[int]*dsp.CELTMode
}

func NewSurroundAnalyzer(sampleRate, channels int) (*SurroundAnalyzer, error) {
	if sampleRate <= 0 || 48000%sampleRate != 0 {
		return nil, fmt.Errorf("celt: unsupported surround analysis rate %d", sampleRate)
	}
	if channels < 3 || channels > 8 {
		return nil, fmt.Errorf("celt: unsupported surround channel count %d", channels)
	}
	a := &SurroundAnalyzer{
		sampleRate: sampleRate,
		channels:   channels,
		overlap:    make([][]float64, channels),
		preemphMem: make([]float64, channels),
		modes:      map[int]*dsp.CELTMode{},
	}
	for channel := range a.overlap {
		a.overlap[channel] = make([]float64, 120)
	}
	return a, nil
}

func (a *SurroundAnalyzer) Clone() *SurroundAnalyzer {
	clone := &SurroundAnalyzer{
		sampleRate: a.sampleRate,
		channels:   a.channels,
		overlap:    make([][]float64, a.channels),
		preemphMem: append([]float64(nil), a.preemphMem...),
		modes:      a.modes,
	}
	for channel := range a.overlap {
		clone.overlap[channel] = append([]float64(nil), a.overlap[channel]...)
	}
	return clone
}

func (a *SurroundAnalyzer) Reset() {
	for channel := range a.overlap {
		clear(a.overlap[channel])
		a.preemphMem[channel] = 0
	}
}

// Analyze runs surround_analysis on frameSize samples per channel of
// interleaved PCM (nominal range [-1, 1]).
func (a *SurroundAnalyzer) Analyze(pcm []float64, frameSize int) ([]float64, error) {
	if frameSize <= 0 || len(pcm) < frameSize*a.channels {
		return nil, fmt.Errorf("celt: invalid surround analysis frame")
	}
	const overlap = 120
	upsample := 48000 / a.sampleRate
	n := frameSize * upsample
	// LM = log2(frame_size / 120), at most maxLM = 3 (longer frames take
	// several 20 ms MDCTs).
	lm := 0
	for lm = 0; lm < 3; lm++ {
		if 120<<lm == n {
			break
		}
	}
	freqSize := 120 << lm
	if n < 120 || n%freqSize != 0 {
		return nil, fmt.Errorf("celt: unsupported surround analysis frame size %d", frameSize)
	}
	mode := a.modes[freqSize]
	if mode == nil {
		mode = dsp.NewCELTMode(freqSize, overlap, celtWindow(overlap))
		a.modes[freqSize] = mode
	}
	M := 1 << lm
	pos := surroundChannelPos(a.channels)
	bandLogE := make([]float64, a.channels*NumBands48000)
	var maskLogE [3][NumBands48000]float32
	for c := range maskLogE {
		for i := range maskLogE[c] {
			maskLogE[c][i] = -28
		}
	}
	in := make([]float64, n+overlap)
	freq := make([]float64, freqSize)
	for c := 0; c < a.channels; c++ {
		copy(in, a.overlap[c])
		// celt_preemphasis (upsampled input is zero-stuffed).
		m := float32(a.preemphMem[c])
		for i := 0; i < n; i++ {
			var x float32
			if i%upsample == 0 {
				x = float32(32768) * float32(pcm[(i/upsample)*a.channels+c])
			}
			in[overlap+i] = float64(x - m)
			m = float32(preemphCoef0 * x)
		}
		a.preemphMem[c] = float64(m)
		// Filter out both NaNs and ridiculous signals that could cause NaNs
		// further down.
		var sum float32
		for _, v := range in {
			f := float32(v)
			sum += float32(f * f)
		}
		if !(sum < 1e18) || sum != sum {
			clear(in)
			a.preemphMem[c] = 0
		}
		var bandE [NumBands48000]float32
		for frame := 0; frame < n/freqSize; frame++ {
			copy(freq, mode.CLTMDCTForward(in[freqSize*frame:freqSize*frame+freqSize+overlap]))
			if upsample != 1 {
				bound := freqSize / upsample
				for i := 0; i < bound; i++ {
					freq[i] = float64(float32(freq[i]) * float32(upsample))
				}
				clear(freq[bound:])
			}
			// If there are multiple frames, take the max energy.
			for i := 0; i < NumBands48000; i++ {
				tmp := float32(bandEnergy32(freq[M*int(EBands48000[i]) : M*int(EBands48000[i+1])]))
				if tmp > bandE[i] {
					bandE[i] = tmp
				}
			}
		}
		var logE [NumBands48000]float32
		for i := range logE {
			logE[i] = float32(amp2Log2Band(float64(bandE[i]), i))
		}
		// Spreading function with -6 dB/band going up and -12 dB/band going
		// down.
		for i := 1; i < NumBands48000; i++ {
			logE[i] = max(logE[i], logE[i-1]-1)
		}
		for i := NumBands48000 - 2; i >= 0; i-- {
			logE[i] = max(logE[i], logE[i+1]-2)
		}
		switch pos[c] {
		case 1:
			for i := range logE {
				maskLogE[0][i] = surroundLogSum(maskLogE[0][i], logE[i])
			}
		case 3:
			for i := range logE {
				maskLogE[2][i] = surroundLogSum(maskLogE[2][i], logE[i])
			}
		case 2:
			for i := range logE {
				maskLogE[0][i] = surroundLogSum(maskLogE[0][i], logE[i]-0.5)
				maskLogE[2][i] = surroundLogSum(maskLogE[2][i], logE[i]-0.5)
			}
		}
		for i, v := range logE {
			bandLogE[c*NumBands48000+i] = float64(v)
		}
		copy(a.overlap[c], in[n:n+overlap])
	}
	for i := 0; i < NumBands48000; i++ {
		maskLogE[1][i] = min(maskLogE[0][i], maskLogE[2][i])
	}
	channelOffset := float32(0.5) * float32(celtLog2F32(float64(float32(2)/float32(a.channels-1))))
	for c := range maskLogE {
		for i := range maskLogE[c] {
			maskLogE[c][i] += channelOffset
		}
	}
	for c := 0; c < a.channels; c++ {
		row := bandLogE[c*NumBands48000 : (c+1)*NumBands48000]
		if pos[c] == 0 {
			clear(row)
			continue
		}
		mask := &maskLogE[pos[c]-1]
		for i := range row {
			row[i] = float64(float32(row[i]) - mask[i])
		}
	}
	return bandLogE, nil
}

// surroundDiffTable is logSum's diff_table (only the first nine entries
// are set).
var surroundDiffTable = [17]float32{0.5000000, 0.2924813, 0.1609640, 0.0849625,
	0.0437314, 0.0221971, 0.0111839, 0.0056136, 0.0028123}

// surroundLogSum is logSum: a rough approximation of log2(2^a + 2^b).
func surroundLogSum(a, b float32) float32 {
	maxV, diff := b, b-a
	if a > b {
		maxV, diff = a, a-b
	}
	if !(diff < 8) { // inverted to catch NaNs
		return maxV
	}
	twice := float32(2 * diff)
	low := int(float32(int(twice)))
	frac := twice - float32(low)
	return maxV + surroundDiffTable[low] + float32(frac*(surroundDiffTable[low+1]-surroundDiffTable[low]))
}

// surroundChannelPos is channel_pos: 0 don't mix, 1 left, 2 center, 3 right.
func surroundChannelPos(channels int) []int {
	switch channels {
	case 4:
		return []int{1, 3, 1, 3}
	case 3, 5, 6:
		return []int{1, 2, 3, 1, 3, 0}[:channels]
	case 7:
		return []int{1, 2, 3, 1, 3, 2, 0}
	case 8:
		return []int{1, 2, 3, 1, 3, 1, 3, 0}
	default:
		return make([]int, channels)
	}
}
