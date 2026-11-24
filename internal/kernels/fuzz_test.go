package kernels

import (
	"hash/fnv"
	"math"
	"math/rand"
	"testing"
)

// The fuzz targets drive every accelerated kernel and its pure Go reference
// with the same fuzzer-chosen shapes and values, on every path this build
// has (withPaths), and require the same bits. FuzzKernelsFloat covers the
// float kernels and FuzzKernelsFixed the fixed-point ones; both run in the
// nightly fuzz job on amd64 and arm64.

// fuzzSource reads values from the fuzz input and, once that is used up,
// from a generator seeded by it, so short inputs still fill whole buffers.
type fuzzSource struct {
	data []byte
	rest *rand.Rand
}

func newFuzzSource(data []byte) *fuzzSource {
	h := fnv.New64a()
	h.Write(data)
	return &fuzzSource{data: data, rest: rand.New(rand.NewSource(int64(h.Sum64())))}
}

func (s *fuzzSource) u32() uint32 {
	if len(s.data) >= 4 {
		v := uint32(s.data[0]) | uint32(s.data[1])<<8 | uint32(s.data[2])<<16 | uint32(s.data[3])<<24
		s.data = s.data[4:]
		return v
	}
	return s.rest.Uint32()
}

func (s *fuzzSource) intn(n int) int { return int(s.u32() % uint32(n)) }

// f32 mixes raw bit patterns (NaN, infinities, subnormals) with values in
// the ranges the codec uses.
func (s *fuzzSource) f32() float32 {
	v := s.u32()
	switch v & 3 {
	case 0:
		return math.Float32frombits(s.u32())
	case 1:
		return float32(int16(v>>16)) / 1024
	case 2:
		return []float32{0, float32(math.Copysign(0, -1)), 1e-40, -1e-40, 1e30, -1e30,
			float32(math.Inf(1)), float32(math.Inf(-1)), 32767.5, -32768.5}[(v>>2)%10]
	default:
		return float32(int32(v)) / (1 << 20)
	}
}

func (s *fuzzSource) i32() int32 {
	v := s.u32()
	switch v & 3 {
	case 0:
		return []int32{0, 1, -1, math.MaxInt32, math.MinInt32, 1 << 30, -1 << 30}[(v>>2)%7]
	case 1:
		return int32(int16(v >> 16))
	default:
		return int32(s.u32())
	}
}

func (s *fuzzSource) i16() int16 {
	v := s.u32()
	if v&1 == 0 {
		return []int16{0, 1, -1, math.MaxInt16, math.MinInt16}[(v>>1)%5]
	}
	return int16(v >> 16)
}

func (s *fuzzSource) f32s(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = s.f32()
	}
	return out
}

func FuzzKernelsFloat(f *testing.F) {
	for i := byte(0); i < 6; i++ {
		f.Add([]byte{i, 1, 2, 3, 4, 5, 6, 7})
		f.Add([]byte{i, 0xff, 0xff, 0x7f, 0x7f, 0, 0, 0x80, 0xff})
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		which := data[0] % 6
		withPaths(t, func(t *testing.T) {
			s := newFuzzSource(data[1:])
			switch which {
			case 0:
				fuzzXcorr(t, s)
			case 1:
				fuzzComb(t, s)
			case 2:
				fuzzFFT(t, s)
			case 3:
				fuzzIMDCT(t, s)
			case 4:
				fuzzPCM(t, s)
			case 5:
				fuzzConversions(t, s)
			}
		})
	})
}

func FuzzKernelsFixed(f *testing.F) {
	for i := byte(0); i < 5; i++ {
		f.Add([]byte{i, 1, 2, 3, 4, 5, 6, 7})
		f.Add([]byte{i, 0xff, 0xff, 0xff, 0x7f, 0, 0, 0, 0x80})
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		which := data[0] % 5
		withPaths(t, func(t *testing.T) {
			s := newFuzzSource(data[1:])
			switch which {
			case 0:
				fuzzFIR8(t, s)
			case 1:
				fuzzWarpedAR(t, s)
			case 2:
				fuzzLPC4(t, s)
			case 3:
				fuzzNSQCand(t, s)
			case 4:
				fuzzLPCAnalysis(t, s)
			}
		})
	})
}

func fuzzXcorr(t *testing.T, s *fuzzSource) {
	length := s.intn(400)
	maxPitch := 1 + s.intn(300)
	x := s.f32s(length)
	y := s.f32s(length + maxPitch - 1)
	got := make([]float32, maxPitch)
	want := make([]float32, maxPitch)
	PitchXcorr32(x, y, got, length, maxPitch)
	PitchXcorr32Generic(x, y, want, length, maxPitch)
	if i := sameSlices(got, want); i >= 0 {
		t.Fatalf("xcorr length %d maxPitch %d: lag %d got %v want %v", length, maxPitch, i, got[i], want[i])
	}
}

