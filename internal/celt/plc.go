package celt

import "math"

// Float32-faithful port of libopus 1.6.1's CELT packet loss concealment
// (celt/celt_decoder.c celt_decode_lost, celt_plc_pitch_search and
// prefilter_and_fold, without the neural PLC / DRED). The decoder history
// libopus keeps in decode_mem is the post-filter history (PostFilter.buf,
// decodeBufferSize samples per channel) plus the IMDCT carry (overlap) and,
// after a pitch-based concealment, the extrapolated overlap (plcTail).

const (
	decodeBufferSize = 2048 // DECODE_BUFFER_SIZE (DEC_PITCH_BUF_SIZE)
	plcPitchLagMax   = 720
	plcPitchLagMin   = 100
	celtLPCOrder     = 24
	plcMaxPeriod     = 1024 // MAX_PERIOD

	frameNone        = 0
	frameNormal      = 1
	framePLCNoise    = 2
	framePLCPeriodic = 3
)

// plcPitchSearch is celt_plc_pitch_search.
func (d *Decoder) plcPitchSearch() int {
	C := d.mode.Channels
	mem := make([][]float32, C)
	for c := 0; c < C; c++ {
		mem[c] = make([]float32, decodeBufferSize)
		buf := d.postFilter[c].buf
		for i := range mem[c] {
			mem[c][i] = float32(buf[len(buf)-decodeBufferSize+i])
		}
	}
	lp := make([]float32, decodeBufferSize>>1)
	pitchDownsample32(mem, lp, decodeBufferSize>>1, C)
	pitch := pitchSearch32(lp[plcPitchLagMax>>1:], lp, decodeBufferSize-plcPitchLagMax, plcPitchLagMax-plcPitchLagMin)
	return plcPitchLagMax - pitch
}

// celtAutocorrWindowed32 is _celt_autocorr with the analysis window applied
// to the first and last overlap samples.
func celtAutocorrWindowed32(x []float32, ac []float32, window []float64, overlap, lag, n int) {
	xx := make([]float32, n)
	copy(xx, x[:n])
	for i := 0; i < overlap; i++ {
		w := float32(window[i])
		xx[i] = float32(x[i] * w)
		xx[n-i-1] = float32(x[n-i-1] * w)
	}
	celtAutocorr32(xx, ac, lag, n)
}

// celtFIR32 is celt_fir_c: y[i] = x[i] + Σ num[k]*x[i-k-1], accumulated
// from the oldest input. x holds ord samples of history before xOff.
func celtFIR32(x []float32, xOff int, num []float32, y []float32, n, ord int) {
	for i := 0; i < n; i++ {
		sum := x[xOff+i]
		for j := 0; j < ord; j++ {
			sum += float32(num[ord-1-j] * x[xOff+i+j-ord])
		}
		y[i] = sum
	}
}

// celtIIR32 is celt_iir (the unrolled float build; n must be a multiple of
// 4): y = x filtered by 1/(1 + Σ den[k] z^-(k+1)), mem holding the last
// outputs (newest first). It may run in place.
func celtIIR32(x []float32, den []float32, y []float32, n, ord int, mem []float32) {
	rden := make([]float32, ord)
	for i := 0; i < ord; i++ {
		rden[i] = den[ord-i-1]
	}
	yy := make([]float32, n+ord)
	for i := 0; i < ord; i++ {
		yy[i] = -mem[ord-i-1]
	}
	for i := 0; i+3 < n; i += 4 {
		var sum [4]float32
		sum[0], sum[1], sum[2], sum[3] = x[i], x[i+1], x[i+2], x[i+3]
		// xcorr_kernel: each sum accumulates rden[j]*yy[i+k+j] in j order.
		for k := 0; k < 4; k++ {
			for j := 0; j < ord; j++ {
				sum[k] += float32(rden[j] * yy[i+k+j])
			}
		}
		yy[i+ord] = -sum[0]
		y[i] = sum[0]
		sum[1] += float32(yy[i+ord] * den[0])
		yy[i+ord+1] = -sum[1]
		y[i+1] = sum[1]
		sum[2] += float32(yy[i+ord+1] * den[0])
		sum[2] += float32(yy[i+ord] * den[1])
		yy[i+ord+2] = -sum[2]
		y[i+2] = sum[2]
		sum[3] += float32(yy[i+ord+2] * den[0])
		sum[3] += float32(yy[i+ord+1] * den[1])
		sum[3] += float32(yy[i+ord] * den[2])
		yy[i+ord+3] = -sum[3]
		y[i+3] = sum[3]
	}
	for i := 0; i < ord; i++ {
		mem[i] = y[n-i-1]
	}
}

