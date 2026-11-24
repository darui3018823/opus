package kernels

// FIR8Table is the 8-tap fractional-delay filter bank of the SILK IIR/FIR
// resampler laid out for the kernel: row t holds the eight taps for
// interpolation phase t, already in buffer order.
type FIR8Table [12][8]int16

// FIR8Interp computes the leading outputs of
//
//	dst[i] = sat16(round(Σ_k buf[(idx_i>>16)+k] * table[((idx_i&0xFFFF)*12)>>16][k], 15))
//
// with idx_i = indexQ16 + i*idxInc, where round(v, 15) is libopus
// silk_RSHIFT_ROUND. It returns how many outputs it wrote; the caller
// finishes the rest in scalar code. The sum is int32 with wrap-around, which
// is associative, so the vector version is bit-identical to the scalar one.
func FIR8Interp(dst, buf []int16, table *FIR8Table, indexQ16, idxInc int32) int {
	n := len(dst) &^ 3
	if n == 0 || indexQ16 < 0 || idxInc < 0 {
		// Negative positions are left to the scalar code, which indexes
		// (and panics) exactly as libopus's would.
		return 0
	}
	// The kernel steps the index in int32 and reads
	// buf[(idx>>16) : (idx>>16)+8] for the last output.
	last := int64(indexQ16) + int64(n-1)*int64(idxInc)
	if last > 1<<31-1 {
		return 0
	}
	_ = buf[int(last>>16)+7]
	return fir8InterpArch(dst[:n], buf, table, indexQ16, idxInc)
}
