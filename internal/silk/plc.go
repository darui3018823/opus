package silk

// Packet loss concealment and comfort noise generation ported from libopus
// silk/PLC.c and silk/CNG.c. All arithmetic mirrors the fixed-point C code so
// that concealed frames, the state carried into the next good frame, and the
// glue fade-in match libopus exactly.

const (
	silkMaxFrameLength = 320 // MAX_FRAME_LENGTH: 5 * MAX_NB_SUBFR * 16 kHz

	plcBWECoefQ16              = int32(64881) // SILK_FIX_CONST(BWE_COEF=0.99, 16)
	plcVPitchGainStartMinQ14   = int32(11469) // 0.7 in Q14
	plcVPitchGainStartMaxQ14   = int32(15565) // 0.95 in Q14
	plcMaxPitchLagMs           = 18
	plcRandBufSize             = 128
	plcRandBufMask             = plcRandBufSize - 1
	plcLog2InvLPCGainHighThres = 3
	plcLog2InvLPCGainLowThres  = 8
	plcPitchDriftFacQ16        = int32(655) // 0.01 in Q16
	plcNBAtt                   = 2

	bweAfterLossQ16 = int32(63570) // BWE_AFTER_LOSS_Q16

	cngBufMaskMax           = 255   // CNG_BUF_MASK_MAX
	cngGainSmthQ16          = 4634  // CNG_GAIN_SMTH_Q16: 0.25^(1/4)
	cngGainSmthThresholdQ16 = 46396 // CNG_GAIN_SMTH_THRESHOLD_Q16: -3 dB
	cngNLSFSmthQ16          = 16348 // CNG_NLSF_SMTH_Q16: 0.25
)

var (
	plcHarmAttQ15           = [plcNBAtt]int16{32440, 31130} // 0.99, 0.95
	plcRandAttenuateVQ15    = [plcNBAtt]int16{31130, 26214} // 0.95, 0.8
	plcRandAttenuateUVQ15   = [plcNBAtt]int16{32440, 29491} // 0.99, 0.9
	plcPostLossCenterTapQ14 = int16(4096)                   // SILK_FIX_CONST(0.25, 14)
)

// plcState mirrors silk_PLC_struct.
type plcState struct {
	pitchLQ8        int32
	ltpCoefQ14      [5]int16
	prevLPCQ12      [silkMaxLPCOrder]int16
	lastFrameLost   bool
	randSeed        int32
	randScaleQ14    int16
	concEnergy      int32
	concEnergyShift int
	prevLTPScaleQ14 int16
	prevGainQ16     [2]int32
	fsKHz           int
	nbSubfr         int
	subfrLength     int
}

// cngState mirrors silk_CNG_struct.
type cngState struct {
	excBufQ14   [silkMaxFrameLength]int32
	smthNLSFQ15 [silkMaxLPCOrder]int16
	synthState  [silkMaxLPCOrder]int32
	smthGainQ16 int32
	randSeed    int32
	fsKHz       int
}

// decoderControl carries the per-frame values libopus keeps in
// silk_decoder_control that the PLC and CNG updates consume.
type decoderControl struct {
	signalType  int
	pitchL      []int
	ltpCoefQ14  [][5]int16
	predCoefQ12 []int16 // PredCoef_Q12[1]
	ltpScaleQ14 int16
	gainsQ16    []int32
}

// silk_PLC_Reset
func (d *Decoder) plcReset() {
	d.plc.pitchLQ8 = int32(d.frameSize) << (8 - 1)
	d.plc.prevGainQ16[0] = 1 << 16
	d.plc.prevGainQ16[1] = 1 << 16
	d.plc.subfrLength = 20
	d.plc.nbSubfr = 2
}

// silk_CNG_Reset
func (d *Decoder) cngReset() {
	nlsfStepQ15 := int32(32767) / int32(d.lpcOrder+1)
	nlsfAccQ15 := int32(0)
	for i := 0; i < d.lpcOrder; i++ {
		nlsfAccQ15 += nlsfStepQ15
		d.cng.smthNLSFQ15[i] = int16(nlsfAccQ15)
	}
	d.cng.smthGainQ16 = 0
	d.cng.randSeed = 3176576
}

