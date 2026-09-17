package celt

import (
	"fmt"
	"math"
	"os"

	"github.com/darui3018823/opus/internal/entcode"
)

// This file is a faithful Go port of the libopus CELT band-quantization decoder
// path (celt/bands.c quant_all_bands / quant_band / quant_partition / compute_theta
// and celt/vq.c alg_unquant), float build, decoder-only (encode=0, resynth=1).
//
// celt_norm is float64. Q15 fixed-point scaling collapses to plain float multiply
// in the float build, so MULT16_16*/PSHR32/EXTRACT16 etc. become direct arithmetic.

const (
	bitres               = 3 // libopus BITRES (1<<bitres == 8)
	spreadNone           = 0
	spreadAggressive     = 3
	qThetaOffset         = 4
	qThetaOffsetTwoPhase = 16
	logMaxPseudo         = 6
)

// qabDebug enables per-band trace capture in QuantAllBands (test diagnostics).
var qabDebug = false

// qabRangeTrace prints per-band tellFrac/rng for both encode and decode so the
// exact symbol where the two desync can be located.
var qabRangeTrace = os.Getenv("OPUS_QAB_TRACE") != ""

// qabTellTrace, when non-nil, receives ec_tell_frac after each band the
// encoder codes (the oracle's [CELT_ENC_QAB] dump).
var qabTellTrace *[]int

type qabBandTrace struct {
	i, N, b, tellf int
	rng            uint32
	xcm            uint
}

var qabLog []qabBandTrace

// qabDP records decodePulses (n,k,V,idx,difBefore,difAfter,tellBefore,tellAfter) calls when qabDebug is set.
var qabDP [][8]uint64

var qabTheta [][6]int

// celtLCGRand matches libopus celt_lcg_rand (celt.c).
func celtLCGRand(seed uint32) uint32 { return 1664525*seed + 1013904223 }

// fracMul16 matches FRAC_MUL16(a,b) = (16384 + a*b) >> 15 with int16 operands.
func fracMul16(a, b int) int {
	return (16384 + int(int16(a))*int(int16(b))) >> 15
}

// bitexactCos matches libopus bitexact_cos (entcode.c / bands.c).
func bitexactCos(x int16) int {
	tmp := (4096 + int(x)*int(x)) >> 13
	x2 := tmp
	x2 = (32767 - x2) + fracMul16(x2, -7651+fracMul16(x2, 8277+fracMul16(-626, x2)))
	return 1 + x2
}

// bitexactLog2Tan matches libopus bitexact_log2tan (bands.c).
func bitexactLog2Tan(isin, icos int) int {
	lc := entcode.ILog(uint32(icos))
	ls := entcode.ILog(uint32(isin))
	icos <<= uint(15 - lc)
	isin <<= uint(15 - ls)
	return (ls-lc)*(1<<11) +
		fracMul16(isin, fracMul16(isin, -2597)+7932) -
		fracMul16(icos, fracMul16(icos, -2597)+7932)
}

// isqrt32 matches libopus isqrt32 (mathops.c).
func isqrt32(val uint32) int {
	g := uint32(0)
	bshift := (entcode.ILog(val) - 1) >> 1
	b := uint32(1) << uint(bshift)
	for {
		t := ((g << 1) + b) << uint(bshift)
		if t <= val {
			g += b
			val -= t
		}
		b >>= 1
		bshift--
		if bshift < 0 {
			break
		}
	}
	return int(g)
}

// celtCosNorm matches the float build's celt_cos_norm macro. The argument and
// result are celt_norm/opus_val16 float values even though cos itself operates
// in double precision in C.
func celtCosNorm(x float32) float32 {
	return float32(math.Cos((0.5 * math.Pi) * float64(x)))
}

func mulFloat32(a, b float64) float64 {
	return float64(float32(a) * float32(b))
}

// getPulses matches libopus get_pulses (rate.h).
func getPulses(i int) int {
	if i < 8 {
		return i
	}
	return (8 + (i & 7)) << uint((i>>3)-1)
}

// cacheSlice returns the pulse-cost cache row for (lm, band), or nil.
func cacheSlice(lm, band int) []uint8 {
	idx := (lm+1)*NumBands48000 + band
	if idx < 0 || idx >= len(CacheIndex50) {
		return nil
	}
	start := int(CacheIndex50[idx])
	if start < 0 || start >= len(CacheBits50) {
		return nil
	}
	return CacheBits50[start:]
}

// ---- vq.c (float) ----

func decodePulses(dec *entcode.Decoder, n, k int) ([]int, float64) {
	v := cwrsV(n, k)
	ft := uint32(v)
	if v > uint64(0xFFFFFFFF) {
		ft = 0xFFFFFFFF
	}
	tb := 0
	db := uint32(0)
	if qabDebug {
		tb = dec.TellFrac()
		db = dec.GetDif()
	}
	idx := dec.DecodeUint(ft)
	if qabDebug {
		qabDP = append(qabDP, [8]uint64{uint64(n), uint64(k), v, uint64(idx), uint64(db), uint64(dec.GetDif()), uint64(tb), uint64(dec.TellFrac())})
	}
	iy := cwrsiLibopus(n, k, idx)
	ryy := 0.0
	for _, y := range iy {
		ryy += float64(y * y)
	}
	return iy, ryy
}

func normaliseResidual(iy []int, X []float64, n int, ryy, gain float64) {
	g := float32(gain)
	if ryy > 0 {
		sqrtRyy := float32(math.Sqrt(float64(float32(ryy))))
		g = (float32(1.0) / sqrtRyy) * float32(gain)
	}
	for i := 0; i < n; i++ {
		X[i] = float64(g * float32(iy[i]))
	}
}

