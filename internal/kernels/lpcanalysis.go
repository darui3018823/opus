package kernels

// lpcAnalysisMaxLen bounds the lengths the accelerated path converts into
// its stack buffer; longer calls (none in SILK) run the reference.
const lpcAnalysisMaxLen = 512

// LPCAnalysisFilter is silk_LPC_analysis_filter as SILK calls it, on int32
// samples truncated to int16:
//
//	out[ix] = sat16(RSHIFT_ROUND((in[ix] << 12) - Σ_{j<order} int16(in[ix-1-j])*b[j], 12))
//
// for order <= ix < length, with the int32 sum wrapping, and out[ix] = 0 for
// ix < order. The terms are exact int16 products and the int32 sum wraps,
// so the accelerated versions, which add them in another order, return the
// same bits.
func LPCAnalysisFilter(out []int16, in []int32, b []int16, length, order int) {
	if length <= 0 {
		return
	}
	if order < 0 || order > len(b) {
		panic("kernels: bad LPC analysis order")
	}
	_ = out[length-1]
	_ = in[length-1]
	done := order
	if order >= 1 && order <= 16 && length-order >= 4 && length <= lpcAnalysisMaxLen {
		done += lpcAnalysisArch(out, in, b, length, order)
	}
	lpcAnalysisRange(out, in, b, order, done, length)
	for ix := 0; ix < order && ix < length; ix++ {
		out[ix] = 0
	}
}

// LPCAnalysisFilterGeneric is the pure Go reference for LPCAnalysisFilter.
func LPCAnalysisFilterGeneric(out []int16, in []int32, b []int16, length, order int) {
	lpcAnalysisRange(out, in, b, order, order, length)
	for ix := 0; ix < order && ix < length; ix++ {
		out[ix] = 0
	}
}

func lpcAnalysisRange(out []int16, in []int32, b []int16, order, from, to int) {
	for ix := from; ix < to; ix++ {
		inPtr := ix - 1
		acc := int32(int16(in[inPtr])) * int32(b[0])
		for j := 1; j < order; j++ {
			acc += int32(int16(in[inPtr-j])) * int32(b[j])
		}
		acc = (in[ix] << 12) - acc
		out[ix] = sat16(((acc >> 11) + 1) >> 1)
	}
}

func sat16(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

// lpcAnalysisTaps lays b out for the kernels: output ix = order+i is
// Σ_{k<16} buf[i+k] * taps[k] with buf[t] = int16(in[t]), so taps[k] is
// b[order-1-k] for k < order and zero above.
func lpcAnalysisTaps(b []int16, order int) (taps [16]int16) {
	for k := 0; k < order; k++ {
		taps[k] = b[order-1-k]
	}
	return taps
}