// plcCheckReset mirrors the lazy sample-rate reset at the top of silk_PLC.
func (d *Decoder) plcCheckReset() {
	if d.fsKHz != d.plc.fsKHz {
		d.plcReset()
		d.plc.fsKHz = d.fsKHz
	}
}

// silk_PLC_update: refresh the concealment parameters after a good frame.
func (d *Decoder) plcUpdate(ctrl *decoderControl) {
	d.plcCheckReset()
	psPLC := &d.plc

	d.prevSignalType = ctrl.signalType
	ltpGainQ14 := int32(0)
	if ctrl.signalType == SignalTypeVoiced {
		// Find the parameters for the last subframe which contains a pitch pulse.
		for j := 0; j*d.subfrmLen < ctrl.pitchL[d.nSubframes-1]; j++ {
			if j == d.nSubframes {
				break
			}
			tempLTPGainQ14 := int32(0)
			for i := 0; i < 5; i++ {
				tempLTPGainQ14 += int32(ctrl.ltpCoefQ14[d.nSubframes-1-j][i])
			}
			if tempLTPGainQ14 > ltpGainQ14 {
				ltpGainQ14 = tempLTPGainQ14
				psPLC.ltpCoefQ14 = ctrl.ltpCoefQ14[d.nSubframes-1-j]
				psPLC.pitchLQ8 = int32(ctrl.pitchL[d.nSubframes-1-j]) << 8
			}
		}

		psPLC.ltpCoefQ14 = [5]int16{}
		psPLC.ltpCoefQ14[5/2] = int16(ltpGainQ14)

		// Limit LT coefs.
		if ltpGainQ14 < plcVPitchGainStartMinQ14 {
			tmp := plcVPitchGainStartMinQ14 << 10
			scaleQ10 := tmp / silkMax32(ltpGainQ14, 1)
			for i := 0; i < 5; i++ {
				psPLC.ltpCoefQ14[i] = int16(silkSMULBB(int32(psPLC.ltpCoefQ14[i]), scaleQ10) >> 10)
			}
		} else if ltpGainQ14 > plcVPitchGainStartMaxQ14 {
			tmp := plcVPitchGainStartMaxQ14 << 14
			scaleQ14 := tmp / silkMax32(ltpGainQ14, 1)
			for i := 0; i < 5; i++ {
				psPLC.ltpCoefQ14[i] = int16(silkSMULBB(int32(psPLC.ltpCoefQ14[i]), scaleQ14) >> 14)
			}
		}
	} else {
		psPLC.pitchLQ8 = silkSMULBB(int32(d.fsKHz), 18) << 8
		psPLC.ltpCoefQ14 = [5]int16{}
	}

	// Save LPC coefficients.
	copy(psPLC.prevLPCQ12[:d.lpcOrder], ctrl.predCoefQ12[:d.lpcOrder])
	psPLC.prevLTPScaleQ14 = ctrl.ltpScaleQ14

	// Save last two gains.
	psPLC.prevGainQ16[0] = ctrl.gainsQ16[d.nSubframes-2]
	psPLC.prevGainQ16[1] = ctrl.gainsQ16[d.nSubframes-1]

	psPLC.subfrLength = d.subfrmLen
	psPLC.nbSubfr = d.nSubframes
}

// silk_PLC_energy
func (d *Decoder) plcEnergy(prevGainQ10 [2]int32) (energy1 int32, shift1 int, energy2 int32, shift2 int) {
	excBuf := make([]int16, 2*d.subfrmLen)
	for k := 0; k < 2; k++ {
		for i := 0; i < d.subfrmLen; i++ {
			excBuf[k*d.subfrmLen+i] = silkSAT16(silkSMULWW(
				d.excQ14[i+(k+d.nSubframes-2)*d.subfrmLen], prevGainQ10[k]) >> 8)
		}
	}
	energy1, shift1 = silkSumSqrShift(excBuf[:d.subfrmLen])
	energy2, shift2 = silkSumSqrShift(excBuf[d.subfrmLen:])
	return
}