func fuzzComb(t *testing.T, s *fuzzSource) {
	p0 := 6 + s.intn(1020)
	p1 := 6 + s.intn(1020)
	start := max(p0, p1) + 2 + s.intn(3)
	n := s.intn(200)
	x := s.f32s(start + n + 3)
	var g CombGains
	for i := range g {
		g[i] = s.f32()
	}
	w := s.f32s(n + 1)
	a := append([]float32(nil), x...)
	b := append([]float32(nil), x...)
	if s.intn(2) == 0 {
		CombConst(a, start, n, p1, g[3], g[4], g[5])
		CombConstGeneric(b, start, n, p1, g[3], g[4], g[5])
	} else {
		CombOverlap(a, start, n, p0, p1, &g, w)
		CombOverlapGeneric(b, start, n, p0, p1, &g, w)
	}
	if i := sameSlices(a, b); i >= 0 {
		t.Fatalf("comb p0 %d p1 %d n %d: x[%d] got %v want %v", p0, p1, n, i, a[i], b[i])
	}
}

func fuzzFFT(t *testing.T, s *fuzzSource) {
	sh := fftShapes[s.intn(len(fftShapes))]
	p, m, groups, mm := sh[0], sh[1], sh[2], sh[3]
	tw := FFTTwiddleTable(p, m, func(q, j int) (float32, float32) { return s.f32(), s.f32() })
	a := s.f32s(2 * ((groups-1)*mm + p*m))
	b := append([]float32(nil), a...)
	ya := [2]float32{s.f32(), s.f32()}
	yb := [2]float32{s.f32(), s.f32()}
	switch p {
	case 3:
		FFTRadix3(a, tw, ya[1], m, groups, mm)
		FFTRadix3Generic(b, tw, ya[1], m, groups, mm)
	case 4:
		FFTRadix4(a, tw, m, groups, mm)
		FFTRadix4Generic(b, tw, m, groups, mm)
	case 5:
		FFTRadix5(a, tw, ya, yb, m, groups, mm)
		FFTRadix5Generic(b, tw, ya, yb, m, groups, mm)
	}
	if i := sameSlices(a, b); i >= 0 {
		t.Fatalf("fft radix %d m %d groups %d: float %d got %v want %v", p, m, groups, i, a[i], b[i])
	}
}

func fuzzIMDCT(t *testing.T, s *fuzzSource) {
	n4 := []int{60, 120, 240, 480, 4, 8, 12, 1 + s.intn(64)}[s.intn(8)]
	x := make([]float64, 2*n4)
	for i := range x {
		if s.intn(4) == 0 {
			x[i] = math.Float64frombits(uint64(s.u32())<<32 | uint64(s.u32()))
		} else {
			x[i] = float64(s.f32())
		}
	}
	trig := s.f32s(2 * n4)
	perm := make([]int, n4)
	for i := range perm {
		perm[i] = i
	}
	for i := n4 - 1; i > 0; i-- {
		j := s.intn(i + 1)
		perm[i], perm[j] = perm[j], perm[i]
	}
	a := make([]float32, 2*n4)
	b := make([]float32, 2*n4)
	IMDCTPreRotate(a, x, trig, NewBitRev(perm), n4)
	IMDCTPreRotateGeneric(b, x, trig, perm, n4)
	if i := sameSlices(a, b); i >= 0 {
		t.Fatalf("imdct pre n4 %d: float %d got %v want %v", n4, i, a[i], b[i])
	}
	fout := s.f32s(2 * n4)
	ba := make([]float64, 2*n4)
	bb := make([]float64, 2*n4)
	IMDCTPostRotate(ba, fout, trig, n4)
	IMDCTPostRotateGeneric(bb, fout, trig, n4)
	for i := range ba {
		if math.Float64bits(ba[i]) != math.Float64bits(bb[i]) && !(ba[i] != ba[i] && bb[i] != bb[i]) {
			t.Fatalf("imdct post n4 %d: buf %d got %v want %v", n4, i, ba[i], bb[i])
		}
	}
}

func fuzzPCM(t *testing.T, s *fuzzSource) {
	src := s.f32s(s.intn(100))
	got := make([]int16, len(src))
	want := make([]int16, len(src))
	Float32ToInt16(got, src)
	Float32ToInt16Generic(want, src)
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("pcm: sample %d (%v bits %#x) got %d want %d", i, src[i], math.Float32bits(src[i]), got[i], want[i])
		}
	}
}

