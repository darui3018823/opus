package dsp

import "math"

// opusComplex is the scalar type used by the floating-point libopus KISS FFT.
type opusComplex struct {
	r float32
	i float32
}

func opusCAdd(a, b opusComplex) opusComplex {
	return opusComplex{r: a.r + b.r, i: a.i + b.i}
}

func opusCSub(a, b opusComplex) opusComplex {
	return opusComplex{r: a.r - b.r, i: a.i - b.i}
}

func opusCMul(a, b opusComplex) opusComplex {
	arbr := a.r * b.r
	aibi := a.i * b.i
	arbi := a.r * b.i
	aibr := a.i * b.r
	return opusComplex{r: arbr - aibi, i: arbi + aibr}
}

type opusFFTPlan struct {
	n        int
	shift    int
	factors  []int
	bitrev   []int
	twiddles []opusComplex
}

// opusFFT computes the unscaled DFT using the float KISS FFT stage ordering
// used by the static 48 kHz CELT mode. CELT's MDCT uses only these four sizes.
func opusFFT(input []Complex) []Complex {
	n := len(input)
	if n != 60 && n != 120 && n != 240 && n != 480 {
		return AnyFFT(input)
	}
	plan := newOpusFFTPlan(n)
	fout := make([]opusComplex, n)
	for i, value := range input {
		fout[plan.bitrev[i]] = opusComplex{r: float32(value.Real), i: float32(value.Imag)}
	}
	plan.transform(fout)
	out := make([]Complex, n)
	for i, value := range fout {
		out[i] = Complex{Real: float64(value.r), Imag: float64(value.i)}
	}
	return out
}

func newOpusFFTPlan(n int) opusFFTPlan {
	plan := opusFFTPlan{n: n}
	for scaled := n; scaled < 480; scaled <<= 1 {
		plan.shift++
	}
	plan.factors = opusFFTFactors(n)
	plan.bitrev = make([]int, n)
	var buildBitrev func(fout, offset, stride, stage int)
	buildBitrev = func(fout, offset, stride, stage int) {
		p := plan.factors[2*stage]
		m := plan.factors[2*stage+1]
		if m == 1 {
			for j := 0; j < p; j++ {
				plan.bitrev[offset+j*stride] = fout + j
			}
			return
		}
		for j := 0; j < p; j++ {
			buildBitrev(fout, offset, stride*p, stage+1)
			offset += stride
			fout += m
		}
	}
	buildBitrev(0, 0, 1, 0)

	plan.twiddles = make([]opusComplex, 480)
	for i := range plan.twiddles {
		phase := (-2 * math.Pi / 480) * float64(i)
		plan.twiddles[i] = opusComplex{r: float32(math.Cos(phase)), i: float32(math.Sin(phase))}
	}
	return plan
}

func opusFFTFactors(n int) []int {
	p := 4
	var radices []int
	remaining := n
	for remaining > 1 {
		for remaining%p != 0 {
			switch p {
			case 4:
				p = 2
			case 2:
				p = 3
			default:
				p += 2
			}
			if p*p > remaining {
				p = remaining
			}
		}
		remaining /= p
		radices = append(radices, p)
		if p == 2 && len(radices) > 2 {
			radices[len(radices)-1] = 4
			radices[1] = 2
		}
	}
	for i, j := 0, len(radices)-1; i < j; i, j = i+1, j-1 {
		radices[i], radices[j] = radices[j], radices[i]
	}
	factors := make([]int, 0, 2*len(radices))
	remaining = n
	for _, radix := range radices {
		remaining /= radix
		factors = append(factors, radix, remaining)
	}
	return factors
}

func (p opusFFTPlan) transform(fout []opusComplex) {
	stages := len(p.factors) / 2
	fstride := make([]int, stages+1)
	fstride[0] = 1
	for i := 0; i < stages; i++ {
		fstride[i+1] = fstride[i] * p.factors[2*i]
	}
	m := p.factors[2*stages-1]
	for i := stages - 1; i >= 0; i-- {
		m2 := 1
		if i != 0 {
			m2 = p.factors[2*i-1]
		}
		switch p.factors[2*i] {
		case 2:
			opusBfly2(fout, m, fstride[i])
		case 3:
			p.bfly3(fout, fstride[i]<<p.shift, m, fstride[i], m2)
		case 4:
			p.bfly4(fout, fstride[i]<<p.shift, m, fstride[i], m2)
		case 5:
			p.bfly5(fout, fstride[i]<<p.shift, m, fstride[i], m2)
		}
		m = m2
	}
}

func opusBfly2(fout []opusComplex, m, groups int) {
	if m != 4 {
		panic("opus FFT radix-2 stage requires m=4")
	}
	const tw = float32(0.7071067812)
	for group := 0; group < groups; group++ {
		base := group * 8
		for j := 0; j < 4; j++ {
			a := fout[base+j]
			b := fout[base+4+j]
			var t opusComplex
			switch j {
			case 0:
				t = b
			case 1:
				t = opusComplex{r: (b.r + b.i) * tw, i: (b.i - b.r) * tw}
			case 2:
				t = opusComplex{r: b.i, i: -b.r}
			case 3:
				t = opusComplex{r: (b.i - b.r) * tw, i: -(b.i + b.r) * tw}
			}
			fout[base+j] = opusCAdd(a, t)
			fout[base+4+j] = opusCSub(a, t)
		}
	}
}

