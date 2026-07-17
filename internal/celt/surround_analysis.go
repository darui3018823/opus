package celt

import (
	"fmt"
	"math"

	"github.com/darui3018823/opus/internal/dsp"
)

// SurroundAnalyzer holds the overlap and pre-emphasis history used by the
// libopus-style multichannel masking analysis. It is internal to the public
// surround encoder; the returned values are log2-amplitude SMRs for 21 bands.
type SurroundAnalyzer struct {
	sampleRate int
	channels   int
	overlap    [][]float64
	preemphMem []float64
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

func (a *SurroundAnalyzer) Analyze(pcm []float64, frameSize int) ([]float64, error) {
	if frameSize <= 0 || len(pcm) < frameSize*a.channels {
		return nil, fmt.Errorf("celt: invalid surround analysis frame")
	}
	upsample := 48000 / a.sampleRate
	internalFrameSize := frameSize * upsample
	if internalFrameSize < 120 || internalFrameSize > 5760 || internalFrameSize%120 != 0 {
		return nil, fmt.Errorf("celt: unsupported surround analysis frame size %d", frameSize)
	}
	freqSize := internalFrameSize
	if freqSize > 960 {
		freqSize = 960
	}
	if freqSize != 120 && freqSize != 240 && freqSize != 480 && freqSize != 960 {
		return nil, fmt.Errorf("celt: unsupported surround transform size %d", freqSize)
	}
	mode := dsp.NewCELTMode(freqSize, 120, celtWindow(120))
	M := freqSize / 120
	roles := surroundChannelRoles(a.channels)
	bandLogE := make([]float64, a.channels*NumBands48000)
	masks := [3][NumBands48000]float64{}
	for role := range masks {
		for band := range masks[role] {
			masks[role][band] = -28
		}
	}

	for channel := 0; channel < a.channels; channel++ {
		preemphasized := make([]float64, internalFrameSize)
		for i := 0; i < frameSize; i++ {
			preemphasized[i*upsample] = pcm[i*a.channels+channel] * 32768
		}
		mem := a.preemphMem[channel]
		valid := true
		var energy float64
		for i, sample := range preemphasized {
			preemphasized[i] = sample - mem
			mem = 0.85 * sample
			energy += preemphasized[i] * preemphasized[i]
			if math.IsNaN(preemphasized[i]) || math.IsInf(preemphasized[i], 0) {
				valid = false
			}
		}
		if !valid || !(energy < 1e18) {
			clear(preemphasized)
			mem = 0
		}
		a.preemphMem[channel] = mem

		analysis := make([]float64, 120+internalFrameSize)
		copy(analysis, a.overlap[channel])
		copy(analysis[120:], preemphasized)
		copy(a.overlap[channel], analysis[internalFrameSize:internalFrameSize+120])
		bandEnergy := make([]float64, NumBands48000)
		for frame := 0; frame < internalFrameSize/freqSize; frame++ {
			coeffs := mode.CLTMDCTForward(analysis[frame*freqSize : frame*freqSize+freqSize+120])
			if upsample != 1 {
				bound := freqSize / upsample
				for i := 0; i < bound; i++ {
					coeffs[i] *= float64(upsample)
				}
				clear(coeffs[bound:])
			}
			for band := 0; band < NumBands48000; band++ {
				lo := M * int(EBands48000[band])
				hi := M * int(EBands48000[band+1])
				var sum float64
				for bin := lo; bin < hi; bin++ {
					sum += coeffs[bin] * coeffs[bin]
				}
				if sum > bandEnergy[band] {
					bandEnergy[band] = sum
				}
			}
		}
		base := channel * NumBands48000
		for band := 0; band < NumBands48000; band++ {
			bandLogE[base+band] = 0.5 * math.Log2(math.Max(1e-27, bandEnergy[band]))
		}
		for band := 1; band < NumBands48000; band++ {
			bandLogE[base+band] = math.Max(bandLogE[base+band], bandLogE[base+band-1]-1)
		}
		for band := NumBands48000 - 2; band >= 0; band-- {
			bandLogE[base+band] = math.Max(bandLogE[base+band], bandLogE[base+band+1]-2)
		}
		switch roles[channel] {
		case 1, 3:
			role := roles[channel] - 1
			for band := 0; band < NumBands48000; band++ {
				masks[role][band] = surroundLogSum(masks[role][band], bandLogE[base+band])
			}
		case 2:
			for band := 0; band < NumBands48000; band++ {
				center := bandLogE[base+band] - 0.5
				masks[0][band] = surroundLogSum(masks[0][band], center)
				masks[2][band] = surroundLogSum(masks[2][band], center)
			}
		}
	}

	channelOffset := 0.5 * math.Log2(2/float64(a.channels-1))
	for band := 0; band < NumBands48000; band++ {
		masks[1][band] = math.Min(masks[0][band], masks[2][band])
		for role := range masks {
			masks[role][band] += channelOffset
		}
	}
	for channel, role := range roles {
		base := channel * NumBands48000
		if role == 0 {
			clear(bandLogE[base : base+NumBands48000])
			continue
		}
		for band := 0; band < NumBands48000; band++ {
			bandLogE[base+band] -= masks[role-1][band]
		}
	}
	return bandLogE, nil
}

func surroundLogSum(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	diff := a - b
	if diff >= 8 {
		return a
	}
	return a + 0.5*math.Log2(1+math.Exp2(-2*diff))
}

func surroundChannelRoles(channels int) []int {
	switch channels {
	case 3, 5, 6:
		return []int{1, 2, 3, 1, 3, 0}[:channels]
	case 4:
		return []int{1, 3, 1, 3}
	case 7:
		return []int{1, 2, 3, 1, 3, 2, 0}
	case 8:
		return []int{1, 2, 3, 1, 3, 1, 3, 0}
	default:
		return make([]int, channels)
	}
}