func expRotation1(X []float64, length, stride int, c, s float32) {
	ms := -s
	// forward
	for i := 0; i < length-stride; i++ {
		x1 := float32(X[i])
		x2 := float32(X[i+stride])
		X[i+stride] = float64(c*x2 + s*x1)
		X[i] = float64(c*x1 + ms*x2)
	}
	// backward
	for i := length - 2*stride - 1; i >= 0; i-- {
		x1 := float32(X[i])
		x2 := float32(X[i+stride])
		X[i+stride] = float64(c*x2 + s*x1)
		X[i] = float64(c*x1 + ms*x2)
	}
}

func expRotation(X []float64, length, dir, stride, k, spread int) {
	spreadFactor := [3]int{15, 10, 5}
	if 2*k >= length || spread == spreadNone {
		return
	}
	factor := spreadFactor[spread-1]
	gain := float32(length) / float32(length+factor*k)
	theta := float32(0.5) * gain * gain
	c := celtCosNorm(theta)
	s := celtCosNorm(float32(1.0) - theta) // sin(theta*pi/2)
	stride2 := 0
	if length >= 8*stride {
		stride2 = 1
		for (stride2*stride2+stride2)*stride+(stride>>2) < length {
			stride2++
		}
	}
	ln := length / stride
	for i := 0; i < stride; i++ {
		seg := X[i*ln:]
		if dir < 0 {
			if stride2 != 0 {
				expRotation1(seg, ln, stride2, s, c)
			}
			expRotation1(seg, ln, 1, c, s)
		} else {
			expRotation1(seg, ln, 1, c, -s)
			if stride2 != 0 {
				expRotation1(seg, ln, stride2, s, -c)
			}
		}
	}
}

func extractCollapseMask(iy []int, n, b int) uint {
	if b <= 1 {
		return 1
	}
	n0 := n / b
	var mask uint
	for i := 0; i < b; i++ {
		tmp := 0
		for j := 0; j < n0; j++ {
			tmp |= iy[i*n0+j]
		}
		if tmp != 0 {
			mask |= 1 << uint(i)
		}
	}
	return mask
}

func algUnquant(X []float64, n, k, spread, b int, dec *entcode.Decoder, gain float64) uint {
	iy, ryy := decodePulses(dec, n, k)
	normaliseResidual(iy, X, n, ryy, gain)
	expRotation(X, n, -1, b, k, spread)
	return extractCollapseMask(iy, n, b)
}

func renormaliseVector(X []float64, n int, gain float64) {
	e := float32(1e-15)
	for i := 0; i < n; i++ {
		x := float32(X[i])
		e += x * x
	}
	g := (float32(1.0) / float32(math.Sqrt(float64(e)))) * float32(gain)
	for i := 0; i < n; i++ {
		X[i] = float64(g * float32(X[i]))
	}
}

// haar1 matches libopus haar1 (bands.c), float build.
func haar1(X []float64, n0, stride int) {
	n0 >>= 1
	const s = float32(0.70710678)
	for i := 0; i < stride; i++ {
		for j := 0; j < n0; j++ {
			t1 := s * float32(X[stride*2*j+i])
			t2 := s * float32(X[stride*(2*j+1)+i])
			X[stride*2*j+i] = float64(t1 + t2)
			X[stride*(2*j+1)+i] = float64(t1 - t2)
		}
	}
}

var orderyTable = []int{
	1, 0,
	3, 0, 2, 1,
	7, 0, 4, 3, 6, 1, 5, 2,
	15, 0, 8, 7, 12, 3, 11, 4, 14, 1, 9, 6, 13, 2, 10, 5,
}

func deinterleaveHadamard(X []float64, n0, stride, hadamard int) {
	n := n0 * stride
	tmp := make([]float64, n)
	if hadamard != 0 {
		ordery := orderyTable[stride-2:]
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[ordery[i]*n0+j] = X[j*stride+i]
			}
		}
	} else {
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[i*n0+j] = X[j*stride+i]
			}
		}
	}
	copy(X[:n], tmp)
}

func interleaveHadamard(X []float64, n0, stride, hadamard int) {
	n := n0 * stride
	tmp := make([]float64, n)
	if hadamard != 0 {
		ordery := orderyTable[stride-2:]
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[j*stride+i] = X[ordery[i]*n0+j]
			}
		}
	} else {
		for i := 0; i < stride; i++ {
			for j := 0; j < n0; j++ {
				tmp[j*stride+i] = X[i*n0+j]
			}
		}
	}
	copy(X[:n], tmp)
}

// ---- band context ----

type bandCtx struct {
	m             *Mode
	i             int
	intensity     int
	spread        int
	tfChange      int
	dec           *entcode.Decoder
	enc           *entcode.Encoder // non-nil when encode==true
	encode        bool
	nbEBands      int       // for second-channel bandE indexing (stereo encode)
	bandE         []float64 // band energies (channel-major), encode-side only
	thetaRound    int
	remainingBits int
	seed          uint32
	arch          int
	disableInv    bool
	avoidSplit    bool
}

// tellFrac returns ec_tell_frac on whichever entropy coder is active.
func (ctx *bandCtx) tellFrac() int {
	if ctx.encode {
		return ctx.enc.TellFrac()
	}
	return ctx.dec.TellFrac()
}

// celtAtanNorm is libopus celt_atan_norm (float build): a Remez
// approximation of 2/pi*atan(x) for x in [0, 1], evaluated in float32.
func celtAtanNorm(x float32) float32 {
	const (
		a03 = float32(-3.3331659436225891113281250000e-01)
		a05 = float32(1.99627041816711425781250000000e-01)
		a07 = float32(-1.3976582884788513183593750000e-01)
		a09 = float32(9.79423448443412780761718750000e-02)
		a11 = float32(-5.7773590087890625000000000000e-02)
		a13 = float32(2.30401363223791122436523437500e-02)
		a15 = float32(-4.3554059229791164398193359375e-03)
	)
	xSq := x * x
	p := a13 + xSq*a15
	p = a11 + xSq*p
	p = a09 + xSq*p
	p = a07 + xSq*p
	p = a05 + xSq*p
	p = a03 + xSq*p
	return float32(0.636619772367581) * (x + x*xSq*p)
}

