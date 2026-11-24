//go:build amd64 && !purego

package kernels

//go:noescape
func fftRadix4SSE(f, t *float32, m, groups, mm int)

//go:noescape
func fftRadix3SSE(f, t *float32, epi float32, m, groups, mm int)

//go:noescape
func fftRadix5SSE(f, t, y *float32, m, groups, mm int)

func hasFFT() bool { return useSSE }

func fftRadix4Arch(f, t []float32, m, groups, mm int) bool {
	if !useSSE {
		return false
	}
	fftRadix4SSE(&f[0], &t[0], m, groups, mm)
	return true
}

func fftRadix3Arch(f, t []float32, epi float32, m, groups, mm int) bool {
	if !useSSE {
		return false
	}
	fftRadix3SSE(&f[0], &t[0], epi, m, groups, mm)
	return true
}

func fftRadix5Arch(f, t []float32, ya, yb [2]float32, m, groups, mm int) bool {
	if !useSSE {
		return false
	}
	y := [16]float32{
		ya[0], ya[0], ya[0], ya[0], ya[1], ya[1], ya[1], ya[1],
		yb[0], yb[0], yb[0], yb[0], yb[1], yb[1], yb[1], yb[1],
	}
	fftRadix5SSE(&f[0], &t[0], &y[0], m, groups, mm)
	return true
}
