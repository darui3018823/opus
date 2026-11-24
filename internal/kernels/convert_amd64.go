//go:build amd64 && !purego

package kernels

//go:noescape
func f64to32SSE2(dst *float32, src *float64, blocks int)

//go:noescape
func f32to64SSE2(dst *float64, src *float32, blocks int)

func f64to32Arch(dst []float32, src []float64) int {
	blocks := len(src) / 4
	if !useSSE || blocks == 0 {
		return 0
	}
	f64to32SSE2(&dst[0], &src[0], blocks)
	return blocks * 4
}

func f32to64Arch(dst []float64, src []float32) int {
	blocks := len(src) / 4
	if !useSSE || blocks == 0 {
		return 0
	}
	f32to64SSE2(&dst[0], &src[0], blocks)
	return blocks * 4
}
