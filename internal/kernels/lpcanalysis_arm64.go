//go:build arm64 && go1.27 && !purego

package kernels

// lpcAnalysis4NEON writes n outputs (n a positive multiple of 4) from
// buf[0:n+15] (int16 samples), in[0:n] (the int32 samples at the outputs)
// and taps.
//
//go:noescape
func lpcAnalysis4NEON(out *int16, buf *int16, in *int32, taps *[16]int16, n int)

// lpcAnalysisArch computes outputs order..order+n-1 for the largest
// multiple of four n that fits and returns n. The caller has checked
// 1 <= order <= 16, length-order >= 4 and length <= lpcAnalysisMaxLen.
func lpcAnalysisArch(out []int16, in []int32, b []int16, length, order int) int {
	if !useNEON {
		return 0
	}
	n := (length - order) &^ 3
	var buf [lpcAnalysisMaxLen + 16]int16
	for t, v := range in[:length] {
		buf[t] = int16(v)
	}
	taps := lpcAnalysisTaps(b, order)
	lpcAnalysis4NEON(&out[order], &buf[0], &in[order], &taps, n)
	return n
}
