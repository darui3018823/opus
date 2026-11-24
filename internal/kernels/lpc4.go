package kernels

// LPCRows is the length of the delayed-decision sLPC_Q14 history:
// NSQ_LPC_BUF_LENGTH plus the longest subframe.
const LPCRows = 136

// LPC4 is the short-term prediction of the SILK delayed-decision quantizer
// for up to four states at once, one state per lane.
type LPC4 struct {
	// H[r][k] is sLPC_Q14[r] of state k.
	H [LPCRows][4]int32
	// A[t] is a_Q12[t] sign-extended, duplicated for two lanes.
	A     [16][2]int64
	Order int
	// Lanes is 2 or 4: how many states Predict computes.
	Lanes int
}

// SetCoefficients loads a_Q12 for a subframe. order is 1 to 16; states is
// the live state count, 1 to 4.
func (p *LPC4) SetCoefficients(a []int16, order, states int) {
	if order < 1 || order > 16 {
		panic("kernels: bad LPC order")
	}
	if states < 1 || states > 4 {
		panic("kernels: bad state count")
	}
	_ = a[order-1]
	for t := 0; t < order; t++ {
		p.A[t] = [2]int64{int64(a[t]), int64(a[t])}
	}
	p.Order = order
	p.Lanes = 4
	if states <= 2 {
		p.Lanes = 2
	}
}

// Predict stores, for each lane, silk_noise_shape_quantizer_short_prediction
// over the window H[i : i+16] shifted left by 4 (LPC_pred_Q14):
//
//	(order>>1 + Σ_{t<order} SMULWB(H[i+15-t], a[t])) << 4
//
// The int32 sum wraps, so its order does not matter.
func (p *LPC4) Predict(i int, out *[4]int32) {
	_ = p.H[i+15]
	if lpc4Arch(p, i, out) {
		return
	}
	p.PredictGeneric(i, out)
}

// PredictGeneric is the pure Go reference for Predict.
func (p *LPC4) PredictGeneric(i int, out *[4]int32) {
	lanes := p.Lanes
	if lanes != 2 {
		lanes = 4
	}
	for k := 0; k < lanes; k++ {
		acc := int32(p.Order >> 1)
		for t := 0; t < p.Order; t++ {
			acc = smlawb(acc, p.H[i+15-t][k], p.A[t][0])
		}
		out[k] = int32(uint32(acc) << 4)
	}
}
