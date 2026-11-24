//go:build arm64 && go1.27 && !purego

package kernels

//go:noescape
func combConstNEON(x *float32, period, blocks int, g0, g1, g2 float32)

//go:noescape
func combOverlapNEON(x *float32, p0, p1, blocks int, gains *CombGains, w *float32)

func combConstArch(x []float32, start, n, period int, g0, g1, g2 float32) int {
	blocks := n / 4
	if !useNEON || blocks == 0 {
		return 0
	}
	combConstNEON(&x[start], period, blocks, g0, g1, g2)
	return blocks * 4
}

func combOverlapArch(x []float32, start, n, p0, p1 int, g *CombGains, w []float32) int {
	blocks := n / 4
	if !useNEON || blocks == 0 {
		return 0
	}
	combOverlapNEON(&x[start], p0, p1, blocks, g, &w[0])
	return blocks * 4
}