// celtAtan2pNorm is libopus celt_atan2p_norm (float build): atan2(y, x)
// normalised to [0, 1] for non-negative arguments.
func celtAtan2pNorm(y, x float32) float32 {
	if x*x+y*y < 1e-18 {
		return 0
	}
	if y < x {
		return celtAtanNorm(y / x)
	}
	return 1 - celtAtanNorm(x/y)
}

// stereoIthetaF is the float32 port of libopus stereo_itheta (vq.c),
// returning the pre-quantization angle in [0,16384] (stereo_itheta's Q30
// value shifted down by 16). For a mono split (stereo=false) X and Y are
// the two halves; for a stereo band they are the L/R channels.
func stereoIthetaF(X, Y []float64, stereo bool, n int) int {
	var Emid, Eside float32
	if stereo {
		for i := 0; i < n; i++ {
			m := float32(X[i]) + float32(Y[i])
			s := float32(X[i]) - float32(Y[i])
			Emid += float32(m * m)
			Eside += float32(s * s)
		}
	} else {
		for i := 0; i < n; i++ {
			x := float32(X[i])
			Emid += float32(x * x)
		}
		for i := 0; i < n; i++ {
			y := float32(Y[i])
			Eside += float32(y * y)
		}
	}
	mid := float32(math.Sqrt(float64(Emid)))
	side := float32(math.Sqrt(float64(Eside)))
	ithetaQ30 := int32(math.Floor(float64(float32(0.5) + float32(65536.0*16384)*celtAtan2pNorm(side, mid))))
	return int(ithetaQ30 >> 16)
}

// intensityStereo collapses Y into X using the band's stereo energy ratio
// (libopus bands.c intensity_stereo, float build).
func intensityStereo(ctx *bandCtx, X, Y []float64, n int) {
	left := float32(ctx.bandE[ctx.i])
	right := float32(ctx.bandE[ctx.nbEBands+ctx.i])
	norm := float32(1e-15) + float32(math.Sqrt(float64(float32(float32(1e-15)+float32(left*left))+float32(right*right))))
	a1 := left / norm
	a2 := right / norm
	for j := 0; j < n; j++ {
		l := float32(X[j])
		r := float32(Y[j])
		X[j] = float64(float32(a1*l) + float32(a2*r))
	}
}

// stereoSplit rotates (X,Y) into the orthonormal mid/side basis
// (libopus bands.c stereo_split, float build).
func stereoSplit(X, Y []float64, n int) {
	const c = float32(0.70710678)
	for j := 0; j < n; j++ {
		l := c * float32(X[j])
		r := c * float32(Y[j])
		X[j] = float64(l + r)
		Y[j] = float64(r - l)
	}
}

type splitResult struct {
	inv, imid, iside, delta, itheta, qalloc int
}

