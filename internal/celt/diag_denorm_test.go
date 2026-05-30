package celt

import (
	"fmt"
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

func applyFineEnergyLogE(dec *entcode.Decoder, logE []float64, numBands, channels int, eBits []int) {
	for i := 0; i < numBands; i++ {
		fb := eBits[i]
		if fb <= 0 {
			continue
		}
		for c := 0; c < channels; c++ {
			q2 := int(dec.DecodeBits(uint(fb)))
			offset := (float64(q2)+0.5)/float64(int(1)<<fb) - 0.5
			logE[c*numBands+i] += offset
		}
	}
}

func applyFinalFineEnergyLogE(dec *entcode.Decoder, logE []float64, numBands, channels int, eBits, finePriority []int, bitsLeft int) {
	for prio := 0; prio < 2; prio++ {
		for i := 0; i < numBands && bitsLeft >= channels; i++ {
			if eBits[i] >= MaxFineEnergy || finePriority[i] != prio {
				continue
			}
			for c := 0; c < channels; c++ {
				q2 := int(dec.DecodeBits(1))
				offset := (float64(q2) - 0.5) * math.Exp2(float64(-eBits[i]-1))
				logE[c*numBands+i] += offset
				bitsLeft--
			}
		}
	}
}

func denormalizedMDCTViaBandProcessor(frameLen, numBands, channels int, X []float64, logE []float64) [][]float64 {
	mode := NewModeEx(frameLen, 48000, numBands, channels)
	M := frameLen / mode.NBase
	if M < 1 {
		M = 1
	}

	out := make([][]float64, channels)
	for c := 0; c < channels; c++ {
		bp := NewBandProcessor(mode)
		base := c * frameLen
		for i := 0; i < numBands; i++ {
			b := bp.bands[i]
			start := M * int(EBands48000[i])
			if base+start+b.Size <= len(X) {
				copy(b.Coeffs, X[base+start:base+start+b.Size])
			}
			amp := logEAmplitude(logE[c*numBands+i], i)
			b.Energy = amp * amp
		}
		bp.DenormalizeBands()
		coeffs := bp.AssembleMDCT()
		if len(coeffs) < frameLen {
			padded := make([]float64, frameLen)
			copy(padded, coeffs)
			coeffs = padded
		} else if len(coeffs) > frameLen {
			coeffs = coeffs[:frameLen]
		}
		out[c] = coeffs
	}
	return out
}

func logEAmplitude(meanSubtractedLogE float64, band int) float64 {
	return math.Exp2(meanSubtractedLogE + EMean(band))
}

func dumpDenormalizedMDCT(coeffs [][]float64, numBands, lm int) {
	M := 1 << uint(lm)
	for c := range coeffs {
		for i := 0; i < numBands; i++ {
			n := M * int(EBands48000[i+1]-EBands48000[i])
			start := M * int(EBands48000[i])
			fmt.Printf("[XD] ch=%d band=%d N=%d", c, i, n)
			for j := 0; j < n && start+j < len(coeffs[c]); j++ {
				fmt.Printf(" X[%d]=%.9g", j, coeffs[c][start+j])
			}
			fmt.Println()
		}
	}
}
