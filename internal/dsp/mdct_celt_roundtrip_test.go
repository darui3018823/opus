package dsp

import (
	"math"
	"testing"
)

// celtWin builds the CELT overlap window of length n (libopus formula).
func celtWin(n int) []float32 {
	w := make([]float32, n)
	for i := 0; i < n; i++ {
		x := math.Pi * (float64(i) + 0.5) / (2.0 * float64(n))
		s := math.Sin(x)
		w[i] = float32(math.Sin(math.Pi / 2.0 * s * s))
	}
	return w
}

// TestCLTMDCTForwardBackwardRoundtrip characterises the forward/backward MDCT
// pair: it streams overlapping frames through CLTMDCTForward then CLTMDCTBackward
// and reports the best reconstruction delay, scale ratio, and residual so the
// encoder's transform layer can be validated/tuned.
func TestCLTMDCTForwardBackwardRoundtrip(t *testing.T) {
	const N = 960
	const ov = 120
	mode := NewCELTMode(N, ov, celtWin(ov))

	nFrames := 12
	total := nFrames * N
	in := make([]float64, total)
	for n := 0; n < total; n++ {
		in[n] = 0.6*math.Sin(2*math.Pi*440*float64(n)/48000.0) +
			0.3*math.Sin(2*math.Pi*3000*float64(n)/48000.0)
	}

	mem := make([]float64, ov)
	carry := make([]float64, ov)
	out := make([]float64, 0, total)
	for f := 0; f < nFrames; f++ {
		frame := in[f*N : (f+1)*N]
		buf := make([]float64, N+ov)
		copy(buf[:ov], mem)
		copy(buf[ov:], frame)
		coeffs := mode.CLTMDCTForward(buf)
		dec := mode.CLTMDCTBackward(coeffs, carry)
		out = append(out, dec...)
		copy(mem, buf[N:N+ov])
	}

	// Find the delay that best matches input to output over a steady middle region.
	lo, hi := 3*N, 9*N
	bestDelay, bestErr := 0, math.Inf(1)
	var bestScale float64
	for d := 0; d <= 2*N; d++ {
		var dot, e2in, e2out float64
		for i := lo; i < hi; i++ {
			if i-d < 0 || i-d >= len(out) {
				continue
			}
			a := in[i]
			b := out[i-d]
			dot += a * b
			e2in += a * a
			e2out += b * b
		}
		if e2out == 0 {
			continue
		}
		scale := dot / e2out
		var err float64
		for i := lo; i < hi; i++ {
			if i-d < 0 || i-d >= len(out) {
				continue
			}
			r := in[i] - scale*out[i-d]
			err += r * r
		}
		err = math.Sqrt(err / float64(hi-lo))
		if err < bestErr {
			bestErr, bestDelay, bestScale = err, d, scale
		}
	}
	inRMS := 0.0
	for i := lo; i < hi; i++ {
		inRMS += in[i] * in[i]
	}
	inRMS = math.Sqrt(inRMS / float64(hi-lo))
	t.Logf("bestDelay=%d bestScale=%.6f residualRMS=%.6g inRMS=%.6g SNR=%.2fdB",
		bestDelay, bestScale, bestErr, inRMS, 20*math.Log10(inRMS/bestErr))
}

// TestCLTMDCTShortBlockRoundtrip exercises the transient (short-block) MDCT path:
// the encoder forward transform produces M interleaved NBase-point blocks, and the
// decoder synthesises them with M chained NBase-point inverse transforms sharing a
// rolling overlap tail. This mirrors internal/celt encoder pass 2 and decoder.go
// transient synthesis. A high SNR over a steady region confirms the short
// forward/backward pair reconstructs through overlap-add.
func TestCLTMDCTShortBlockRoundtrip(t *testing.T) {
	const N = 960
	const ov = 120
	const nBase = 120
	const M = N / nBase // 8 short blocks per frame
	short := NewCELTMode(nBase, ov, celtWin(ov))

	nFrames := 12
	total := nFrames * N
	in := make([]float64, total)
	for n := 0; n < total; n++ {
		in[n] = 0.6*math.Sin(2*math.Pi*440*float64(n)/48000.0) +
			0.3*math.Sin(2*math.Pi*3000*float64(n)/48000.0)
	}

	encOverlap := make([]float64, ov) // encoder analysis overlap (prev frame tail)
	decTail := make([]float64, nBase) // decoder rolling short-block overlap tail
	out := make([]float64, 0, total)
	for f := 0; f < nFrames; f++ {
		frame := in[f*N : (f+1)*N]
		buf := make([]float64, N+ov)
		copy(buf[:ov], encOverlap)
		copy(buf[ov:], frame)

		// Forward: M short MDCTs over overlapping NBase windows, interleaved.
		coeffs := make([]float64, N)
		for b := 0; b < M; b++ {
			sub := buf[b*nBase : b*nBase+nBase+ov]
			sc := short.CLTMDCTForward(sub)
			for i := 0; i < nBase; i++ {
				coeffs[b+i*M] = sc[i]
			}
		}
		copy(encOverlap, buf[N:N+ov])

		// Backward: M chained short IMDCTs sharing the rolling overlap tail.
		samplesOut := make([]float64, N)
		for k := 0; k < M; k++ {
			subCoeffs := make([]float64, nBase)
			for i := 0; i < nBase; i++ {
				subCoeffs[i] = coeffs[k+i*M]
			}
			subOut := short.CLTMDCTBackward(subCoeffs, decTail)
			copy(samplesOut[k*nBase:], subOut)
		}
		out = append(out, samplesOut...)
	}

	lo, hi := 3*N, 9*N
	bestDelay, bestErr := 0, math.Inf(1)
	var bestScale float64
	for d := 0; d <= 2*N; d++ {
		var dot, e2out float64
		for i := lo; i < hi; i++ {
			if i-d < 0 || i-d >= len(out) {
				continue
			}
			dot += in[i] * out[i-d]
			e2out += out[i-d] * out[i-d]
		}
		if e2out == 0 {
			continue
		}
		scale := dot / e2out
		var err float64
		for i := lo; i < hi; i++ {
			if i-d < 0 || i-d >= len(out) {
				continue
			}
			r := in[i] - scale*out[i-d]
			err += r * r
		}
		err = math.Sqrt(err / float64(hi-lo))
		if err < bestErr {
			bestErr, bestDelay, bestScale = err, d, scale
		}
	}
	inRMS := 0.0
	for i := lo; i < hi; i++ {
		inRMS += in[i] * in[i]
	}
	inRMS = math.Sqrt(inRMS / float64(hi-lo))
	snr := 20 * math.Log10(inRMS/bestErr)
	t.Logf("short-block: bestDelay=%d bestScale=%.6f residualRMS=%.6g inRMS=%.6g SNR=%.2fdB",
		bestDelay, bestScale, bestErr, inRMS, snr)
	if snr < 60 {
		t.Errorf("short-block MDCT roundtrip SNR %.2fdB too low (forward/backward pair not inverse)", snr)
	}
}