// silk_PLC_conceal: synthesize one concealed frame and return it as int16
// samples. pitchL receives the final drifted lag for every subframe.
func (d *Decoder) plcConceal() (frame []int16, lag int) {
	psPLC := &d.plc
	ltpMemLength := len(d.ltpState)
	sLTPQ14 := make([]int32, ltpMemLength+d.frameSize)
	sLTP := make([]int16, ltpMemLength)

	prevGainQ10 := [2]int32{psPLC.prevGainQ16[0] >> 6, psPLC.prevGainQ16[1] >> 6}

	if d.firstFrame {
		psPLC.prevLPCQ12 = [silkMaxLPCOrder]int16{}
	}

	energy1, shift1, energy2, shift2 := d.plcEnergy(prevGainQ10)

	var randPtr []int32
	if energy1>>uint(shift2) < energy2>>uint(shift1) {
		// First sub-frame has lowest energy.
		randPtr = d.excQ14[silkMaxInt(0, (psPLC.nbSubfr-1)*psPLC.subfrLength-plcRandBufSize):]
	} else {
		// Second sub-frame has lowest energy.
		randPtr = d.excQ14[silkMaxInt(0, psPLC.nbSubfr*psPLC.subfrLength-plcRandBufSize):]
	}

	// Set up gain to random noise component.
	bQ14 := &psPLC.ltpCoefQ14
	randScaleQ14 := psPLC.randScaleQ14

	// Set up attenuation gains.
	att := silkMinInt(plcNBAtt-1, d.lossCnt)
	harmGainQ15 := int32(plcHarmAttQ15[att])
	var randGainQ15 int32
	if d.prevSignalType == SignalTypeVoiced {
		randGainQ15 = int32(plcRandAttenuateVQ15[att])
	} else {
		randGainQ15 = int32(plcRandAttenuateUVQ15[att])
	}

	// LPC concealment. Apply BWE to previous LPC.
	silkBwexpander16(psPLC.prevLPCQ12[:d.lpcOrder], d.lpcOrder, plcBWECoefQ16)
	var aQ12 [silkMaxLPCOrder]int16
	copy(aQ12[:], psPLC.prevLPCQ12[:d.lpcOrder])

	// First lost frame.
	if d.lossCnt == 0 {
		randScaleQ14 = 1 << 14

		if d.prevSignalType == SignalTypeVoiced {
			// Reduce random noise gain for voiced frames.
			for i := 0; i < 5; i++ {
				randScaleQ14 -= bQ14[i]
			}
			randScaleQ14 = silkMax16(3277, randScaleQ14) // 0.2
			randScaleQ14 = int16(silkSMULBB(int32(randScaleQ14), int32(psPLC.prevLTPScaleQ14)) >> 14)
		} else {
			// Reduce random noise for unvoiced frames with high LPC gain.
			invGainQ30 := silkLPCInversePredGainQ12(psPLC.prevLPCQ12[:d.lpcOrder], d.lpcOrder)

			downScaleQ30 := silkMin32(int32(1)<<30>>plcLog2InvLPCGainHighThres, invGainQ30)
			downScaleQ30 = silkMax32(int32(1)<<30>>plcLog2InvLPCGainLowThres, downScaleQ30)
			downScaleQ30 <<= plcLog2InvLPCGainHighThres

			randGainQ15 = silkSMULWB(downScaleQ30, int16(randGainQ15)) >> 14
		}
	}

	randSeed := psPLC.randSeed
	lag = int(silkRShiftRound(int64(psPLC.pitchLQ8), 8))
	sLTPBufIdx := ltpMemLength

	// Rewhiten LTP state.
	idx := ltpMemLength - lag - d.lpcOrder - 5/2
	silkLPCAnalysisFilter(sLTP[idx:], d.ltpState[idx:], aQ12[:d.lpcOrder], ltpMemLength-idx, d.lpcOrder)
	// Scale LTP state.
	invGainQ30 := silkInverse32VarQ(psPLC.prevGainQ16[1], 46)
	invGainQ30 = silkMin32(invGainQ30, 0x7fffffff>>1)
	for i := idx + d.lpcOrder; i < ltpMemLength; i++ {
		sLTPQ14[i] = silkSMULWB(invGainQ30, sLTP[i])
	}

	// LTP synthesis filtering.
	for k := 0; k < d.nSubframes; k++ {
		predLag := sLTPBufIdx - lag + 5/2
		for i := 0; i < d.subfrmLen; i++ {
			// Unrolled loop; the initial 2 avoids a bias because SMLAWB
			// always rounds to -inf.
			ltpPredQ12 := int32(2)
			ltpPredQ12 = silkSMLAWB(ltpPredQ12, sLTPQ14[predLag], bQ14[0])
			ltpPredQ12 = silkSMLAWB(ltpPredQ12, sLTPQ14[predLag-1], bQ14[1])
			ltpPredQ12 = silkSMLAWB(ltpPredQ12, sLTPQ14[predLag-2], bQ14[2])
			ltpPredQ12 = silkSMLAWB(ltpPredQ12, sLTPQ14[predLag-3], bQ14[3])
			ltpPredQ12 = silkSMLAWB(ltpPredQ12, sLTPQ14[predLag-4], bQ14[4])
			predLag++

			// Generate LPC excitation.
			randSeed = silkRAND(randSeed)
			ridx := int(randSeed>>25) & plcRandBufMask
			sLTPQ14[sLTPBufIdx] = silkSMLAWB(ltpPredQ12, randPtr[ridx], randScaleQ14) << 2
			sLTPBufIdx++
		}

		// Gradually reduce LTP gain.
		for j := 0; j < 5; j++ {
			bQ14[j] = int16(silkSMULBB(harmGainQ15, int32(bQ14[j])) >> 15)
		}
		// Gradually reduce excitation gain.
		randScaleQ14 = int16(silkSMULBB(int32(randScaleQ14), randGainQ15) >> 15)

		// Slowly increase pitch lag.
		psPLC.pitchLQ8 = silkSMLAWB(psPLC.pitchLQ8, psPLC.pitchLQ8, int16(plcPitchDriftFacQ16))
		psPLC.pitchLQ8 = silkMin32(psPLC.pitchLQ8, silkSMULBB(plcMaxPitchLagMs, int32(d.fsKHz))<<8)
		lag = int(silkRShiftRound(int64(psPLC.pitchLQ8), 8))
	}

	// LPC synthesis filtering.
	sLPCQ14 := sLTPQ14[ltpMemLength-silkMaxLPCOrder:]
	copy(sLPCQ14[:silkMaxLPCOrder], d.lpcState)

	frame = make([]int16, d.frameSize)
	for i := 0; i < d.frameSize; i++ {
		lpcPredQ10 := int32(d.lpcOrder >> 1)
		for j := 0; j < d.lpcOrder; j++ {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPCQ14[silkMaxLPCOrder+i-j-1], aQ12[j])
		}

		// Add prediction to LPC excitation.
		sLPCQ14[silkMaxLPCOrder+i] = silkAddSat32(sLPCQ14[silkMaxLPCOrder+i], silkLShiftSat32(lpcPredQ10, 4))

		// Scale with gain.
		frame[i] = silkSAT16(silkRShiftRound(int64(silkSMULWW(sLPCQ14[silkMaxLPCOrder+i], prevGainQ10[1])), 8))
	}

	// Save LPC state.
	copy(d.lpcState, sLPCQ14[d.frameSize:d.frameSize+silkMaxLPCOrder])

	// Update states.
	psPLC.randSeed = randSeed
	psPLC.randScaleQ14 = randScaleQ14
	return frame, lag
}

