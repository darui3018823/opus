package kernels

// NSQCand4 is the per-state body of one sample of the SILK
// delayed-decision quantizer (silk_noise_shape_quantizer_del_dec, from the
// seed update to the two candidates' state) for up to four states, one per
// lane. The caller fills the inputs, runs Step, and reads the outputs;
// lanes beyond the live state count are computed and ignored.
type NSQCand4 struct {
	// Per-subframe constants, set by SetSubframe.
	LFLo     int64 // int16(LF_shp_Q14), sign-extended
	LFHi     int64 // LF_shp_Q14 >> 16
	Offset   int32 // offset_Q10
	Lambda   int32 // Lambda_Q10
	Lambda16 int32 // int32(int16(Lambda_Q10)), as silk_SMULBB reads it
	Rdo      int32 // Lambda_Q10/2 - 512, used when Lambda_Q10 > 2048

	// Per-sample scalars.
	XQ10    int32 // x_Q10[i]
	XQ10Sh  int32 // silk_LSHIFT32(x_Q10[i], 4)
	NLTP    int32 // n_LTP_Q14
	LTPPred int32 // LTP_pred_Q14

	// Per-state inputs. Seed is updated in place (silk_RAND).
	Seed    [4]int32
	NAR     [4]int32 // n_AR_Q14
	LPCPred [4]int32 // LPC_pred_Q14
	Shape   [4]int32 // Shape_Q14[*smpl_buf_idx]
	LFAR    [4]int32 // LF_AR_Q14
	RD      [4]int32 // RD_Q10 so far

	// Outputs per candidate rank c (0 = lower rate-distortion).
	Q      [2][4]int32 // Q_Q10
	RDc    [2][4]int32 // RD_Q10
	XQ     [2][4]int32 // xq_Q14
	Diff   [2][4]int32 // Diff_Q14
	LFOut  [2][4]int32 // LF_AR_Q14
	LTPShp [2][4]int32 // sLTP_shp_Q14
	LPCExc [2][4]int32 // LPC_exc_Q14
}

// QuantLevelAdjustQ10 is QUANT_LEVEL_ADJUST_Q10.
const QuantLevelAdjustQ10 = 80

// SetSubframe loads the per-subframe constants.
func (c *NSQCand4) SetSubframe(lfShpQ14, offsetQ10, lambdaQ10 int32) {
	c.LFLo = int64(int16(lfShpQ14))
	c.LFHi = int64(lfShpQ14 >> 16)
	c.Offset = offsetQ10
	c.Lambda = lambdaQ10
	c.Lambda16 = int32(int16(lambdaQ10))
	c.Rdo = lambdaQ10/2 - 512
}

// SetSample loads the per-sample scalars.
func (c *NSQCand4) SetSample(xQ10, nLTPQ14, ltpPredQ14 int32) {
	c.XQ10 = xQ10
	c.XQ10Sh = int32(uint32(xQ10) << 4)
	c.NLTP = nLTPQ14
	c.LTPPred = ltpPredQ14
}

// Step computes all four lanes.
func (c *NSQCand4) Step() {
	if nsqCand4Arch(c) {
		return
	}
	c.StepGeneric()
}

func addSat32(a, b int32) int32 {
	s := int64(a) + int64(b)
	switch {
	case s > 1<<31-1:
		return 1<<31 - 1
	case s < -1<<31:
		return -1 << 31
	}
	return int32(s)
}

func subSat32(a, b int32) int32 {
	s := int64(a) - int64(b)
	switch {
	case s > 1<<31-1:
		return 1<<31 - 1
	case s < -1<<31:
		return -1 << 31
	}
	return int32(s)
}

func smulbb(a, b int32) int32 { return int32(int16(a)) * int32(int16(b)) }

