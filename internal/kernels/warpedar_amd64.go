//go:build amd64 && !purego

package kernels

var useSSE41 = cpuHasSSE41()

func cpuHasSSE41() bool {
	maxID, _, _, _ := cpuid(0, 0)
	if maxID < 1 {
		return false
	}
	_, _, ecx1, _ := cpuid(1, 0)
	return ecx1&(1<<19) != 0
}

//go:noescape
func warpedAR4SSE41(w *WarpedAR4, diff, lfAR, out *[4]int32)

//go:noescape
func warpedAR2SSE41(w *WarpedAR4, diff, lfAR, out *[4]int32)

func hasStateLanes() bool { return useSSE41 }

func warpedAR4Arch(w *WarpedAR4, diff, lfAR, out *[4]int32) bool {
	if !useSSE41 {
		return false
	}
	if w.Order < 2 || w.Order > MaxShapeOrder || w.Order&1 != 0 {
		panic("kernels: bad shaping order")
	}
	if w.Lanes == 2 {
		warpedAR2SSE41(w, diff, lfAR, out)
	} else {
		warpedAR4SSE41(w, diff, lfAR, out)
	}
	return true
}