func (p opusFFTPlan) bfly4(fout []opusComplex, fstride, m, groups, mm int) {
	if m == 1 {
		for group := 0; group < groups; group++ {
			base := group * 4
			a0, a1, a2, a3 := fout[base], fout[base+1], fout[base+2], fout[base+3]
			s0 := opusCSub(a0, a2)
			a0 = opusCAdd(a0, a2)
			s1 := opusCAdd(a1, a3)
			fout[base] = opusCAdd(a0, s1)
			fout[base+2] = opusCSub(a0, s1)
			s1 = opusCSub(a1, a3)
			fout[base+1] = opusComplex{r: s0.r + s1.i, i: s0.i - s1.r}
			fout[base+3] = opusComplex{r: s0.r - s1.i, i: s0.i + s1.r}
		}
		return
	}
	for group := 0; group < groups; group++ {
		base := group * mm
		for j := 0; j < m; j++ {
			i0 := base + j
			s0 := opusCMul(fout[i0+m], p.twiddles[j*fstride])
			s1 := opusCMul(fout[i0+2*m], p.twiddles[j*fstride*2])
			s2 := opusCMul(fout[i0+3*m], p.twiddles[j*fstride*3])
			s5 := opusCSub(fout[i0], s1)
			center := opusCAdd(fout[i0], s1)
			s3 := opusCAdd(s0, s2)
			s4 := opusCSub(s0, s2)
			fout[i0] = opusCAdd(center, s3)
			fout[i0+2*m] = opusCSub(center, s3)
			fout[i0+m] = opusComplex{r: s5.r + s4.i, i: s5.i - s4.r}
			fout[i0+3*m] = opusComplex{r: s5.r - s4.i, i: s5.i + s4.r}
		}
	}
}

func (p opusFFTPlan) bfly3(fout []opusComplex, fstride, m, groups, mm int) {
	epi := p.twiddles[fstride*m].i
	for group := 0; group < groups; group++ {
		base := group * mm
		for j := 0; j < m; j++ {
			i0 := base + j
			s1 := opusCMul(fout[i0+m], p.twiddles[j*fstride])
			s2 := opusCMul(fout[i0+2*m], p.twiddles[j*fstride*2])
			s3 := opusCAdd(s1, s2)
			s0 := opusCSub(s1, s2)
			mid := opusComplex{r: fout[i0].r - 0.5*s3.r, i: fout[i0].i - 0.5*s3.i}
			s0 = opusComplex{r: s0.r * epi, i: s0.i * epi}
			fout[i0] = opusCAdd(fout[i0], s3)
			fout[i0+2*m] = opusComplex{r: mid.r + s0.i, i: mid.i - s0.r}
			fout[i0+m] = opusComplex{r: mid.r - s0.i, i: mid.i + s0.r}
		}
	}
}

func (p opusFFTPlan) bfly5(fout []opusComplex, fstride, m, groups, mm int) {
	ya := p.twiddles[fstride*m]
	yb := p.twiddles[fstride*2*m]
	for group := 0; group < groups; group++ {
		base := group * mm
		for j := 0; j < m; j++ {
			i0 := base + j
			s0 := fout[i0]
			s1 := opusCMul(fout[i0+m], p.twiddles[j*fstride])
			s2 := opusCMul(fout[i0+2*m], p.twiddles[j*fstride*2])
			s3 := opusCMul(fout[i0+3*m], p.twiddles[j*fstride*3])
			s4 := opusCMul(fout[i0+4*m], p.twiddles[j*fstride*4])
			s7, s10 := opusCAdd(s1, s4), opusCSub(s1, s4)
			s8, s9 := opusCAdd(s2, s3), opusCSub(s2, s3)
			fout[i0] = opusCAdd(s0, opusCAdd(s7, s8))
			s5 := opusComplex{
				r: s0.r + (s7.r*ya.r + s8.r*yb.r),
				i: s0.i + (s7.i*ya.r + s8.i*yb.r),
			}
			s6 := opusComplex{
				r: s10.i*ya.i + s9.i*yb.i,
				i: -(s10.r*ya.i + s9.r*yb.i),
			}
			fout[i0+m] = opusCSub(s5, s6)
			fout[i0+4*m] = opusCAdd(s5, s6)
			s11 := opusComplex{
				r: s0.r + (s7.r*yb.r + s8.r*ya.r),
				i: s0.i + (s7.i*yb.r + s8.i*ya.r),
			}
			s12 := opusComplex{
				r: s9.i*ya.i - s10.i*yb.i,
				i: s10.r*yb.i - s9.r*ya.i,
			}
			fout[i0+2*m] = opusCAdd(s11, s12)
			fout[i0+3*m] = opusCSub(s11, s12)
		}
	}
}
