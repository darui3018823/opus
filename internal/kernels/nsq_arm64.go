//go:build arm64 && go1.27 && !purego

package kernels

//go:noescape
func warpedAR4NEON(w *WarpedAR4, diff, lfAR, out *[4]int32)

//go:noescape
func warpedAR2NEON(w *WarpedAR4, diff, lfAR, out *[4]int32)

//go:noescape
func lpcPred4NEON(row *[4]int32, a *[16][2]int64, order int, out *[4]int32)

//go:noescape
func lpcPred2NEON(row *[4]int32, a *[16][2]int64, order int, out *[4]int32)

func hasStateLanes() bool { return useNEON }

func warpedAR4Arch(w *WarpedAR4, diff, lfAR, out *[4]int32) bool {
	if !useNEON {
		return false
	}
	if w.Order < 2 || w.Order > MaxShapeOrder || w.Order&1 != 0 {
		panic("kernels: bad shaping order")
	}
	if w.Lanes == 2 {
		warpedAR2NEON(w, diff, lfAR, out)
	} else {
		warpedAR4NEON(w, diff, lfAR, out)
	}
	return true
}

func lpc4Arch(p *LPC4, i int, out *[4]int32) bool {
	if !useNEON {
		return false
	}
	if p.Order < 1 || p.Order > 16 {
		panic("kernels: bad LPC order")
	}
	if p.Lanes == 2 {
		lpcPred2NEON(&p.H[i+15], &p.A, p.Order, out)
	} else {
		lpcPred4NEON(&p.H[i+15], &p.A, p.Order, out)
	}
	return true
}
