package kernels

// The KISS FFT radix stages of the float libopus build, on interleaved
// complex float32 data (re, im, re, im, ...). Within a stage the m
// butterflies of a group are independent, so the vector versions run four
// consecutive butterflies at once, one per lane, with the scalar operation
// order: every product is rounded before it is added, and there is no FMA.
//
// Twiddles are read from a per-stage table built by FFTTwiddleTable, laid
// out in blocks of four butterflies so the kernels read them contiguously.

// FFTTwiddleTable builds the stage table for radix p (3, 4 or 5) with m
// butterflies per group: for each block of four j, the p-1 twiddles
// tw(q*j) for q = 1..p-1, real parts then imaginary parts. twr and twi
// return the real and imaginary part of twiddle index j*q.
func FFTTwiddleTable(p, m int, tw func(q, j int) (re, im float32)) []float32 {
	if m%4 != 0 {
		panic("kernels: FFT stage needs m divisible by 4")
	}
	t := make([]float32, 0, 2*(p-1)*m)
	for j0 := 0; j0 < m; j0 += 4 {
		for q := 1; q < p; q++ {
			var re, im [4]float32
			for l := 0; l < 4; l++ {
				re[l], im[l] = tw(q, j0+l)
			}
			t = append(t, re[:]...)
			t = append(t, im[:]...)
		}
	}
	return t
}

// HasFFT reports whether the FFT stages have vector versions on this CPU.
func HasFFT() bool { return hasFFT() }

func fftCheck(f, t []float32, p, m, groups, mm int) {
	if m <= 0 || m%4 != 0 || groups <= 0 {
		panic("kernels: bad FFT stage shape")
	}
	if len(t) < 2*(p-1)*m {
		panic("kernels: short twiddle table")
	}
	_ = f[2*((groups-1)*mm+p*m)-1]
}

// FFTRadix4 runs a radix-4 stage (kf_bfly4 with m > 1).
func FFTRadix4(f, t []float32, m, groups, mm int) {
	fftCheck(f, t, 4, m, groups, mm)
	if fftRadix4Arch(f, t, m, groups, mm) {
		return
	}
	FFTRadix4Generic(f, t, m, groups, mm)
}

// FFTRadix3 runs a radix-3 stage (kf_bfly3); epi is the imaginary part of
// twiddle fstride*m.
func FFTRadix3(f, t []float32, epi float32, m, groups, mm int) {
	fftCheck(f, t, 3, m, groups, mm)
	if fftRadix3Arch(f, t, epi, m, groups, mm) {
		return
	}
	FFTRadix3Generic(f, t, epi, m, groups, mm)
}

// FFTRadix5 runs a radix-5 stage (kf_bfly5); ya and yb are twiddles
// fstride*m and fstride*2*m.
func FFTRadix5(f, t []float32, ya, yb [2]float32, m, groups, mm int) {
	fftCheck(f, t, 5, m, groups, mm)
	if fftRadix5Arch(f, t, ya, yb, m, groups, mm) {
		return
	}
	FFTRadix5Generic(f, t, ya, yb, m, groups, mm)
}

type cpx struct{ r, i float32 }

func cmul(a, b cpx) cpx {
	arbr := float32(a.r * b.r)
	aibi := float32(a.i * b.i)
	arbi := float32(a.r * b.i)
	aibr := float32(a.i * b.r)
	return cpx{arbr - aibi, arbi + aibr}
}

func cadd(a, b cpx) cpx { return cpx{a.r + b.r, a.i + b.i} }
func csub(a, b cpx) cpx { return cpx{a.r - b.r, a.i - b.i} }

func ld(f []float32, k int) cpx    { return cpx{f[2*k], f[2*k+1]} }
func st(f []float32, k int, v cpx) { f[2*k], f[2*k+1] = v.r, v.i }

// twAt reads twiddle q (1-based) of butterfly j from a stage table.
func twAt(t []float32, p, q, j int) cpx {
	base := (j/4)*8*(p-1) + (q-1)*8 + j%4
	return cpx{t[base], t[base+4]}
}