// computeTheta — shared encode/decode port of libopus compute_theta (bands.c).
// When ctx.encode is set, the angle is derived from the signal (X,Y) and written
// to ctx.enc; otherwise it is read from ctx.dec. The surrounding budget
// accounting (qn, qalloc, *b) is identical in both directions.
func computeTheta(ctx *bandCtx, X, Y []float64, n int, b *int, B, B0, lm int, stereo bool, fill *int) splitResult {
	i := ctx.i
	encode := ctx.encode
	pulseCap := int(LogN400[i]) + lm*(1<<bitres)
	off := pulseCap >> 1
	if stereo && n == 2 {
		off -= qThetaOffsetTwoPhase
	} else {
		off -= qThetaOffset
	}
	qn := computeQn(n, *b, off, pulseCap, stereo)
	if stereo && i >= ctx.intensity {
		qn = 1
	}

	// Encoder: pick the raw angle (0..16384) from the signal energies.
	ithetaRaw := 0
	if encode {
		ithetaRaw = stereoIthetaF(X, Y, stereo, n)
	}

	tell := ctx.tellFrac()
	itheta := 0
	inv := 0
	if qn != 1 {
		if encode {
			if !stereo || ctx.thetaRound == 0 {
				itheta = (ithetaRaw*qn + 8192) >> 14
				if !stereo && ctx.avoidSplit && itheta > 0 && itheta < qn {
					unquantized := itheta * 16384 / qn
					imid := bitexactCos(int16(unquantized))
					iside := bitexactCos(int16(16384 - unquantized))
					delta := fracMul16((n-1)<<7, bitexactLog2Tan(iside, imid))
					if delta > *b {
						itheta = qn
					} else if delta < -*b {
						itheta = 0
					}
				}
			} else {
				bias := -32767 / qn
				if ithetaRaw > 8192 {
					bias = 32767 / qn
				}
				down := (ithetaRaw*qn + bias) >> 14
				if down < 0 {
					down = 0
				}
				if down > qn-1 {
					down = qn - 1
				}
				if ctx.thetaRound < 0 {
					itheta = down
				} else {
					itheta = down + 1
				}
			}
		}

		if stereo && n > 2 {
			p0 := 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			if encode {
				x := itheta
				var fl, fh int
				if x <= x0 {
					fl, fh = p0*x, p0*(x+1)
				} else {
					fl, fh = (x-1-x0)+(x0+1)*p0, (x-x0)+(x0+1)*p0
				}
				ctx.enc.Encode(uint32(fl), uint32(fh), uint32(ft))
			} else {
				fs := int(ctx.dec.Decode(uint32(ft)))
				var x int
				if fs < (x0+1)*p0 {
					x = fs / p0
				} else {
					x = x0 + 1 + (fs - (x0+1)*p0)
				}
				var fl, fh int
				if x <= x0 {
					fl, fh = p0*x, p0*(x+1)
				} else {
					fl, fh = (x-1-x0)+(x0+1)*p0, (x-x0)+(x0+1)*p0
				}
				ctx.dec.DecodeUpdate(uint32(fl), uint32(fh), uint32(ft))
				itheta = x
			}
		} else if B0 > 1 || stereo {
			if encode {
				ctx.enc.EncodeUint(uint32(itheta), uint32(qn+1))
			} else {
				itheta = int(ctx.dec.DecodeUint(uint32(qn + 1)))
			}
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			if encode {
				var fl, fs int
				if itheta <= qn>>1 {
					fs = itheta + 1
					fl = itheta * (itheta + 1) >> 1
				} else {
					fs = qn + 1 - itheta
					fl = ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
				}
				ctx.enc.Encode(uint32(fl), uint32(fl+fs), uint32(ft))
			} else {
				fm := int(ctx.dec.Decode(uint32(ft)))
				var fl, fs int
				if fm < (qn>>1)*((qn>>1)+1)>>1 {
					itheta = (isqrt32(uint32(8*fm+1)) - 1) >> 1
					fs = itheta + 1
					fl = itheta * (itheta + 1) >> 1
				} else {
					itheta = (2*(qn+1) - isqrt32(uint32(8*(ft-fm-1)+1))) >> 1
					fs = qn + 1 - itheta
					fl = ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
				}
				ctx.dec.DecodeUpdate(uint32(fl), uint32(fl+fs), uint32(ft))
			}
		}
		itheta = itheta * 16384 / qn
		if encode && stereo {
			if itheta == 0 {
				intensityStereo(ctx, X, Y, n)
			} else {
				stereoSplit(X, Y, n)
			}
		}
	} else if stereo {
		if encode {
			inv = boolToInt(ithetaRaw > 8192 && !ctx.disableInv)
			if inv != 0 {
				for j := 0; j < n; j++ {
					Y[j] = -Y[j]
				}
			}
			intensityStereo(ctx, X, Y, n)
		}
		if *b > 2<<bitres && ctx.remainingBits > 2<<bitres {
			if encode {
				ctx.enc.EncodeBitLogp(inv != 0, 2)
			} else {
				inv = boolToInt(ctx.dec.DecodeBitLogp(2))
			}
		} else {
			inv = 0
		}
		if ctx.disableInv {
			inv = 0
		}
		itheta = 0
	}
	qalloc := ctx.tellFrac() - tell
	*b -= qalloc

	var imid, iside, delta int
	switch {
	case itheta == 0:
		imid, iside = 32767, 0
		*fill &= (1 << uint(B)) - 1
		delta = -16384
	case itheta == 16384:
		imid, iside = 0, 32767
		*fill &= ((1 << uint(B)) - 1) << uint(B)
		delta = 16384
	default:
		imid = bitexactCos(int16(itheta))
		iside = bitexactCos(int16(16384 - itheta))
		delta = fracMul16((n-1)<<7, bitexactLog2Tan(iside, imid))
	}
	if qabDebug {
		qabTheta = append(qabTheta, [6]int{i, n, qn, itheta, qalloc, ctx.dec.TellFrac()})
	}
	return splitResult{inv: inv, imid: imid, iside: iside, delta: delta, itheta: itheta, qalloc: qalloc}
}

func computeQn(n, b, offset, pulseCap int, stereo bool) int {
	exp2t8 := [8]int{16384, 17866, 19483, 21247, 23170, 25267, 27554, 30048}
	n2 := 2*n - 1
	if stereo && n == 2 {
		n2--
	}
	qb := (b + n2*offset) / n2
	if v := b - pulseCap - (4 << bitres); v < qb {
		qb = v
	}
	if (8 << bitres) < qb {
		qb = 8 << bitres
	}
	var qn int
	if qb < (1 << bitres >> 1) {
		qn = 1
	} else {
		qn = exp2t8[qb&0x7] >> uint(14-(qb>>bitres))
		qn = (qn + 1) >> 1 << 1
	}
	return qn
}

func quantBandN1(ctx *bandCtx, X, Y, lowbandOut []float64) uint {
	stereo := Y != nil
	chans := 1
	if stereo {
		chans = 2
	}
	x := X
	for c := 0; c < chans; c++ {
		sign := 0
		if ctx.remainingBits >= 1<<bitres {
			if ctx.encode {
				if x[0] < 0 {
					sign = 1
				}
				ctx.enc.EncodeBits(uint32(sign), 1)
			} else {
				sign = int(ctx.dec.DecodeBits(1))
			}
			ctx.remainingBits -= 1 << bitres
		}
		if sign != 0 {
			x[0] = -1.0
		} else {
			x[0] = 1.0
		}
		x = Y
	}
	if lowbandOut != nil {
		lowbandOut[0] = X[0] // SHR16(X[0],4) collapses in float (NORM_SCALING handles)
	}
	return 1
}

