package kernels

// WarpedAR4 holds the warped AR noise-shaping filter of the SILK
// delayed-decision quantizer for up to four decision states at once, one
// state per lane (struct of arrays). Lanes beyond the live state count are
// computed and ignored.
type WarpedAR4 struct {
	// S[j][k] is sAR2_Q14[j] of state k.
	S [MaxShapeOrder][4]int32
	// Ar[j] is AR_shp_Q13[j] sign-extended, duplicated for two lanes.
	Ar [MaxShapeOrder][2]int64
	// Warp is warping_Q16 as int16, Tilt is Tilt_Q14 as int16, both
	// sign-extended and duplicated.
	Warp  [2]int64
	Tilt  [2]int64
	Order int
	// Lanes is 2 or 4: how many states Step updates. Lanes 2 and 3 are
	// left untouched when it is 2.
	Lanes int
}

// MaxShapeOrder is MAX_SHAPE_LPC_ORDER.
const MaxShapeOrder = 24

// SetCoefficients loads the per-subframe filter constants for the given
// number of live states (1 to 4). order must be even and in
// [2, MaxShapeOrder].
func (w *WarpedAR4) SetCoefficients(ar []int16, order int, warp, tilt int16, states int) {
	if order < 2 || order > MaxShapeOrder || order&1 != 0 {
		panic("kernels: bad shaping order")
	}
	if states < 1 || states > 4 {
		panic("kernels: bad state count")
	}
	w.Lanes = 4
	if states <= 2 {
		w.Lanes = 2
	}
	_ = ar[order-1]
	for j := 0; j < order; j++ {
		w.Ar[j] = [2]int64{int64(ar[j]), int64(ar[j])}
	}
	w.Warp = [2]int64{int64(warp), int64(warp)}
	w.Tilt = [2]int64{int64(tilt), int64(tilt)}
	w.Order = order
}

// Step runs one sample of the filter for all four lanes, as in
// silk_noise_shape_quantizer_del_dec:
//
//	tmp2 = SMLAWB(diff, s[0], warp); tmp1 = SMLAWB(s[0], s[1]-tmp2, warp)
//	... (the warped all-pass chain over s[0:order]) ...
//	n_AR = ((order>>1 + Σ SMULWB(chain, ar)) << 1 + SMULWB(lfAR, tilt)) << 2
//
// It updates S and stores n_AR_Q14 for each lane in out.
func (w *WarpedAR4) Step(diff, lfAR, out *[4]int32) {
	if warpedAR4Arch(w, diff, lfAR, out) {
		return
	}
	w.StepGeneric(diff, lfAR, out)
}

// HasStateLanes reports whether WarpedAR4.Step and LPC4.Predict have vector
// implementations on this CPU. Without them, callers are better served by
// their own per-state scalar loops than by the struct-of-arrays layout.
func HasStateLanes() bool { return hasStateLanes() }

func smlawb(a, b int32, c int64) int32 {
	return a + int32((int64(b)*c)>>16)
}

// StepGeneric is the pure Go reference for Step.
func (w *WarpedAR4) StepGeneric(diff, lfAR, out *[4]int32) {
	order := w.Order
	warp, tilt := w.Warp[0], w.Tilt[0]
	lanes := w.Lanes
	if lanes != 2 {
		lanes = 4
	}
	for k := 0; k < lanes; k++ {
		s := func(j int) int32 { return w.S[j][k] }
		tmp2 := smlawb(diff[k], s(0), warp)
		tmp1 := smlawb(s(0), int32(uint32(s(1))-uint32(tmp2)), warp)
		w.S[0][k] = tmp2
		n := int32(order >> 1)
		n = smlawb(n, tmp2, w.Ar[0][0])
		for j := 2; j+1 < order; j += 2 {
			tmp2 = smlawb(s(j-1), int32(uint32(s(j))-uint32(tmp1)), warp)
			w.S[j-1][k] = tmp1
			n = smlawb(n, tmp1, w.Ar[j-1][0])
			tmp1 = smlawb(s(j), int32(uint32(s(j+1))-uint32(tmp2)), warp)
			w.S[j][k] = tmp2
			n = smlawb(n, tmp2, w.Ar[j][0])
		}
		w.S[order-1][k] = tmp1
		n = smlawb(n, tmp1, w.Ar[order-1][0])
		n = int32(uint32(n) << 1)
		n = smlawb(n, lfAR[k], tilt)
		out[k] = int32(uint32(n) << 2)
	}
}