// silk_PLC_glue_frames: smooth the transition between concealed and good
// frames in place.
func (d *Decoder) plcGlueFrames(frame []int16) {
	psPLC := &d.plc
	length := len(frame)
	if d.lossCnt > 0 {
		// Calculate energy in concealed residual.
		psPLC.concEnergy, psPLC.concEnergyShift = silkSumSqrShift(frame)
		psPLC.lastFrameLost = true
		return
	}
	if psPLC.lastFrameLost {
		// Calculate residual in decoded signal if last frame was lost.
		energy, energyShift := silkSumSqrShift(frame)

		// Normalize energies.
		if energyShift > psPLC.concEnergyShift {
			psPLC.concEnergy >>= uint(energyShift - psPLC.concEnergyShift)
		} else if energyShift < psPLC.concEnergyShift {
			energy >>= uint(psPLC.concEnergyShift - energyShift)
		}

		// Fade in the energy difference.
		if energy > psPLC.concEnergy {
			lz := silkCLZ32(psPLC.concEnergy)
			lz--
			psPLC.concEnergy <<= uint(lz)
			energy >>= uint(silkMax32(int32(24-lz), 0))

			fracQ24 := psPLC.concEnergy / silkMax32(energy, 1)

			gainQ16 := silkSqrtApprox(fracQ24) << 4
			slopeQ16 := (int32(1)<<16 - gainQ16) / int32(length)
			// Make slope 4x steeper to avoid missing onsets after DTX.
			slopeQ16 <<= 2
			for i := 0; i < length; i++ {
				frame[i] = int16(silkSMULWB(gainQ16, frame[i]))
				gainQ16 += slopeQ16
				if gainQ16 > 1<<16 {
					break
				}
			}
		}
	}
	psPLC.lastFrameLost = false
}