// prefilterAndFoldCarry is prefilter_and_fold for channel c: the inverse
// post-filter over the extrapolated overlap, then a simulated TDAC so the
// concealed audio blends with the next frame's MDCT. It rewrites the first
// overlap/2 samples of the IMDCT carry.
func (d *Decoder) prefilterAndFoldCarry(c int) {
	pf := d.postFilter[c]
	ov := d.mode.Overlap
	x := d.plcTail[c]
	hist := pf.buf
	get := func(k int) float32 {
		if k < 0 {
			return float32(hist[len(hist)+k])
		}
		return float32(x[k])
	}
	etmp := make([]float32, ov)
	g0 := -float32(pf.prevGain)
	g1 := -float32(pf.gain)
	if (g0 == 0 && g1 == 0) || g1 == 0 {
		for i := range etmp {
			etmp[i] = float32(x[i])
		}
	} else {
		t := max(pf.period, combFilterMinPeriod)
		tapset := pf.tapset
		if tapset < 0 || tapset >= len(pfCombGains) {
			tapset = 0
		}
		g10 := float32(g1 * float32(pfCombGains[tapset][0]))
		g11 := float32(g1 * float32(pfCombGains[tapset][1]))
		g12 := float32(g1 * float32(pfCombGains[tapset][2]))
		x4, x3, x2, x1 := get(-t-2), get(-t-1), get(-t), get(-t+1)
		for i := 0; i < ov; i++ {
			x0 := get(i - t + 2)
			y := get(i)
			y += float32(g10 * x2)
			y += float32(g11 * (x1 + x3))
			y += float32(g12 * (x0 + x4))
			etmp[i] = y
			x4, x3, x2, x1 = x3, x2, x1, x0
		}
	}
	w := d.celtMode.Window
	for i := 0; i < ov/2; i++ {
		d.overlap[c][i] = float64(float32(float32(w[i])*etmp[ov-1-i]) + float32(float32(w[ov-i-1])*etmp[i]))
	}
}

