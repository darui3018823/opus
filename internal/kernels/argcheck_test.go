package kernels

import (
	"math"
	"testing"
)

// The accelerated kernels run unchecked, so their wrappers must reject every
// argument the assembly would read or write out of range, on every build.

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: no panic", name)
		}
	}()
	f()
}

func TestPitchXcorr32ChecksLengthNotCapacity(t *testing.T) {
	backing := make([]float32, 64)
	x := make([]float32, 8)
	y := make([]float32, 8+16-1)
	// xcorr has room in its capacity but not its length: the reference
	// panics on the write, so the accelerated path must too.
	mustPanic(t, "short xcorr", func() { PitchXcorr32(x, y, backing[:1], 8, 16) })
	if backing[1] != 0 {
		t.Fatalf("wrote past len(xcorr): %v", backing[1])
	}

	// x and y are resliced like the reference does, so a zero-length x with
	// enough capacity gives the reference's result instead of a panic.
	xs := make([]float32, 8)
	for i := range xs {
		xs[i] = float32(i + 1)
	}
	for i := range y {
		y[i] = float32(i%5) - 2
	}
	got := make([]float32, 16)
	want := make([]float32, 16)
	PitchXcorr32(xs[:0], y, got, 8, 16)
	PitchXcorr32Generic(xs[:0], y, want, 8, 16)
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("lag %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLPC4PredictRejectsNegativeRow(t *testing.T) {
	var p LPC4
	p.SetCoefficients(make([]int16, 16), 16, 4)
	var out [4]int32
	for _, i := range []int{-1, -15} {
		mustPanic(t, "negative row", func() { p.Predict(i, &out) })
	}
	mustPanic(t, "row past end", func() { p.Predict(LPCRows-15, &out) })
	p.Predict(0, &out)
	p.Predict(LPCRows-16, &out)
}

func TestFFTRejectsBadStride(t *testing.T) {
	f := make([]float32, 32)
	tw := make([]float32, 64)
	ya, yb := [2]float32{}, [2]float32{}
	cases := []struct {
		name       string
		groups, mm int
	}{
		{"negative stride", 2, -8},
		{"overflowing stride", 17, 1 << 60},
		{"too many groups", 1 << 40, 1},
	}
	for _, c := range cases {
		mustPanic(t, "radix4 "+c.name, func() { FFTRadix4(f, tw, 4, c.groups, c.mm) })
		mustPanic(t, "radix3 "+c.name, func() { FFTRadix3(f, tw, 0, 4, c.groups, c.mm) })
		mustPanic(t, "radix5 "+c.name, func() { FFTRadix5(f, tw, ya, yb, 4, c.groups, c.mm) })
	}
}

func TestIMDCTRotationsCheckN4(t *testing.T) {
	// n4 <= 0 is a no-op, as in the references.
	IMDCTPreRotate(nil, nil, nil, NewBitRev(nil), 0)
	IMDCTPostRotate(nil, nil, nil, 0)
	IMDCTPostRotate(nil, nil, nil, -4)

	// 2*big wraps around to 8; the check must still reject it.
	big := math.MaxInt/2 + 5
	buf := make([]float64, 8)
	fout := make([]float32, 8)
	trig := make([]float32, 8)
	mustPanic(t, "overflowing n4", func() { IMDCTPostRotate(buf, fout, trig, big) })
	mustPanic(t, "short buffers", func() { IMDCTPostRotate(buf, fout, trig, 8) })
	mustPanic(t, "short buffers", func() {
		IMDCTPreRotate(fout, buf, trig, NewBitRev(make([]int, 8)), 8)
	})
}

func TestNewBitRevCopiesPermutation(t *testing.T) {
	perm := []int{1, 0, 3, 2}
	br := NewBitRev(perm)
	perm[0] = 1 << 20
	if br.idx[0] != 1 {
		t.Fatalf("BitRev follows the caller's slice: idx[0] = %d", br.idx[0])
	}
}

func TestCombRejectsOverflowingPeriod(t *testing.T) {
	x := make([]float32, 4)
	mustPanic(t, "const", func() { CombConst(x, 0, 4, math.MaxInt, 1, 1, 1) })
	var g CombGains
	w := make([]float32, 4)
	mustPanic(t, "overlap p0", func() { CombOverlap(x, 0, 4, math.MaxInt, 15, &g, w) })
	mustPanic(t, "overlap p1", func() { CombOverlap(x, 0, 4, 15, math.MaxInt, &g, w) })
	long := make([]float32, 32)
	mustPanic(t, "huge n", func() { CombConst(long, 20, math.MaxInt, 15, 1, 1, 1) })
	CombConst(long, 20, 12, 15, 1, 1, 1)
}
