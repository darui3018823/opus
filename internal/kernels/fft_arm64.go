//go:build arm64 && go1.27 && !purego

package kernels

//go:noescape
func fftRadix4NEON(f, t *float32, m, groups, mm int)

//go:noescape
func fftRadix3NEON(f, t *float32, epi float32, m, groups, mm int)

//go:noescape
func fftRadix5NEON(f, t, y *float32, m, groups, mm int)

func hasFFT() bool { return useNEON }

func fftRadix4Arch(f, t []float32, m, groups, mm int) bool {
	if !useNEON {
		return false
	}
	fftRadix4NEON(&f[0], &t[0], m, groups, mm)
	return true
}

func fftRadix3Arch(f, t []float32, epi float32, m, groups, mm int) bool {
	if !useNEON {
		return false
	}
	fftRadix3NEON(&f[0], &t[0], epi, m, groups, mm)
	return true
}

func fftRadix5Arch(f, t []float32, ya, yb [2]float32, m, groups, mm int) bool {
	if !useNEON {
		return false
	}
	y := [16]float32{
		ya[0], ya[0], ya[0], ya[0], ya[1], ya[1], ya[1], ya[1],
		yb[0], yb[0], yb[0], yb[0], yb[1], yb[1], yb[1], yb[1],
	}
	fftRadix5NEON(&f[0], &t[0], &y[0], m, groups, mm)
	return true
}
