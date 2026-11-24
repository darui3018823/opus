//go:build amd64 && !purego

package kernels

// SSE is part of the amd64 baseline, so the 4-lane kernels need no feature
// check. The 8-lane AVX kernels are used when the CPU and the OS support
// AVX. Both use only MULPS/ADDPS (never FMA), one lag per lane.
var (
	useSSE = true
	useAVX = cpuHasAVX()
)

// Features reports the accelerated instruction sets in use.
func Features() string {
	switch {
	case useAVX:
		return "avx"
	case useSSE:
		return "sse"
	}
	return "generic"
}

// xcorr32AVX computes out[k] = Σ_{j<n} x[j]*y[k+j] for k < 32.
//
//go:noescape
func xcorr32AVX(x, y *float32, n int, out *float32)

// xcorr8AVX computes out[k] = Σ_{j<n} x[j]*y[k+j] for k < 8.
//
//go:noescape
func xcorr8AVX(x, y *float32, n int, out *float32)

// xcorr16SSE computes out[k] = Σ_{j<n} x[j]*y[k+j] for k < 16.
//
//go:noescape
func xcorr16SSE(x, y *float32, n int, out *float32)

// xcorr4SSE computes out[k] = Σ_{j<n} x[j]*y[k+j] for k < 4.
//
//go:noescape
func xcorr4SSE(x, y *float32, n int, out *float32)

// pitchXcorr32Arch computes as many leading lags as fit the vector kernels
// and returns how many it computed. The caller has checked the bounds.
func pitchXcorr32Arch(x, y, xcorr []float32, length, maxPitch int) int {
	if length <= 0 || !useSSE {
		return 0
	}
	px := &x[0]
	i := 0
	if useAVX {
		for ; i+32 <= maxPitch; i += 32 {
			xcorr32AVX(px, &y[i], length, &xcorr[i])
		}
		for ; i+8 <= maxPitch; i += 8 {
			xcorr8AVX(px, &y[i], length, &xcorr[i])
		}
	}
	for ; i+16 <= maxPitch; i += 16 {
		xcorr16SSE(px, &y[i], length, &xcorr[i])
	}
	for ; i+4 <= maxPitch; i += 4 {
		xcorr4SSE(px, &y[i], length, &xcorr[i])
	}
	// Finish the last 1 to 3 lags with a block that overlaps lags already
	// done. Each lane's sum depends only on its own lag, so the overlapped
	// lags are rewritten with the same bits.
	if i < maxPitch && maxPitch >= 4 {
		xcorr4SSE(px, &y[maxPitch-4], length, &xcorr[maxPitch-4])
		i = maxPitch
	}
	return i
}
