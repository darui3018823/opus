//go:build arm64 && go1.27 && !purego

package kernels

//go:noescape
func xcorr16NEON(x, y *float32, n int, out *float32)

//go:noescape
func xcorr4NEON(x, y *float32, n int, out *float32)

func pitchXcorr32Arch(x, y, xcorr []float32, length, maxPitch int) int {
	if length <= 0 || !useNEON {
		return 0
	}
	px := &x[0]
	i := 0
	for ; i+16 <= maxPitch; i += 16 {
		xcorr16NEON(px, &y[i], length, &xcorr[i])
	}
	for ; i+4 <= maxPitch; i += 4 {
		xcorr4NEON(px, &y[i], length, &xcorr[i])
	}
	// The last 1 to 3 lags come from a block overlapping lags already
	// done; each lane depends only on its own lag, so they get the same bits.
	if i < maxPitch && maxPitch >= 4 {
		xcorr4NEON(px, &y[maxPitch-4], length, &xcorr[maxPitch-4])
		i = maxPitch
	}
	return i
}
