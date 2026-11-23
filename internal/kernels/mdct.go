package kernels

// The pre- and post-rotations of the float libopus backward MDCT
// (clt_mdct_backward_c) for the static-mode sizes, around the N/4-point
// complex FFT. fout is the FFT buffer as interleaved complex float32, trig
// the mode's N/2 float32 twiddles, x and buf the float64 coefficient and
// output arrays (their float32 conversions are exact on the way out and
// rounded as float32(x) on the way in).

// BitRev is an FFT input permutation whose indices have been checked once,
// so the kernels can scatter through it without bounds checks.
type BitRev struct{ idx []int }

// NewBitRev validates that perm holds indices in [0, len(perm)).
func NewBitRev(perm []int) *BitRev {
	for _, b := range perm {
		if uint(b) >= uint(len(perm)) {
			panic("kernels: bitrev index out of range")
		}
	}
	return &BitRev{idx: perm}
}

// IMDCTPreRotate writes the pre-rotated coefficients into the bit-reversed
// FFT input: for i < n4,
//
//	xp1, xp2 = float32(x[2i]), float32(x[2*n4-1-2i])
//	fout[bitrev[i]] = (xp1*t0 - xp2*t1, xp2*t0 + xp1*t1),  t0, t1 = trig[i], trig[n4+i]
//
// bitrev must have exactly n4 entries.
func IMDCTPreRotate(fout []float32, x []float64, trig []float32, bitrev *BitRev, n4 int) {
	_ = x[2*n4-1]
	_ = trig[2*n4-1]
	_ = fout[2*n4-1]
	br := bitrev.idx
	if len(br) != n4 {
		panic("kernels: bitrev size mismatch")
	}
	done := 0
	if n4%4 == 0 {
		done = imdctPreArch(fout, x, trig, br, n4)
	}
	imdctPreGeneric(fout, x, trig, br, n4, done)
}

// IMDCTPreRotateGeneric is the pure Go reference for IMDCTPreRotate.
func IMDCTPreRotateGeneric(fout []float32, x []float64, trig []float32, bitrev []int, n4 int) {
	imdctPreGeneric(fout, x, trig, bitrev, n4, 0)
}

func imdctPreGeneric(fout []float32, x []float64, trig []float32, bitrev []int, n4, from int) {
	n2 := 2 * n4
	for i := from; i < n4; i++ {
		xp1 := float32(x[2*i])
		xp2 := float32(x[n2-1-2*i])
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := float32(xp2*t0) + float32(xp1*t1)
		yi := float32(xp1*t0) - float32(xp2*t1)
		b := bitrev[i]
		fout[2*b], fout[2*b+1] = yi, yr
	}
}

// IMDCTPostRotate rotates the FFT output into buf[:2*n4], walking from both
// ends: for i < (n4+1)/2, with k = n4-1-i,
//
//	buf[2i], buf[2*n4-1-2i]   from fout[i] and trig[i], trig[n4+i]
//	buf[2*n4-2-2i], buf[2i+1] from fout[k] and trig[n4-1-i], trig[2*n4-1-i]
func IMDCTPostRotate(buf []float64, fout []float32, trig []float32, n4 int) {
	_ = buf[2*n4-1]
	_ = trig[2*n4-1]
	_ = fout[2*n4-1]
	done := imdctPostArch(buf, fout, trig, n4)
	imdctPostGeneric(buf, fout, trig, n4, done)
}

// IMDCTPostRotateGeneric is the pure Go reference for IMDCTPostRotate.
func IMDCTPostRotateGeneric(buf []float64, fout []float32, trig []float32, n4 int) {
	imdctPostGeneric(buf, fout, trig, n4, 0)
}

func imdctPostGeneric(buf []float64, fout []float32, trig []float32, n4, from int) {
	n2 := 2 * n4
	for i := from; i < (n4+1)/2; i++ {
		yp0 := 2 * i
		yp1 := n2 - 2 - 2*i
		k1 := n4 - 1 - i

		re := fout[2*i+1]
		im := fout[2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := float32(re*t0) + float32(im*t1)
		yi := float32(re*t1) - float32(im*t0)

		re = fout[2*k1+1]
		im = fout[2*k1]
		buf[yp0] = float64(yr)
		buf[yp1+1] = float64(yi)

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = float32(re*t0) + float32(im*t1)
		yi = float32(re*t1) - float32(im*t0)
		buf[yp1] = float64(yr)
		buf[yp0+1] = float64(yi)
	}
}