// quantCandidates is the libopus candidate pair for one residual.
func quantCandidates(rQ10, offsetQ10, lambdaQ10 int32) (q1Q10, q2Q10, rd1Q10, rd2Q10 int32) {
	const adj = QuantLevelAdjustQ10
	d := rQ10 - offsetQ10
	q1Q0 := d >> 10
	if lambdaQ10 > 2048 {
		rdo := lambdaQ10/2 - 512
		switch {
		case d > rdo:
			q1Q0 = (d - rdo) >> 10
		case d < -rdo:
			q1Q0 = (d + rdo) >> 10
		case d < 0:
			q1Q0 = -1
		default:
			q1Q0 = 0
		}
	}
	switch {
	case q1Q0 > 0:
		q1Q10 = int32(uint32(q1Q0)<<10) - adj + offsetQ10
		q2Q10 = q1Q10 + 1024
		rd1Q10 = smulbb(q1Q10, lambdaQ10)
		rd2Q10 = smulbb(q2Q10, lambdaQ10)
	case q1Q0 == 0:
		q1Q10 = offsetQ10
		q2Q10 = q1Q10 + 1024 - adj
		rd1Q10 = smulbb(q1Q10, lambdaQ10)
		rd2Q10 = smulbb(q2Q10, lambdaQ10)
	case q1Q0 == -1:
		q2Q10 = offsetQ10
		q1Q10 = q2Q10 - (1024 - adj)
		rd1Q10 = smulbb(-q1Q10, lambdaQ10)
		rd2Q10 = smulbb(q2Q10, lambdaQ10)
	default:
		q1Q10 = int32(uint32(q1Q0)<<10) + adj + offsetQ10
		q2Q10 = q1Q10 + 1024
		rd1Q10 = smulbb(-q1Q10, lambdaQ10)
		rd2Q10 = smulbb(-q2Q10, lambdaQ10)
	}
	rr := rQ10 - q1Q10
	rd1Q10 = (rd1Q10 + smulbb(rr, rr)) >> 10
	rr = rQ10 - q2Q10
	rd2Q10 = (rd2Q10 + smulbb(rr, rr)) >> 10
	return
}

// StepGeneric is the pure Go reference for Step.
func (c *NSQCand4) StepGeneric() {
	for k := 0; k < 4; k++ {
		seed := int32(uint32(907633515) + uint32(c.Seed[k])*uint32(196314165))
		c.Seed[k] = seed
		nLF := int32((int64(c.Shape[k]) * c.LFLo) >> 16)
		nLF += int32((int64(c.LFAR[k]) * c.LFHi) >> 16)
		nLF = int32(uint32(nLF) << 2)
		tmp1 := addSat32(c.NAR[k], nLF)
		tmp2 := c.NLTP + c.LPCPred[k]
		tmp1 = subSat32(tmp2, tmp1)
		tmp1 = int32((int64(tmp1) + 8) >> 4)
		r := c.XQ10 - tmp1
		if seed < 0 {
			r = -r
		}
		r = min(max(r, -(31<<10)), 30<<10)
		q1, q2, rd1, rd2 := quantCandidates(r, c.Offset, c.Lambda)
		if rd1 >= rd2 {
			q1, q2, rd1, rd2 = q2, q1, rd2, rd1
		}
		for i, q := range [2]int32{q1, q2} {
			c.Q[i][k] = q
			c.RDc[i][k] = c.RD[k] + [2]int32{rd1, rd2}[i]
			exc := int32(uint32(q) << 4)
			if seed < 0 {
				exc = -exc
			}
			lpcExc := exc + c.LTPPred
			xq := lpcExc + c.LPCPred[k]
			diff := xq - c.XQ10Sh
			lfAR := diff - c.NAR[k]
			c.XQ[i][k] = xq
			c.Diff[i][k] = diff
			c.LFOut[i][k] = lfAR
			c.LTPShp[i][k] = subSat32(lfAR, nLF)
			c.LPCExc[i][k] = lpcExc
		}
	}
}

// CopyRank copies state src's rank-1 candidate over state dst's rank-0
// candidate (the survivor replacement in the delayed-decision search).
func (c *NSQCand4) CopyRank(dst, src int) {
	c.Q[0][dst] = c.Q[1][src]
	c.RDc[0][dst] = c.RDc[1][src]
	c.XQ[0][dst] = c.XQ[1][src]
	c.Diff[0][dst] = c.Diff[1][src]
	c.LFOut[0][dst] = c.LFOut[1][src]
	c.LTPShp[0][dst] = c.LTPShp[1][src]
	c.LPCExc[0][dst] = c.LPCExc[1][src]
}