// silk_CNG_exc
func (d *Decoder) cngExc(excQ14 []int32) {
	excMask := cngBufMaskMax
	for excMask > len(excQ14) {
		excMask >>= 1
	}
	seed := d.cng.randSeed
	for i := range excQ14 {
		seed = silkRAND(seed)
		idx := int(seed>>24) & excMask
		excQ14[i] = d.cng.excBufQ14[idx]
	}
	d.cng.randSeed = seed
}

// silk_CNG: update the comfort-noise estimate after a good frame and add
// comfort noise to a concealed frame. ctrl may be nil on the concealment path.
func (d *Decoder) cngProcess(ctrl *decoderControl, frame []int16) {
	psCNG := &d.cng
	if d.fsKHz != psCNG.fsKHz {
		d.cngReset()
		psCNG.fsKHz = d.fsKHz
	}
	if d.lossCnt == 0 && d.prevSignalType == SignalTypeInactive && ctrl != nil {
		// Smoothing of LSFs.
		for i := 0; i < d.lpcOrder; i++ {
			psCNG.smthNLSFQ15[i] += int16(silkSMULWB(int32(d.prevNLSFQ15[i])-int32(psCNG.smthNLSFQ15[i]), cngNLSFSmthQ16))
		}
		// Find the subframe with the highest gain.
		maxGainQ16 := int32(0)
		subfr := 0
		for i := 0; i < d.nSubframes; i++ {
			if ctrl.gainsQ16[i] > maxGainQ16 {
				maxGainQ16 = ctrl.gainsQ16[i]
				subfr = i
			}
		}
		// Update CNG excitation buffer with excitation from this subframe.
		copy(psCNG.excBufQ14[d.subfrmLen:d.nSubframes*d.subfrmLen], psCNG.excBufQ14[:(d.nSubframes-1)*d.subfrmLen])
		copy(psCNG.excBufQ14[:d.subfrmLen], d.excQ14[subfr*d.subfrmLen:(subfr+1)*d.subfrmLen])

		// Smooth gains.
		for i := 0; i < d.nSubframes; i++ {
			psCNG.smthGainQ16 += silkSMULWB(ctrl.gainsQ16[i]-psCNG.smthGainQ16, cngGainSmthQ16)
			// If the smoothed gain is 3 dB greater than this subframe's gain,
			// use this subframe's gain to adapt faster.
			if silkSMULWW(psCNG.smthGainQ16, cngGainSmthThresholdQ16) > ctrl.gainsQ16[i] {
				psCNG.smthGainQ16 = ctrl.gainsQ16[i]
			}
		}
	}

	// Add CNG when packet is lost or during DTX.
	if d.lossCnt > 0 {
		length := len(frame)
		cngSigQ14 := make([]int32, length+silkMaxLPCOrder)

		// Generate CNG excitation.
		gainQ16 := silkSMULWW(int32(d.plc.randScaleQ14), d.plc.prevGainQ16[1])
		if gainQ16 >= 1<<21 || psCNG.smthGainQ16 > 1<<23 {
			gainQ16 = silkSMULTT(gainQ16, gainQ16)
			gainQ16 = silkSMULTT(psCNG.smthGainQ16, psCNG.smthGainQ16) - gainQ16<<5
			gainQ16 = silkSqrtApprox(gainQ16) << 16
		} else {
			gainQ16 = silkSMULWW(gainQ16, gainQ16)
			gainQ16 = silkSMULWW(psCNG.smthGainQ16, psCNG.smthGainQ16) - gainQ16<<5
			gainQ16 = silkSqrtApprox(gainQ16) << 8
		}
		gainQ10 := gainQ16 >> 6

		d.cngExc(cngSigQ14[silkMaxLPCOrder:])

		// Convert CNG NLSF to filter representation.
		aQ12 := nlsfToLPCLibopus(psCNG.smthNLSFQ15[:d.lpcOrder], d.lpcOrder)

		// Generate CNG signal by synthesis filtering.
		copy(cngSigQ14[:silkMaxLPCOrder], psCNG.synthState[:])
		for i := 0; i < length; i++ {
			lpcPredQ10 := int32(d.lpcOrder >> 1)
			for j := 0; j < d.lpcOrder; j++ {
				lpcPredQ10 = silkSMLAWB(lpcPredQ10, cngSigQ14[silkMaxLPCOrder+i-j-1], aQ12[j])
			}
			// Update states.
			cngSigQ14[silkMaxLPCOrder+i] = silkAddSat32(cngSigQ14[silkMaxLPCOrder+i], silkLShiftSat32(lpcPredQ10, 4))

			// Scale with gain and add to input signal.
			frame[i] = silkAddSat16(frame[i], silkSAT16(silkRShiftRound(int64(silkSMULWW(cngSigQ14[silkMaxLPCOrder+i], gainQ10)), 8)))
		}
		copy(psCNG.synthState[:], cngSigQ14[length:length+silkMaxLPCOrder])
	} else {
		for i := 0; i < d.lpcOrder; i++ {
			psCNG.synthState[i] = 0
		}
	}
}

