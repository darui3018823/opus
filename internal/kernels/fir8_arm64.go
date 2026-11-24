//go:build arm64 && go1.27 && !purego

package kernels

//go:noescape
func fir8Interp4NEON(dst, buf *int16, table *FIR8Table, indexQ16, idxInc int32, n int)

func fir8InterpArch(dst, buf []int16, table *FIR8Table, indexQ16, idxInc int32) int {
	if !useNEON {
		return 0
	}
	fir8Interp4NEON(&dst[0], &buf[0], table, indexQ16, idxInc, len(dst))
	return len(dst)
}
