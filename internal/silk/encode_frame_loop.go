package silk

import "github.com/darui3018823/opus/internal/entcode"

// encode_frame_loop.go ports the quantiser / entropy-coding loop of
// silk_encode_frame_FLP (libopus 1.6.1): the frame is coded with the
// silk_process_gains_FLP gains first; when the packet must stay within
// maxBits (CBR, or a VBR frame that busts its cap) the gains are scaled by
// gainMult_Q8, re-quantised from LastGainIndex before this frame, and the
// frame re-quantised and re-coded until the bit count lands in
// [maxBits - bits_margin, maxBits], with Lambda raised and dithering
// disabled when the budget is still busted after two iterations, an
// interpolated multiplier once both an upper and a lower bracket exist, and
// the "damage control" zero-pulse frame as the last resort.

// frameLoopResult is what the loop leaves behind for the rest of
// encodeRangeFrame.
type frameLoopResult struct {
	gainIndices  []int
	pitchLags    []int
	ltpCoeffsQ14 [][5]int16
	ltpScaleQ14  int16
	frameSeed    int32
	pulses       []int16
	quantOffset  int
}

// frameLoopSnapshot is the state of a completed iteration that the loop may
// have to return to (the "lower" bracket in libopus: sRangeEnc_copy2 +
// ec_buf_copy, sNSQ_copy[1], LastGainIndex_copy2).
type frameLoopSnapshot struct {
	enc    *entcode.Encoder
	state  encoderFrameState
	trace  FrameTrace
	result frameLoopResult
}

// silkGainsID ports silk_gains_ID.
func silkGainsID(ind []int) int32 {
	var id int32
	for _, v := range ind {
		id = int32(v) + id<<8
	}
	return id
}

