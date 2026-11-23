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

func hasFFT() bool { return false }

func fftRadix4Arch(f, t []float32, m, groups, mm int) bool { return false }

func fftRadix3Arch(f, t []float32, epi float32, m, groups, mm int) bool { return false }

func fftRadix5Arch(f, t []float32, ya, yb [2]float32, m, groups, mm int) bool { return false }

func imdctPreArch(fout []float32, x []float64, trig []float32, bitrev []int, n4 int) int {
	return 0
}

func imdctPostArch(buf []float64, fout, trig []float32, n4 int) int { return 0 }

func combConstArch(x []float32, start, n, period int, g0, g1, g2 float32) int { return 0 }

func combOverlapArch(x []float32, start, n, p0, p1 int, g *CombGains, w []float32) int {
	return 0
}

func f64to32Arch(dst []float32, src []float64) int { return 0 }

func f32to64Arch(dst []float64, src []float32) int { return 0 }