// concealFrame runs the libopus lost-frame path of silk_decode_frame: PLC
// synthesis, output-buffer update, comfort noise, glue bookkeeping, and the
// lagPrev update. It returns the concealed int16 frame.
func (d *Decoder) concealFrame() []int16 {
	d.plcCheckReset()
	frame, lag := d.plcConceal()
	d.lossCnt++

	d.pushOutputHistory(frame)
	d.cngProcess(nil, frame)
	d.plcGlueFrames(frame)
	d.lagPrev = lag
	return frame
}

// pushOutputHistory mirrors the outBuf memmove/memcpy in silk_decode_frame.
func (d *Decoder) pushOutputHistory(frame []int16) {
	ltpMemLen := len(d.ltpState)
	if len(frame) <= ltpMemLen {
		mvLen := ltpMemLen - len(frame)
		copy(d.ltpState[:mvLen], d.ltpState[len(frame):])
		for i, s := range frame {
			d.ltpState[mvLen+i] = int32(s)
		}
		return
	}
	offset := len(frame) - ltpMemLen
	for i := range d.ltpState {
		d.ltpState[i] = int32(frame[offset+i])
	}
}

// ── fixed-point helpers ──────────────────────────────────────────────────────

// silk_sum_sqr_shift
func silkSumSqrShift(x []int16) (energy int32, shift int) {
	n := len(x)
	// Scan the input to find the shift that keeps the sum in 32 bits with
	// two bits of headroom.
	shft := 31 - silkCLZ32(int32(n))
	nrg := int32(n)
	i := 0
	for ; i < n-1; i += 2 {
		nrgTmp := uint32(silkSMULBB(int32(x[i]), int32(x[i])))
		nrgTmp = uint32(silkADD32Ovflw(int32(nrgTmp), silkSMULBB(int32(x[i+1]), int32(x[i+1]))))
		nrg = int32(uint32(nrg) + nrgTmp>>uint(shft))
	}
	if i < n {
		nrgTmp := uint32(silkSMULBB(int32(x[i]), int32(x[i])))
		nrg = int32(uint32(nrg) + nrgTmp>>uint(shft))
	}
	shft = silkMaxInt(0, shft+3-silkCLZ32(nrg))
	nrg = 0
	for i = 0; i < n-1; i += 2 {
		nrgTmp := uint32(silkSMULBB(int32(x[i]), int32(x[i])))
		nrgTmp = uint32(silkADD32Ovflw(int32(nrgTmp), silkSMULBB(int32(x[i+1]), int32(x[i+1]))))
		nrg = int32(uint32(nrg) + nrgTmp>>uint(shft))
	}
	if i < n {
		nrgTmp := uint32(silkSMULBB(int32(x[i]), int32(x[i])))
		nrg = int32(uint32(nrg) + nrgTmp>>uint(shft))
	}
	return nrg, shft
}

