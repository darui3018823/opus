//go:build amd64 && !purego

package kernels

// fir8Interp4SSE2 writes n outputs (n a positive multiple of 4).
//
//go:noescape
func fir8Interp4SSE2(dst, buf *int16, table *FIR8Table, indexQ16, idxInc int32, n int)

func fir8InterpArch(dst, buf []int16, table *FIR8Table, indexQ16, idxInc int32) int {
	if !useSSE {
		return 0
	}
	fir8Interp4SSE2(&dst[0], &buf[0], table, indexQ16, idxInc, len(dst))
	return len(dst)
}
