//go:build !amd64 || purego

package kernels

// pitchXcorr32Arch has no accelerated version on this build; it computes no
// lags and leaves them all to the pure Go loop.
func pitchXcorr32Arch(x, y, xcorr []float32, length, maxPitch int) int {
	return 0
}

// Features reports the accelerated instruction sets in use.
func Features() string { return "generic" }

func fir8InterpArch(dst, buf []int16, table *FIR8Table, indexQ16, idxInc int32) int {
	return 0
}

func float32ToInt16Arch(dst []int16, src []float32) int {
	return 0
}

func hasStateLanes() bool { return false }

func warpedAR4Arch(w *WarpedAR4, diff, lfAR, out *[4]int32) bool { return false }

func lpc4Arch(p *LPC4, i int, out *[4]int32) bool { return false }

func nsqCand4Arch(c *NSQCand4) bool { return false }