// FFTRadix4Generic is the pure Go reference for FFTRadix4.
func FFTRadix4Generic(f, t []float32, m, groups, mm int) {
	for g := 0; g < groups; g++ {
		base := g * mm
		for j := 0; j < m; j++ {
			i0 := base + j
			s0 := cmul(ld(f, i0+m), twAt(t, 4, 1, j))
			s1 := cmul(ld(f, i0+2*m), twAt(t, 4, 2, j))
			s2 := cmul(ld(f, i0+3*m), twAt(t, 4, 3, j))
			f0 := ld(f, i0)
			s5 := csub(f0, s1)
			center := cadd(f0, s1)
			s3 := cadd(s0, s2)
			s4 := csub(s0, s2)
			st(f, i0, cadd(center, s3))
			st(f, i0+2*m, csub(center, s3))
			st(f, i0+m, cpx{s5.r + s4.i, s5.i - s4.r})
			st(f, i0+3*m, cpx{s5.r - s4.i, s5.i + s4.r})
		}
	}
}

// FFTRadix3Generic is the pure Go reference for FFTRadix3.
func FFTRadix3Generic(f, t []float32, epi float32, m, groups, mm int) {
	for g := 0; g < groups; g++ {
		base := g * mm
		for j := 0; j < m; j++ {
			i0 := base + j
			s1 := cmul(ld(f, i0+m), twAt(t, 3, 1, j))
			s2 := cmul(ld(f, i0+2*m), twAt(t, 3, 2, j))
			s3 := cadd(s1, s2)
			s0 := csub(s1, s2)
			f0 := ld(f, i0)
			mid := cpx{f0.r - float32(0.5*s3.r), f0.i - float32(0.5*s3.i)}
			s0 = cpx{float32(s0.r * epi), float32(s0.i * epi)}
			st(f, i0, cadd(f0, s3))
			st(f, i0+2*m, cpx{mid.r + s0.i, mid.i - s0.r})
			st(f, i0+m, cpx{mid.r - s0.i, mid.i + s0.r})
		}
	}
}

// FFTRadix5Generic is the pure Go reference for FFTRadix5.
func FFTRadix5Generic(f, t []float32, yaa, ybb [2]float32, m, groups, mm int) {
	ya, yb := cpx{yaa[0], yaa[1]}, cpx{ybb[0], ybb[1]}
	for g := 0; g < groups; g++ {
		base := g * mm
		for j := 0; j < m; j++ {
			i0 := base + j
			s0 := ld(f, i0)
			s1 := cmul(ld(f, i0+m), twAt(t, 5, 1, j))
			s2 := cmul(ld(f, i0+2*m), twAt(t, 5, 2, j))
			s3 := cmul(ld(f, i0+3*m), twAt(t, 5, 3, j))
			s4 := cmul(ld(f, i0+4*m), twAt(t, 5, 4, j))
			s7, s10 := cadd(s1, s4), csub(s1, s4)
			s8, s9 := cadd(s2, s3), csub(s2, s3)
			st(f, i0, cadd(s0, cadd(s7, s8)))
			s5 := cpx{
				s0.r + (float32(s7.r*ya.r) + float32(s8.r*yb.r)),
				s0.i + (float32(s7.i*ya.r) + float32(s8.i*yb.r)),
			}
			s6 := cpx{
				float32(s10.i*ya.i) + float32(s9.i*yb.i),
				-(float32(s10.r*ya.i) + float32(s9.r*yb.i)),
			}
			st(f, i0+m, csub(s5, s6))
			st(f, i0+4*m, cadd(s5, s6))
			s11 := cpx{
				s0.r + (float32(s7.r*yb.r) + float32(s8.r*ya.r)),
				s0.i + (float32(s7.i*yb.r) + float32(s8.i*ya.r)),
			}
			s12 := cpx{
				float32(s9.i*ya.i) - float32(s10.i*yb.i),
				float32(s10.r*yb.i) - float32(s9.r*ya.i),
			}
			st(f, i0+2*m, cadd(s11, s12))
			st(f, i0+3*m, csub(s11, s12))
		}
	}
}