// decodeLoss is celt_decode_lost followed by the de-emphasis: the
// concealed frame, from the noise-based PLC (after a long loss, for hybrid
// frames and until two packets arrived) or the pitch-based extrapolation.
func (d *Decoder) decodeLoss() []float64 {
	N := d.mode.FrameSize
	C := d.mode.Channels
	lm := d.mode.LM
	ov := d.mode.Overlap
	nb := d.mode.Bands.NumBands
	start := d.lastStartBand
	end := d.lastEndBand
	if end <= 0 || end > nb {
		end = nb
	}
	frameType := framePLCPeriodic
	if d.plcDuration >= 40 || start != 0 || d.skipPLC {
		frameType = framePLCNoise
	}
	samples := make([][]float64, C)
	if frameType == framePLCNoise {
		effEnd := max(start, min(end, NumBands48000))
		if d.prefilterAndFold {
			for c := 0; c < C; c++ {
				d.prefilterAndFoldCarry(c)
			}
		}
		// Energy decay.
		decay := float32(0.5)
		if d.lossDuration == 0 {
			decay = 1.5
		}
		for c := 0; c < C; c++ {
			for i := start; i < end; i++ {
				idx := c*nb + i
				d.prevEnergies[idx] = float64(max(float32(d.backgroundLogE[idx]), float32(d.prevEnergies[idx])-decay))
			}
		}
		seed := d.lastFinalRange
		M := 1 << lm
		for c := 0; c < C; c++ {
			X := make([]float64, N)
			for i := start; i < effEnd; i++ {
				boffs := int(EBands48000[i]) * M
				blen := int(EBands48000[i+1]-EBands48000[i]) * M
				for j := 0; j < blen; j++ {
					seed = celtLCGRand(seed)
					X[boffs+j] = float64(float32(int32(seed) >> 20))
				}
				renormaliseVector(X[boffs:boffs+blen], blen, 1)
			}
			// celt_synthesis: denormalise_bands and the IMDCT.
			freq := make([]float64, N)
			for i := start; i < effEnd; i++ {
				lg := float32(d.prevEnergies[c*nb+i]) + float32(EMean(i))
				g := celtExp2RoundedFloat32(min(float32(32), lg))
				for j := int(EBands48000[i]) * M; j < int(EBands48000[i+1])*M && j < N/d.downsample; j++ {
					freq[j] = float64(float32(X[j]) * g)
				}
			}
			out := d.celtMode.CLTMDCTBackward(freq, d.overlap[c])
			// Run the postfilter with the last parameters (libopus keeps the
			// periods clamped to COMBFILTER_MINPERIOD).
			pf := d.postFilter[c]
			pf.period = max(pf.period, combFilterMinPeriod)
			pf.prevPeriod = max(pf.prevPeriod, combFilterMinPeriod)
			samples[c] = pf.Apply(out, pf.period, pf.gain, pf.tapset, d.mode.NBase, lm, d.celtMode.Window)
		}
		d.lastFinalRange = seed
		d.prefilterAndFold = false
		// Skip regular PLC until we get two consecutive packets.
		d.skipPLC = true
	} else {
		fade := float32(1)
		var pitchIndex int
		if d.lastFrameType != framePLCPeriodic {
			pitchIndex = d.plcPitchSearch()
			d.lastPitchIndex = pitchIndex
		} else {
			pitchIndex = d.lastPitchIndex
			fade = 0.8
		}
		excLength := min(2*pitchIndex, plcMaxPeriod)
		window := d.celtMode.Window
		extrapolationLen := N + ov
		for c := 0; c < C; c++ {
			pf := d.postFilter[c]
			old := pf.buf[len(pf.buf)-decodeBufferSize:]
			// exc[-ORDER .. MAX_PERIOD): the last MAX_PERIOD+ORDER samples.
			exc := make([]float32, plcMaxPeriod+celtLPCOrder)
			for i := range exc {
				exc[i] = float32(old[decodeBufferSize-plcMaxPeriod-celtLPCOrder+i])
			}
			lpc := d.plcLPC[c][:]
			if d.lastFrameType != framePLCPeriodic {
				var ac [celtLPCOrder + 1]float32
				celtAutocorrWindowed32(exc[celtLPCOrder:], ac[:], window, ov, celtLPCOrder, plcMaxPeriod)
				// Add a noise floor of -40 dB.
				ac[0] *= 1.0001
				// Lag windowing.
				lagW := float32(float32(0.008) * float32(0.008))
				for i := 1; i <= celtLPCOrder; i++ {
					ac[i] -= float32(float32(float32(ac[i]*lagW)*float32(i)) * float32(i))
				}
				celtLPC32(lpc, ac[:], celtLPCOrder)
			}
			// The excitation for excLength samples before the loss.
			firTmp := make([]float32, excLength)
			celtFIR32(exc, celtLPCOrder+plcMaxPeriod-excLength, lpc, firTmp, excLength, celtLPCOrder)
			copy(exc[celtLPCOrder+plcMaxPeriod-excLength:], firTmp)
			// Check if the waveform is decaying, and if so how fast.
			e1, e2 := float32(1), float32(1)
			decayLength := excLength >> 1
			for i := 0; i < decayLength; i++ {
				e := exc[celtLPCOrder+plcMaxPeriod-decayLength+i]
				e1 += float32(e * e)
				e = exc[celtLPCOrder+plcMaxPeriod-2*decayLength+i]
				e2 += float32(e * e)
			}
			e1 = min(e1, e2)
			decay := float32(math.Sqrt(float64(e1 / e2)))
			// Extrapolate from the end of the excitation with a period of
			// pitchIndex, scaling down each period by decay.
			extrapolationOffset := plcMaxPeriod - pitchIndex
			ext := make([]float32, extrapolationLen)
			attenuation := float32(fade * decay)
			var s1 float32
			for i, j := 0, 0; i < extrapolationLen; i, j = i+1, j+1 {
				if j >= pitchIndex {
					j -= pitchIndex
					attenuation = float32(attenuation * decay)
				}
				ext[i] = float32(attenuation * exc[celtLPCOrder+extrapolationOffset+j])
				// The energy of the previously decoded signal whose
				// excitation we're copying.
				tmp := float32(old[decodeBufferSize-plcMaxPeriod+extrapolationOffset+j])
				s1 += float32(tmp * tmp)
			}
			// Synthesis filter from the last decoded samples.
			var lpcMem [celtLPCOrder]float32
			for i := 0; i < celtLPCOrder; i++ {
				lpcMem[i] = float32(old[decodeBufferSize-1-i])
			}
			celtIIR32(ext, lpc, ext, extrapolationLen, celtLPCOrder, lpcMem[:])
			// Attenuate if the synthesis energy is higher than expected.
			var s2 float32
			for _, v := range ext {
				s2 += float32(v * v)
			}
			if !(s1 > float32(0.2)*s2) {
				clear(ext)
			} else if s1 < s2 {
				ratio := float32(math.Sqrt(float64((s1 + 1) / (s2 + 1))))
				for i := 0; i < ov; i++ {
					tmpG := float32(1) - float32(float32(window[i])*(1-ratio))
					ext[i] = float32(tmpG * ext[i])
				}
				for i := ov; i < extrapolationLen; i++ {
					ext[i] = float32(ratio * ext[i])
				}
			}
			frame := make([]float64, N)
			for i := 0; i < N; i++ {
				frame[i] = float64(ext[i])
			}
			// The frame joins the history; the extrapolated overlap is the
			// carry the next frame blends with (after prefilter_and_fold).
			pf.updateHistory(frame)
			for i := 0; i < ov; i++ {
				d.plcTail[c][i] = float64(ext[N+i])
			}
			for i := range d.overlap[c] {
				d.overlap[c][i] = 0
				if i < ov/2 {
					d.overlap[c][i] = float64(ext[N+i])
				}
			}
			samples[c] = frame
		}
		d.prefilterAndFold = true
	}
	d.lossDuration = min(10000, d.lossDuration+(1<<lm))
	d.plcDuration = min(10000, d.plcDuration+(1<<lm))
	d.lastFrameType = frameType

	nd := N / d.downsample
	output := make([]float64, nd*C)
	for c := 0; c < C; c++ {
		d.applyDeemphasis(c, samples[c])
		for i := 0; i < nd; i++ {
			output[i*C+c] = samples[c][i*d.downsample] * celtFloatScale
		}
	}
	return output
}