// quantPartition — decoder-only port (bands.c).
func quantPartition(ctx *bandCtx, X []float64, n, b, B int, lowband []float64, lm int, gain float64, fill int) uint {
	i := ctx.i
	spread := ctx.spread
	B0 := B
	var cm uint

	cache := cacheSlice(lm, i)
	if lm != -1 && cache != nil && b > int(cache[cache[0]])+12 && n > 2 {
		var Y []float64
		n >>= 1
		Y = X[n:]
		lm--
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		sc := computeTheta(ctx, X, Y, n, &b, B, B0, lm, false, &fill)
		imid, iside := sc.imid, sc.iside
		delta, itheta, qalloc := sc.delta, sc.itheta, sc.qalloc
		mid := float64(float32(imid) * (float32(1.0) / 32768.0))
		side := float64(float32(iside) * (float32(1.0) / 32768.0))

		if B0 > 1 && (itheta&0x3fff) != 0 {
			if itheta > 8192 {
				delta -= delta >> uint(4-lm)
			} else {
				v := delta + (n << bitres >> uint(5-lm))
				if v > 0 {
					v = 0
				}
				delta = v
			}
		}
		mbits := b - delta
		mbits /= 2
		if mbits < 0 {
			mbits = 0
		}
		if mbits > b {
			mbits = b
		}
		sbits := b - mbits
		ctx.remainingBits -= qalloc

		var nextLowband2 []float64
		if lowband != nil {
			nextLowband2 = lowband[n:]
		}
		rebalance := ctx.remainingBits
		if mbits >= sbits {
			cm = quantPartition(ctx, X, n, mbits, B, lowband, lm, mulFloat32(gain, mid), fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitres && itheta != 0 {
				sbits += rebalance - (3 << bitres)
			}
			cm |= quantPartition(ctx, Y, n, sbits, B, nextLowband2, lm, mulFloat32(gain, side), fill>>uint(B)) << uint(B0>>1)
		} else {
			cm = quantPartition(ctx, Y, n, sbits, B, nextLowband2, lm, mulFloat32(gain, side), fill>>uint(B)) << uint(B0>>1)
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitres && itheta != 16384 {
				mbits += rebalance - (3 << bitres)
			}
			cm |= quantPartition(ctx, X, n, mbits, B, lowband, lm, mulFloat32(gain, mid), fill)
		}
		return cm
	}

	// no-split case
	q := celtBits2PulsesQ3(i, lm, b)
	currBits := celtPulses2BitsQ3(i, lm, q)
	ctx.remainingBits -= currBits
	for ctx.remainingBits < 0 && q > 0 {
		ctx.remainingBits += currBits
		q--
		currBits = celtPulses2BitsQ3(i, lm, q)
		ctx.remainingBits -= currBits
	}

	if q != 0 {
		K := getPulses(q)
		if ctx.encode {
			cm = algQuant(X, n, K, spread, B, ctx.enc, gain)
		} else {
			cm = algUnquant(X, n, K, spread, B, ctx.dec, gain)
		}
	} else {
		// fill the band with folded spectrum or noise
		cmMask := uint((1 << uint(B)) - 1)
		fill &= int(cmMask)
		if fill == 0 {
			for j := 0; j < n; j++ {
				X[j] = 0
			}
		} else {
			if lowband == nil {
				for j := 0; j < n; j++ {
					ctx.seed = celtLCGRand(ctx.seed)
					X[j] = float64(int32(ctx.seed) >> 20)
				}
				cm = cmMask
			} else {
				for j := 0; j < n; j++ {
					ctx.seed = celtLCGRand(ctx.seed)
					tmp := float32(1.0 / 256.0)
					if ctx.seed&0x8000 != 0 {
						X[j] = float64(float32(lowband[j]) + tmp)
					} else {
						X[j] = float64(float32(lowband[j]) - tmp)
					}
				}
				cm = uint(fill)
			}
			renormaliseVector(X, n, gain)
		}
	}
	return cm
}

// quantBand — decoder-only mono band (bands.c).
func quantBand(ctx *bandCtx, X []float64, n, b, B int, lowband []float64, lm int, lowbandOut []float64, gain float64, lowbandScratch []float64, fill int) uint {
	n0 := n
	nB := n
	B0 := B
	timeDivide := 0
	recombine := 0
	tfChange := ctx.tfChange
	longBlocks := boolToInt(B0 == 1)
	var cm uint

	nB = nB / B

	if n == 1 {
		return quantBandN1(ctx, X, nil, lowbandOut)
	}

	if tfChange > 0 {
		recombine = tfChange
	}

	if lowbandScratch != nil && lowband != nil && (recombine != 0 || ((nB&1) == 0 && tfChange < 0) || B0 > 1) {
		copy(lowbandScratch[:n], lowband[:n])
		lowband = lowbandScratch
	}

	bitInterleave := [16]int{0, 1, 1, 1, 2, 3, 3, 3, 2, 3, 3, 3, 2, 3, 3, 3}
	for k := 0; k < recombine; k++ {
		if ctx.encode {
			haar1(X, n>>uint(k), 1<<uint(k))
		}
		if lowband != nil {
			haar1(lowband, n>>uint(k), 1<<uint(k))
		}
		fill = bitInterleave[fill&0xF] | bitInterleave[fill>>4]<<2
	}
	B >>= recombine
	nB <<= recombine

	for (nB&1) == 0 && tfChange < 0 {
		if ctx.encode {
			haar1(X, nB, B)
		}
		if lowband != nil {
			haar1(lowband, nB, B)
		}
		fill |= fill << uint(B)
		B <<= 1
		nB >>= 1
		timeDivide++
		tfChange++
	}
	B0 = B
	nB0 := nB

	if B0 > 1 {
		if ctx.encode {
			deinterleaveHadamard(X, nB>>uint(recombine), B0<<uint(recombine), longBlocks)
		}
		if lowband != nil {
			deinterleaveHadamard(lowband, nB>>uint(recombine), B0<<uint(recombine), longBlocks)
		}
	}

	cm = quantPartition(ctx, X, n, b, B, lowband, lm, gain, fill)

	// resynth (decoder always)
	if B0 > 1 {
		interleaveHadamard(X, nB>>uint(recombine), B0<<uint(recombine), longBlocks)
	}
	nB = nB0
	B = B0
	for k := 0; k < timeDivide; k++ {
		B >>= 1
		nB <<= 1
		cm |= cm >> uint(B)
		haar1(X, nB, B)
	}
	bitDeinterleave := [16]uint{
		0x00, 0x03, 0x0C, 0x0F, 0x30, 0x33, 0x3C, 0x3F,
		0xC0, 0xC3, 0xCC, 0xCF, 0xF0, 0xF3, 0xFC, 0xFF,
	}
	for k := 0; k < recombine; k++ {
		cm = bitDeinterleave[cm&0xF]
		haar1(X, n0>>uint(k), 1<<uint(k))
	}
	B <<= recombine

	if lowbandOut != nil {
		nrm := float32(math.Sqrt(float64(n0)))
		for j := 0; j < n0; j++ {
			lowbandOut[j] = float64(nrm * float32(X[j]))
		}
	}
	cm &= (1 << uint(B)) - 1
	return cm
}

// quantBandStereo — decoder-only stereo band (bands.c).
func quantBandStereo(ctx *bandCtx, X, Y []float64, n, b, B int, lowband []float64, lm int, lowbandOut, lowbandScratch []float64, fill int) uint {
	var cm uint
	if n == 1 {
		return quantBandN1(ctx, X, Y, lowbandOut)
	}
	origFill := fill
	if ctx.encode {
		const minStereoEnergy = 1e-10
		if ctx.bandE[ctx.i] < minStereoEnergy || ctx.bandE[ctx.nbEBands+ctx.i] < minStereoEnergy {
			if ctx.bandE[ctx.i] > ctx.bandE[ctx.nbEBands+ctx.i] {
				copy(Y[:n], X[:n])
			} else {
				copy(X[:n], Y[:n])
			}
		}
	}
	sc := computeTheta(ctx, X, Y, n, &b, B, B, lm, true, &fill)
	inv, imid, iside := sc.inv, sc.imid, sc.iside
	delta, itheta, qalloc := sc.delta, sc.itheta, sc.qalloc
	mid := float64(float32(imid) * (float32(1.0) / 32768.0))
	side := float64(float32(iside) * (float32(1.0) / 32768.0))

	if n == 2 {
		mbits := b
		sbits := 0
		if itheta != 0 && itheta != 16384 {
			sbits = 1 << bitres
		}
		mbits -= sbits
		c := boolToInt(itheta > 8192)
		ctx.remainingBits -= qalloc + sbits

		x2, y2 := X, Y
		if c != 0 {
			x2, y2 = Y, X
		}
		sign := 0
		if sbits != 0 {
			if ctx.encode {
				if x2[0]*y2[1]-x2[1]*y2[0] < 0 {
					sign = 1
				}
				ctx.enc.EncodeBits(uint32(sign), 1)
			} else {
				sign = int(ctx.dec.DecodeBits(1))
			}
		}
		signf := 1.0 - 2.0*float64(sign)
		cm = quantBand(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill)
		y2[0] = -signf * x2[1]
		y2[1] = signf * x2[0]
		// The floating-point libopus build stores celt_norm, opus_val32, and
		// the MULT32_32_Q31/ADD32/SUB32 results as float. Preserve those
		// intermediate roundings instead of carrying the N=2 stereo rotation
		// through Go's float64 coefficient storage.
		mid32, side32 := float32(mid), float32(side)
		x0 := mid32 * float32(X[0])
		x1 := mid32 * float32(X[1])
		y0 := side32 * float32(Y[0])
		y1 := side32 * float32(Y[1])
		X[0] = float64(x0 - y0)
		Y[0] = float64(x0 + y0)
		X[1] = float64(x1 - y1)
		Y[1] = float64(x1 + y1)
	} else {
		mbits := b - delta
		mbits /= 2
		if mbits < 0 {
			mbits = 0
		}
		if mbits > b {
			mbits = b
		}
		sbits := b - mbits
		ctx.remainingBits -= qalloc
		rebalance := ctx.remainingBits
		if mbits >= sbits {
			cm = quantBand(ctx, X, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitres && itheta != 0 {
				sbits += rebalance - (3 << bitres)
			}
			cm |= quantBand(ctx, Y, n, sbits, B, nil, lm, nil, side, nil, fill>>uint(B))
		} else {
			cm = quantBand(ctx, Y, n, sbits, B, nil, lm, nil, side, nil, fill>>uint(B))
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitres && itheta != 16384 {
				mbits += rebalance - (3 << bitres)
			}
			cm |= quantBand(ctx, X, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
		}
	}

	// resynth stereo merge
	if n != 2 {
		stereoMerge(X, Y, mid, n)
	}
	if inv != 0 {
		for j := 0; j < n; j++ {
			Y[j] = -Y[j]
		}
	}
	return cm
}

// stereoMerge — float port of bands.c stereo_merge.
func stereoMerge(X, Y []float64, mid float64, n int) {
	var xp, side float32
	for j := 0; j < n; j++ {
		x := float32(X[j])
		y := float32(Y[j])
		xp += y * x
		side += y * y
	}
	mid32 := float32(mid)
	xp *= mid32
	mid2 := mid32 // SHR16(mid,1) is no-op in float build (libopus arch.h), so mid2=mid
	El := mid2*mid2 + side - 2*xp
	Er := mid2*mid2 + side + 2*xp
	if Er < 6e-4 || El < 6e-4 {
		copy(Y[:n], X[:n])
		return
	}
	lgain := float32(1.0) / float32(math.Sqrt(float64(El)))
	rgain := float32(1.0) / float32(math.Sqrt(float64(Er)))
	for j := 0; j < n; j++ {
		l := mid32 * float32(X[j])
		r := float32(Y[j])
		X[j] = float64(lgain * (l - r))
		Y[j] = float64(rgain * (l + r))
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// specialHybridFolding — port of bands.c special_hybrid_folding.
// norm2 is the second-channel norm buffer (== norm[normLen:] for stereo).
func specialHybridFolding(norm, norm2 []float64, start, M int, dualStereo bool, normOffset int) {
	_ = normOffset
	n1 := M * int(EBands48000[start+1]-EBands48000[start])
	n2 := M * int(EBands48000[start+2]-EBands48000[start+1])
	cnt := n2 - n1
	if cnt <= 0 {
		return
	}
	copy(norm[n1:n1+cnt], norm[2*n1-n2:2*n1-n2+cnt])
	if dualStereo {
		copy(norm2[n1:n1+cnt], norm2[2*n1-n2:2*n1-n2+cnt])
	}
}

// QuantAllBands — decoder-only port of bands.c quant_all_bands.
// X (and Y for stereo) hold the interleaved normalised MDCT coefficients,
// length M*eBands[numBands] per channel. pulses[] are the per-band Q3 PVQ
// budgets from computeAllocation; balance is its returned leftover.
// Returns the updated fold seed (the range value to store as st->rng).
func QuantAllBands(dec *entcode.Decoder, start, end int, X, Y []float64,
	collapseMasks []byte, pulses []int, shortBlocks bool, spread int,
	dualStereo bool, intensity int, tfRes []int, totalBitsQ3, balance, lm, codedBands int,
	seed uint32, disableInv bool) uint32 {
	return quantAllBandsImpl(false, nil, dec, nil, start, end, X, Y,
		collapseMasks, pulses, shortBlocks, spread, dualStereo, intensity, tfRes,
		totalBitsQ3, balance, lm, codedBands, seed, disableInv, 0)
}

// QuantAllBandsEncode is the encoder-side entry point. bandE holds per-band
// energies in channel-major layout (used by stereo intensity/split). It mirrors
// QuantAllBands symbol-for-symbol so the existing decoder reconstructs X/Y.
func QuantAllBandsEncode(enc *entcode.Encoder, bandE []float64, start, end int, X, Y []float64,
	collapseMasks []byte, pulses []int, shortBlocks bool, spread int,
	dualStereo bool, intensity int, tfRes []int, totalBitsQ3, balance, lm, codedBands int,
	seed uint32, disableInv bool, complexity int) uint32 {
	return quantAllBandsImpl(true, enc, nil, bandE, start, end, X, Y,
		collapseMasks, pulses, shortBlocks, spread, dualStereo, intensity, tfRes,
		totalBitsQ3, balance, lm, codedBands, seed, disableInv, complexity)
}

func quantAllBandsImpl(encode bool, enc *entcode.Encoder, dec *entcode.Decoder, bandE []float64,
	start, end int, X, Y []float64,
	collapseMasks []byte, pulses []int, shortBlocks bool, spread int,
	dualStereo bool, intensity int, tfRes []int, totalBitsQ3, balance, lm, codedBands int,
	seed uint32, disableInv bool, complexity int) uint32 {
	// theta_rdo: at complexity >= 8 the stereo encoder codes each joint band
	// twice (theta rounded down and up) and keeps the better reconstruction.
	thetaRdo := encode && Y != nil && !dualStereo && complexity >= 8

	eBands := EBands48000
	nbEBands := NumBands48000
	M := 1 << uint(lm)
	C := 1
	if Y != nil {
		C = 2
	}
	B := 1
	if shortBlocks {
		B = M
	}
	normOffset := M * int(eBands[start])
	normLen := M*int(eBands[nbEBands-1]) - normOffset
	if normLen < 0 {
		normLen = 0
	}
	norm := make([]float64, C*normLen)
	scratch := make([]float64, M*int(eBands[nbEBands]))

	ctx := &bandCtx{
		i:          start,
		intensity:  intensity,
		spread:     spread,
		dec:        dec,
		enc:        enc,
		encode:     encode,
		nbEBands:   nbEBands,
		bandE:      bandE,
		seed:       seed,
		disableInv: disableInv,
		avoidSplit: B > 1,
	}

	lowbandOffset := 0
	updateLowband := true

	for i := start; i < end; i++ {
		ctx.i = i
		last := i == end-1
		Xband := X[M*int(eBands[i]):]
		var Yband []float64
		if Y != nil {
			Yband = Y[M*int(eBands[i]):]
		}
		N := M*int(eBands[i+1]) - M*int(eBands[i])
		tell := ctx.tellFrac()
		if i != start {
			balance -= tell
		}
		remainingBits := totalBitsQ3 - tell - 1
		ctx.remainingBits = remainingBits
		var b int
		if i <= codedBands-1 {
			currBalance := balance / min(3, codedBands-i)
			b = pulses[i] + currBalance
			if b > remainingBits+1 {
				b = remainingBits + 1
			}
			if b > 16383 {
				b = 16383
			}
			if b < 0 {
				b = 0
			}
		}

		if (M*int(eBands[i])-N >= normOffset || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFolding(norm, normSecond(norm, normLen), start, M, dualStereo, normOffset)
		}

		ctx.tfChange = tfRes[i]

		lowbandScratch := scratch
		if last {
			lowbandScratch = nil
		}

		effectiveLowband := -1
		var xcm, ycm uint
		if lowbandOffset != 0 && (spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband = M*int(eBands[lowbandOffset]) - normOffset - N
			if effectiveLowband < 0 {
				effectiveLowband = 0
			}
			foldStart := lowbandOffset
			for {
				foldStart--
				if M*int(eBands[foldStart]) <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if !(foldEnd < i && M*int(eBands[foldEnd]) < effectiveLowband+normOffset+N) {
					break
				}
			}
			for foldI := foldStart; foldI < foldEnd; foldI++ {
				xcm |= uint(collapseMasks[foldI*C+0])
				ycm |= uint(collapseMasks[foldI*C+C-1])
			}
		} else {
			xcm = uint((1 << uint(B)) - 1)
			ycm = xcm
		}

		if dualStereo && i == intensity {
			dualStereo = false
			lim := M*int(eBands[i]) - normOffset
			for j := 0; j < lim; j++ {
				norm[j] = float64(float32(0.5) * (float32(norm[j]) + float32(norm[normLen+j])))
			}
		}

		if dualStereo {
			var lbX, lbY, outX, outY []float64
			if effectiveLowband != -1 {
				lbX = norm[effectiveLowband:]
				lbY = norm[normLen+effectiveLowband:]
			}
			if !last {
				outX = norm[M*int(eBands[i])-normOffset:]
				outY = norm[normLen+M*int(eBands[i])-normOffset:]
			}
			xcm = quantBand(ctx, Xband, N, b/2, B, lbX, lm, outX, 1.0, lowbandScratch, int(xcm))
			ycm = quantBand(ctx, Yband, N, b/2, B, lbY, lm, outY, 1.0, lowbandScratch, int(ycm))
		} else {
			var lb, out []float64
			if effectiveLowband != -1 {
				lb = norm[effectiveLowband:]
			}
			if !last {
				out = norm[M*int(eBands[i])-normOffset:]
			}
			if Yband != nil && thetaRdo && i < intensity {
				var w [2]float32
				computeChannelWeights(float32(bandE[i]), float32(bandE[nbEBands+i]), &w)
				cm := int(xcm | ycm)
				// Make a copy.
				encSave := enc.Clone()
				ctxSave := *ctx
				xSave := append([]float64(nil), Xband[:N]...)
				ySave := append([]float64(nil), Yband[:N]...)
				// Encode and round down.
				ctx.thetaRound = -1
				cmDown := quantBandStereo(ctx, Xband, Yband, N, b, B, lb, lm, out, lowbandScratch, cm)
				dist0 := float32(w[0]*innerProd64as32(xSave, Xband, N)) + float32(w[1]*innerProd64as32(ySave, Yband, N))
				// Save first result.
				encSave2 := enc.Clone()
				ctxSave2 := *ctx
				xSave2 := append([]float64(nil), Xband[:N]...)
				ySave2 := append([]float64(nil), Yband[:N]...)
				var normSave2 []float64
				if !last {
					normSave2 = append([]float64(nil), out[:N]...)
				}
				// Restore.
				enc.Restore(encSave)
				*ctx = ctxSave
				copy(Xband[:N], xSave)
				copy(Yband[:N], ySave)
				if i == start+1 {
					specialHybridFolding(norm, normSecond(norm, normLen), start, M, dualStereo, normOffset)
				}
				// Encode and round up.
				ctx.thetaRound = 1
				xcm = quantBandStereo(ctx, Xband, Yband, N, b, B, lb, lm, out, lowbandScratch, cm)
				dist1 := float32(w[0]*innerProd64as32(xSave, Xband, N)) + float32(w[1]*innerProd64as32(ySave, Yband, N))
				if dist0 >= dist1 {
					xcm = cmDown
					enc.Restore(encSave2)
					*ctx = ctxSave2
					copy(Xband[:N], xSave2)
					copy(Yband[:N], ySave2)
					if !last {
						copy(out[:N], normSave2)
					}
				}
			} else if Yband != nil {
				ctx.thetaRound = 0
				xcm = quantBandStereo(ctx, Xband, Yband, N, b, B, lb, lm, out, lowbandScratch, int(xcm|ycm))
			} else {
				xcm = quantBand(ctx, Xband, N, b, B, lb, lm, out, 1.0, lowbandScratch, int(xcm|ycm))
			}
			ycm = xcm
		}
		collapseMasks[i*C+0] = byte(xcm)
		collapseMasks[i*C+C-1] = byte(ycm)
		if qabRangeTrace {
			var rng uint32
			if encode {
				rng = enc.GetRng()
			} else {
				rng = dec.GetRng()
			}
			tag := "DEC"
			if encode {
				tag = "ENC"
			}
			fmt.Fprintf(os.Stderr, "[QAB %s] band=%d N=%d b=%d tellf=%d rng=%08x xcm=%d\n",
				tag, i, N, b, ctx.tellFrac(), rng, xcm)
		}
		if qabDebug && dec != nil {
			qabLog = append(qabLog, qabBandTrace{i: i, N: N, b: b, tellf: dec.TellFrac(), rng: dec.GetRng(), xcm: xcm})
			fmt.Fprintf(os.Stderr, "[XB] band=%d N=%d", i, N)
			for j := 0; j < N; j++ {
				fmt.Fprintf(os.Stderr, " X[%d]=%.9g", j, Xband[j])
			}
			fmt.Fprintln(os.Stderr)
		}
		balance += pulses[i] + tell
		if encode && qabTellTrace != nil {
			*qabTellTrace = append(*qabTellTrace, ctx.tellFrac())
		}
		updateLowband = b > (N << bitres)
		ctx.avoidSplit = false
	}
	return ctx.seed
}

// normSecond returns the second-channel slice of norm (nil if mono-sized).
func normSecond(norm []float64, normLen int) []float64 {
	if len(norm) >= 2*normLen && normLen > 0 {
		return norm[normLen:]
	}
	return nil
}

// computeChannelWeights mirrors compute_channel_weights (float build): the
// per-channel weights of the theta_rdo distortion, each band energy plus a
// third of the smaller one.
func computeChannelWeights(Ex, Ey float32, w *[2]float32) {
	minE := Ex
	if Ey < minE {
		minE = Ey
	}
	// Adjustment to make the weights a bit more conservative.
	Ex = Ex + minE/3
	Ey = Ey + minE/3
	w[0] = Ex
	w[1] = Ey
}