// silk_bwexpander (int16 variant)
func silkBwexpander16(ar []int16, d int, chirpQ16 int32) {
	chirpMinusOneQ16 := chirpQ16 - 65536
	for i := 0; i < d-1; i++ {
		ar[i] = int16(silkRShiftRound(int64(chirpQ16)*int64(ar[i]), 16))
		chirpQ16 += silkRShiftRound(int64(chirpQ16)*int64(chirpMinusOneQ16), 16)
	}
	ar[d-1] = int16(silkRShiftRound(int64(chirpQ16)*int64(ar[d-1]), 16))
}

// silk_CLZ_FRAC
func silkCLZFrac(in int32) (lz int, fracQ7 int32) {
	lz = silkCLZ32(in)
	fracQ7 = silkROR32(in, 24-lz) & 0x7f
	return
}

// silk_ROR32
func silkROR32(a32 int32, rot int) int32 {
	x := uint32(a32)
	if rot == 0 {
		return a32
	}
	if rot < 0 {
		r := uint(-rot)
		return int32(x<<r | x>>(32-r))
	}
	r := uint(rot)
	return int32(x<<(32-r) | x>>r)
}

// silk_SQRT_APPROX
func silkSqrtApprox(x int32) int32 {
	if x <= 0 {
		return 0
	}
	lz, fracQ7 := silkCLZFrac(x)
	var y int32
	if lz&1 != 0 {
		y = 32768
	} else {
		y = 46214 // sqrt(2) * 32768
	}
	// Get scaling right.
	y >>= uint(lz >> 1)
	// Increment using fractional part of input.
	y = silkSMLAWB(y, y, int16(silkSMULBB(213, fracQ7)))
	return y
}

// silk_SMULTT
func silkSMULTT(a, b int32) int32 { return (a >> 16) * (b >> 16) }

// silk_ADD_SAT16
func silkAddSat16(a, b int16) int16 { return silkSAT16(int32(a) + int32(b)) }

func silkMax16(a, b int16) int16 {
	if a > b {
		return a
	}
	return b
}

func silkMax32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func silkMin32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func silkMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func silkMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