func fuzzConversions(t *testing.T, s *fuzzSource) {
	n := s.intn(100)
	f64 := make([]float64, n)
	for i := range f64 {
		f64[i] = math.Float64frombits(uint64(s.u32())<<32 | uint64(s.u32()))
	}
	got32 := make([]float32, n)
	Float64sToFloat32s(got32, f64)
	for i, v := range f64 {
		if !sameBits(got32[i], float32(v)) {
			t.Fatalf("float64->float32: %d (%v) got %v want %v", i, v, got32[i], float32(v))
		}
	}
	f32 := s.f32s(n)
	got64 := make([]float64, n)
	Float32sToFloat64s(got64, f32)
	for i, v := range f32 {
		w := float64(v)
		if math.Float64bits(got64[i]) != math.Float64bits(w) && !(w != w && got64[i] != got64[i]) {
			t.Fatalf("float32->float64: %d (%v) got %v want %v", i, v, got64[i], w)
		}
	}
}

func fuzzFIR8(t *testing.T, s *fuzzSource) {
	var table FIR8Table
	for i := range table {
		for k := range table[i] {
			table[i][k] = s.i16()
		}
	}
	idxInc := int32(1 + s.intn(1<<17))
	n := s.intn(160)
	last := int64(n) * int64(idxInc)
	buf := make([]int16, int(last>>16)+9)
	for i := range buf {
		buf[i] = s.i16()
	}
	got := make([]int16, n)
	want := make([]int16, n)
	done := FIR8Interp(got, buf, &table, 0, idxInc)
	fir8Reference(want, buf, &table, 0, idxInc)
	for i := 0; i < done; i++ {
		if got[i] != want[i] {
			t.Fatalf("fir8 inc %d n %d: output %d got %d want %d", idxInc, n, i, got[i], want[i])
		}
	}
}

func fuzzWarpedAR(t *testing.T, s *fuzzSource) {
	order := 2 * (1 + s.intn(MaxShapeOrder/2))
	ar := make([]int16, order)
	for i := range ar {
		ar[i] = s.i16()
	}
	var a WarpedAR4
	a.SetCoefficients(ar, order, s.i16(), s.i16(), 1+s.intn(4))
	for j := range a.S {
		for k := range a.S[j] {
			a.S[j][k] = s.i32()
		}
	}
	b := a
	for step := 0; step < 8; step++ {
		var diff, lf, got, want [4]int32
		for k := range diff {
			diff[k], lf[k] = s.i32(), s.i32()
		}
		a.Step(&diff, &lf, &got)
		b.StepGeneric(&diff, &lf, &want)
		if a.Lanes == 2 {
			got[2], got[3], want[2], want[3] = 0, 0, 0, 0
		}
		if got != want || a.S != b.S {
			t.Fatalf("warped AR order %d step %d: got %v want %v (state equal %v)", order, step, got, want, a.S == b.S)
		}
	}
}

func fuzzLPC4(t *testing.T, s *fuzzSource) {
	order := 1 + s.intn(16)
	coef := make([]int16, order)
	for i := range coef {
		coef[i] = s.i16()
	}
	var p LPC4
	p.SetCoefficients(coef, order, 1+s.intn(4))
	for row := range p.H {
		for k := range p.H[row] {
			p.H[row][k] = s.i32()
		}
	}
	i := s.intn(LPCRows - 15)
	var got, want [4]int32
	p.Predict(i, &got)
	p.PredictGeneric(i, &want)
	if p.Lanes == 2 {
		got[2], got[3], want[2], want[3] = 0, 0, 0, 0
	}
	if got != want {
		t.Fatalf("lpc4 order %d row %d: got %v want %v", order, i, got, want)
	}
}

func fuzzLPCAnalysis(t *testing.T, s *fuzzSource) {
	order := 1 + s.intn(16)
	length := s.intn(lpcAnalysisMaxLen + 40)
	in := make([]int32, length)
	for i := range in {
		in[i] = s.i32()
	}
	b := make([]int16, order)
	for i := range b {
		b[i] = s.i16()
	}
	got := make([]int16, length)
	want := make([]int16, length)
	LPCAnalysisFilter(got, in, b, length, order)
	LPCAnalysisFilterGeneric(want, in, b, length, order)
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("lpc analysis order %d length %d: out[%d] got %d want %d", order, length, i, got[i], want[i])
		}
	}
}

func fuzzNSQCand(t *testing.T, s *fuzzSource) {
	var c NSQCand4
	c.SetSubframe(s.i32(), s.i32(), s.i32())
	c.SetSample(s.i32(), s.i32(), s.i32())
	for k := 0; k < 4; k++ {
		c.Seed[k] = int32(s.u32())
		c.NAR[k] = s.i32()
		c.LPCPred[k] = s.i32()
		c.Shape[k] = s.i32()
		c.LFAR[k] = s.i32()
		c.RD[k] = s.i32()
	}
	d := c
	c.Step()
	d.StepGeneric()
	if c != d {
		t.Fatalf("nsq candidates:\n got  %+v\n want %+v", c, d)
	}
}