// encodeFrameLoop codes the frame's indices and excitation into enc. pg holds
// silk_process_gains_FLP's result (the unquantised gains and their first
// quantisation); maxBits is the packet-cumulative bit budget for this frame
// (0 = unlimited) and useCBR the encControl->useCBR flag for this frame.
func (e *Encoder) encodeFrameLoop(
	enc *entcode.Encoder,
	initialState encoderFrameState,
	signal []float64,
	vadActive bool,
	signalType, quantOffset int,
	conditional bool,
	nlsf nlsfAnalysis,
	cb *nlsfCBParams,
	pitchLag int,
	pitchGain float64,
	pg *silkProcessGainsResult,
	maxBits int,
	useCBR bool,
) frameLoopResult {
	const maxIter = 6
	if maxBits <= 0 {
		maxBits = 1 << 30
	}
	bitsMargin := maxBits / 4
	if useCBR {
		bitsMargin = 5
	}

	// silk_encode_frame_FLP: indices.Seed = frameCounter++ & 3 (restored to
	// this value before every iteration; the NSQ writes the winner's seed).
	frameSeed := int32(e.frameCounter & 3)
	e.frameCounter++

	// Copy part of the input state.
	encCopy := enc.Clone()
	prevSignalType := e.prevSignalType // ec_prevSignalType
	lastGainIndexPrev := e.prevGainIdx // sEncCtrl.lastGainIndexPrev
	// The frame's Lambda is captured by the first NSQ run; remember it once
	// the loop starts raising it so the LBRR copy (coded after the loop with
	// the original Lambda in libopus) is unaffected.
	lambdaRaised := false
	var lambdaOrig float64

	symbols := append([]int(nil), pg.symbols...)
	absIdx := append([]int(nil), pg.absIndices...)
	gainsID := silkGainsID(symbols)
	gainMultQ8 := int32(256)
	foundLower, foundUpper := false, false
	gainsIDLower, gainsIDUpper := int32(-1), int32(-1)
	var nBitsLower, nBitsUpper, gainMultLower, gainMultUpper int32
	var gainLock [silkMaxNBSubframes]bool
	var bestGainMult [silkMaxNBSubframes]int32
	var bestSum [silkMaxNBSubframes]int
	var lower *frameLoopSnapshot

	var res frameLoopResult
	res.frameSeed = frameSeed
	res.quantOffset = quantOffset
	var nBits int32
	var loopTrace []LoopIteration
	record := func(iter int, damage bool) {
		loopTrace = append(loopTrace, LoopIteration{
			Iter: iter, NBits: int(nBits), MaxBits: maxBits, UseCBR: useCBR, GainMultQ8: gainMultQ8, GainsID: gainsID,
			FoundLower: foundLower, FoundUpper: foundUpper, Lambda: float32(e.lambda32), QuantOffset: quantOffset,
			GainSymbols: append([]int(nil), symbols...), Damage: damage,
		})
	}

	codeIndices := func() {
		e.encodeTypeOffset(enc, vadActive, signalType, quantOffset)
		e.exactGainSymbols = symbols
		res.gainIndices = e.encodeGains(enc, signalType, absIdx, conditional)
		e.exactGainSymbols = nil
		e.encodeNLSF(enc, cb, signalType, nlsf)
		if e.nSubframes == 4 {
			enc.EncodeIcdf(nlsf.interpFactor, silkNLSFInterpFactorICDF[:], 8)
		}
		res.ltpCoeffsQ14 = make([][5]int16, e.nSubframes)
		res.ltpScaleQ14 = silkLTPScalesTable[0]
		res.pitchLags = make([]int, e.nSubframes)
		for sf := range res.pitchLags {
			res.pitchLags[sf] = pitchLag
		}
		if signalType == SignalTypeVoiced {
			res.ltpCoeffsQ14, res.ltpScaleQ14, res.pitchLags = e.encodePitchAndLTP(enc, signal, nlsf.lpcQ12, pitchGain, conditional)
		}
	}

	for iter := 0; ; iter++ {
		firstPassFits := false
		switch {
		case gainsID == gainsIDLower:
			nBits = nBitsLower
		case gainsID == gainsIDUpper:
			nBits = nBitsUpper
		default:
			// Restore part of the input state.
			if iter > 0 {
				enc.Restore(encCopy)
				e.restoreFrameState(initialState)
				e.prevSignalType = prevSignalType
			}
			// Encode parameters, run the noise shaping quantizer, encode the
			// excitation (the NSQ does not touch the range coder, so coding the
			// indices first is equivalent to libopus' order).
			codeIndices()
			e.nsqSeed = frameSeed
			e.traceNLSFQ15 = nlsf.nlsfQ15
			res.pulses = e.closedLoopNSQWithRateScale(signal, nlsf.lpcQ12, nlsf.lpcQ12Interp, absIdx,
				signalType, quantOffset, frameSeed, res.pitchLags, res.ltpCoeffsQ14, res.ltpScaleQ14, 1)
			enc.EncodeIcdf(int(e.nsqSeed), silkUniform4ICDF[:], 8)
			e.encodePulses(enc, res.pulses, signalType, quantOffset)
			nBits = int32(enc.ECTell())
			record(iter, false)

			// If we still bust after the last iteration, do some damage control.
			if iter == maxIter && !foundLower && int(nBits) > maxBits {
				enc.Restore(encCopy)
				e.prevSignalType = prevSignalType
				e.prevLagIndex = initialState.prevLagIndex
				// Keep gains the same as the last frame.
				for i := range symbols {
					symbols[i] = 4 // delta 0
					absIdx[i] = lastGainIndexPrev
				}
				if !conditional {
					symbols[0] = lastGainIndexPrev
				}
				for i := range res.pulses {
					res.pulses[i] = 0
				}
				// The LTP indices are re-derived, so they must start from the
				// same LTP gain accumulator as the first coding of the frame.
				e.ltpSumLogGainQ7 = initialState.ltpSumLogGainQ7
				codeIndices()
				// indices.Seed still holds the last NSQ run's winning seed.
				enc.EncodeIcdf(int(e.nsqSeed), silkUniform4ICDF[:], 8)
				e.encodePulses(enc, res.pulses, signalType, quantOffset)
				e.pendingTrace.Pulses = append([]int16(nil), res.pulses...)
				e.pendingTrace.GainsIndices = append([]int(nil), absIdx...)
				nBits = int32(enc.ECTell())
				record(iter, true)
			}

			firstPassFits = !useCBR && iter == 0 && int(nBits) <= maxBits
		}
		if firstPassFits {
			break
		}

		if iter == maxIter {
			if foundLower && (gainsID == gainsIDLower || int(nBits) > maxBits) {
				// Restore output state from earlier iteration that did meet the
				// bitrate budget.
				enc.Restore(lower.enc)
				e.restoreFrameState(lower.state)
				e.pendingTrace = lower.trace
				e.pendingTrace.LoopRestore = true
				res = lower.result
			}
			break
		}

		if int(nBits) > maxBits {
			if !foundLower && iter >= 2 {
				// Adjust the quantizer's rate/distortion tradeoff and discard
				// previous "upper" results.
				if !lambdaRaised {
					lambdaRaised = true
					lambdaOrig = e.lambda32
				}
				lambda := f32(e.lambda32 * 1.5)
				if lambda < 1.5 {
					lambda = 1.5
				}
				e.lambda32 = lambda
				// Reducing dithering can help us hit the target.
				quantOffset = 0
				res.quantOffset = 0
				foundUpper = false
				gainsIDUpper = -1
			} else {
				foundUpper = true
				nBitsUpper = nBits
				gainMultUpper = gainMultQ8
				gainsIDUpper = gainsID
			}
		} else if int(nBits) < maxBits-bitsMargin {
			foundLower = true
			nBitsLower = nBits
			gainMultLower = gainMultQ8
			if gainsID != gainsIDLower {
				gainsIDLower = gainsID
				// Copy part of the output state.
				lower = &frameLoopSnapshot{
					enc:    enc.Clone(),
					state:  e.snapshotFrameState(),
					trace:  e.pendingTrace,
					result: res,
				}
				lower.result.gainIndices = append([]int(nil), res.gainIndices...)
				lower.result.pulses = append([]int16(nil), res.pulses...)
			}
		} else {
			// Close enough.
			break
		}

		if !foundLower && int(nBits) > maxBits {
			subfrLength := e.frameSize / e.nSubframes
			for i := 0; i < e.nSubframes; i++ {
				sum := 0
				for j := i * subfrLength; j < (i+1)*subfrLength; j++ {
					v := int(res.pulses[j])
					if v < 0 {
						v = -v
					}
					sum += v
				}
				if iter == 0 || (sum < bestSum[i] && !gainLock[i]) {
					bestSum[i] = sum
					bestGainMult[i] = gainMultQ8
				} else {
					gainLock[i] = true
				}
			}
		}
		if !(foundLower && foundUpper) {
			// Adjust gain according to high-rate rate/distortion curve.
			if int(nBits) > maxBits {
				gainMultQ8 = gainMultQ8 * 3 / 2
				if gainMultQ8 > 1024 {
					gainMultQ8 = 1024
				}
			} else {
				gainMultQ8 = gainMultQ8 * 4 / 5
				if gainMultQ8 < 64 {
					gainMultQ8 = 64
				}
			}
		} else {
			// Adjust gain by interpolating.
			gainMultQ8 = gainMultLower + ((gainMultUpper-gainMultLower)*(int32(maxBits)-nBitsLower))/(nBitsUpper-nBitsLower)
			// New gain multiplier must be between 25% and 75% of old range
			// (note that gainMult_upper < gainMult_lower).
			if hi := gainMultLower + (gainMultUpper-gainMultLower)>>2; gainMultQ8 > hi {
				gainMultQ8 = hi
			} else if lo := gainMultUpper - (gainMultUpper-gainMultLower)>>2; gainMultQ8 < lo {
				gainMultQ8 = lo
			}
		}

		gainsQ16 := make([]int32, e.nSubframes)
		for i := 0; i < e.nSubframes; i++ {
			tmp := gainMultQ8
			if gainLock[i] {
				tmp = bestGainMult[i]
			}
			gainsQ16[i] = silkLShiftSat32(silkSMULWB(pg.gainsUnqQ16[i], int16(tmp)), 8)
		}
		// Quantize gains from LastGainIndex before this frame.
		lastGainIndex := lastGainIndexPrev
		symbols, absIdx = silkGainsQuant(gainsQ16, &lastGainIndex, conditional, e.nSubframes)
		gainsID = silkGainsID(symbols)
	}

	if lambdaRaised {
		e.lambda32 = lambdaOrig
	}
	e.pendingTrace.Loop = loopTrace
	return res
}
