//go:build arm64 && go1.27 && !purego

package kernels

//go:noescape
func f64to32NEON(dst *float32, src *float64, blocks int)

//go:noescape
func f32to64NEON(dst *float64, src *float32, blocks int)

//go:noescape
func float32ToInt16NEON(dst *int16, src *float32, blocks int)

func f64to32Arch(dst []float32, src []float64) int {
	blocks := len(src) / 4
	if !useNEON || blocks == 0 {
		return 0
	}
	f64to32NEON(&dst[0], &src[0], blocks)
	return blocks * 4
}

func f32to64Arch(dst []float64, src []float32) int {
	blocks := len(src) / 4
	if !useNEON || blocks == 0 {
		return 0
	}
	f32to64NEON(&dst[0], &src[0], blocks)
	return blocks * 4
}

func float32ToInt16Arch(dst []int16, src []float32) int {
	n := len(src) &^ 7
	if !useNEON || n == 0 {
		return 0
	}
	float32ToInt16NEON(&dst[0], &src[0], n/8)
	return n
}
