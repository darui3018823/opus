package kernels

// The CELT pitch post-filter (libopus comb_filter, float build, in place)
// on a contiguous float32 buffer. Output sample i reads samples back to
// i - period - 2 that the filter has already written, so it is recursive,
// but with period >= 6 the four outputs of a block read only samples
// before the block, and four consecutive outputs can run in lanes.

// CombGains are the six products gain*tap of comb_filter: g00..g02 for
// the old period and tapset, g10..g12 for the new.
type CombGains [6]float32

func combCheck(x []float32, start, n, period int) {
	if n < 0 || period < combMinPeriodForLanes || start-period-2 < 0 {
		panic("kernels: comb filter reads before the buffer")
	}
	_ = x[start+n-1]
}

// combMinPeriodForLanes keeps a block of four from reading its own outputs.
const combMinPeriodForLanes = 6

// CombConst filters x[start:start+n] in place with a fixed period:
//
//	y = x[i] + g0*x[i-p] + g1*(x[i-p+1] + x[i-p-1]) + g2*(x[i-p+2] + x[i-p-2])
//
// each product rounded and the terms added in that order.
func CombConst(x []float32, start, n, period int, g0, g1, g2 float32) {
	if n <= 0 {
		return
	}
	combCheck(x, start, n, period)
	done := combConstArch(x, start, n, period, g0, g1, g2)
	CombConstGeneric(x, start+done, n-done, period, g0, g1, g2)
}

// CombConstGeneric is the pure Go reference for CombConst.
func CombConstGeneric(x []float32, start, n, period int, g0, g1, g2 float32) {
	for pos := start; pos < start+n; pos++ {
		b := pos - period
		y := x[pos]
		y += float32(g0 * x[b])
		y += float32(g1 * (x[b+1] + x[b-1]))
		y += float32(g2 * (x[b+2] + x[b-2]))
		x[pos] = y
	}
}

// CombOverlap filters x[start:start+n] in place while crossfading from
// period p0 with gains g[0:3] to period p1 with gains g[3:6] over the
// squared window w[0:n] (f = w*w):
//
//	y = x[i] + ((1-f)*g00)*x[i-p0] + ((1-f)*g01)*(x[i-p0+1] + x[i-p0-1])
//	    + ((1-f)*g02)*(x[i-p0+2] + x[i-p0-2]) + (f*g10)*x[i-p1]
//	    + (f*g11)*(x[i-p1+1] + x[i-p1-1]) + (f*g12)*(x[i-p1+2] + x[i-p1-2])
func CombOverlap(x []float32, start, n, p0, p1 int, g *CombGains, w []float32) {
	if n <= 0 {
		return
	}
	combCheck(x, start, n, p0)
	combCheck(x, start, n, p1)
	_ = w[n-1]
	done := combOverlapArch(x, start, n, p0, p1, g, w)
	combOverlapGeneric(x, start, n, p0, p1, g, w, done)
}

// CombOverlapGeneric is the pure Go reference for CombOverlap.
func CombOverlapGeneric(x []float32, start, n, p0, p1 int, g *CombGains, w []float32) {
	combOverlapGeneric(x, start, n, p0, p1, g, w, 0)
}

func combOverlapGeneric(x []float32, start, n, p0, p1 int, g *CombGains, w []float32, from int) {
	for i := from; i < n; i++ {
		pos := start + i
		f := float32(w[i] * w[i])
		omf := float32(1) - f
		b0, b1 := pos-p0, pos-p1
		y := x[pos]
		y += float32(float32(omf*g[0]) * x[b0])
		y += float32(float32(omf*g[1]) * (x[b0+1] + x[b0-1]))
		y += float32(float32(omf*g[2]) * (x[b0+2] + x[b0-2]))
		y += float32(float32(f*g[3]) * x[b1])
		y += float32(float32(f*g[4]) * (x[b1+1] + x[b1-1]))
		y += float32(float32(f*g[5]) * (x[b1+2] + x[b1-2]))
		x[pos] = y
	}
}
