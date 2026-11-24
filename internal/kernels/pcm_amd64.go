//go:build amd64 && !purego

package kernels

// float32ToInt16SSE2 converts n samples (n a positive multiple of 8).
//
//go:noescape
func float32ToInt16SSE2(dst *int16, src *float32, n int)

func float32ToInt16Arch(dst []int16, src []float32) int {
	n := len(src) &^ 7
	if n == 0 || !useSSE {
		return 0
	}
	float32ToInt16SSE2(&dst[0], &src[0], n)
	return n
}
