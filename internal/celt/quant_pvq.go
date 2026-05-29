package celt

import (
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// This file is a faithful Go port of the libopus CELT band-quantization decoder
// path (celt/bands.c quant_all_bands / quant_band / quant_partition / compute_theta
// and celt/vq.c alg_unquant), float build, decoder-only (encode=0, resynth=1).
//
// celt_norm is float64. Q15 fixed-point scaling collapses to plain float multiply
// in the float build, so MULT16_16*/PSHR32/EXTRACT16 etc. become direct arithmetic.

const (
	bitres              = 3 // libopus BITRES (1<<bitres == 8)
	spreadNone          = 0
	spreadAggressive    = 3
	qThetaOffset        = 4
	qThetaOffsetTwoPhase = 16
	logMaxPseudo        = 6
)

// qabDebug enables per-band trace capture in QuantAllBands (test diagnostics).
var qabDebug = false

type qabBandTrace struct {
	i, N, b, tellf int
	rng            uint32
	xcm            uint
}

var qabLog []qabBandTrace

// qabDP records decodePulses (n,k,V,tellBefore,tellAfter) calls when qabDebug is set.
var qabDP [][5]uint64

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

// celtCosNorm matches celt_cos_norm(x) = cos(0.5*pi*x) in the float build.
func celtCosNorm(x float64) float64 { return math.Cos((0.5 * math.Pi) * x) }

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
	if qabDebug {
		tb = dec.TellFrac()
	}
	idx := dec.DecodeUint(ft)
	if qabDebug {
		qabDP = append(qabDP, [5]uint64{uint64(n), uint64(k), v, uint64(tb), uint64(dec.TellFrac())})
	}
	iy := cwrsiLibopus(n, k, idx)
	ryy := 0.0
	for _, y := range iy {
		ryy += float64(y * y)
	}
	return iy, ryy
}

func normaliseResidual(iy []int, X []float64, n int, ryy, gain float64) {
	g := gain
	if ryy > 0 {
		g = (1.0 / math.Sqrt(ryy)) * gain
	}
	for i := 0; i < n; i++ {
		X[i] = g * float64(iy[i])
	}
}

func expRotation1(X []float64, length, stride int, c, s float64) {
	ms := -s
	// forward
	for i := 0; i < length-stride; i++ {
		x1 := X[i]
		x2 := X[i+stride]
		X[i+stride] = c*x2 + s*x1
		X[i] = c*x1 + ms*x2
	}
	// backward
	for i := length - 2*stride - 1; i >= 0; i-- {
		x1 := X[i]
		x2 := X[i+stride]
		X[i+stride] = c*x2 + s*x1
		X[i] = c*x1 + ms*x2
	}
}

func expRotation(X []float64, length, dir, stride, k, spread int) {
	spreadFactor := [3]int{15, 10, 5}
	if 2*k >= length || spread == spreadNone {
		return
	}
	factor := spreadFactor[spread-1]
	gain := float64(length) / float64(length+factor*k)
	theta := 0.5 * gain * gain
	c := celtCosNorm(theta)
	s := celtCosNorm(1.0 - theta) // sin(theta*pi/2)
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
	e := 1e-15
	for i := 0; i < n; i++ {
		e += X[i] * X[i]
	}
	g := (1.0 / math.Sqrt(e)) * gain
	for i := 0; i < n; i++ {
		X[i] *= g
	}
}

// haar1 matches libopus haar1 (bands.c), float build.
func haar1(X []float64, n0, stride int) {
	n0 >>= 1
	const s = 0.70710678
	for i := 0; i < stride; i++ {
		for j := 0; j < n0; j++ {
			t1 := s * X[stride*2*j+i]
			t2 := s * X[stride*(2*j+1)+i]
			X[stride*2*j+i] = t1 + t2
			X[stride*(2*j+1)+i] = t1 - t2
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
	remainingBits int
	bandE         []float64
	seed          uint32
	arch          int
	disableInv    bool
	avoidSplit    bool
}

type splitResult struct {
	inv, imid, iside, delta, itheta, qalloc int
}

// computeTheta — decoder-only port of libopus compute_theta (bands.c).
func computeTheta(ctx *bandCtx, X, Y []float64, n int, b *int, B, B0, lm int, stereo bool, fill *int) splitResult {
	i := ctx.i
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
	tell := ctx.dec.TellFrac()
	itheta := 0
	inv := 0
	if qn != 1 {
		if stereo && n > 2 {
			p0 := 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
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
		} else if B0 > 1 || stereo {
			itheta = int(ctx.dec.DecodeUint(uint32(qn + 1)))
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			fm := int(ctx.dec.Decode(uint32(ft)))
			var fl, fs int
			if fm < (qn>>1)*((qn>>1)+1)>>1 {
				itheta = (isqrt32(uint32(8*fm+1)) - 1) >> 1
				fs = itheta + 1
				fl = itheta * (itheta + 1) >> 1
			} else {
				itheta = (2*(qn+1) - isqrt32(uint32(8*(ft-fm-1)+1))) >> 1
				fs = qn + 1 - itheta
				fl = ft - ((qn+1-itheta)*(qn+2-itheta)>>1)
			}
			ctx.dec.DecodeUpdate(uint32(fl), uint32(fl+fs), uint32(ft))
		}
		itheta = itheta * 16384 / qn
	} else if stereo {
		if *b > 2<<bitres && ctx.remainingBits > 2<<bitres {
			inv = boolToInt(ctx.dec.DecodeBitLogp(2))
		} else {
			inv = 0
		}
		if ctx.disableInv {
			inv = 0
		}
		itheta = 0
	}
	qalloc := ctx.dec.TellFrac() - tell
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
			sign = int(ctx.dec.DecodeBits(1))
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
		mid := float64(imid) / 32768.0
		side := float64(iside) / 32768.0

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
			cm = quantPartition(ctx, X, n, mbits, B, lowband, lm, gain*mid, fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitres && itheta != 0 {
				sbits += rebalance - (3 << bitres)
			}
			cm |= quantPartition(ctx, Y, n, sbits, B, nextLowband2, lm, gain*side, fill>>uint(B)) << uint(B0>>1)
		} else {
			cm = quantPartition(ctx, Y, n, sbits, B, nextLowband2, lm, gain*side, fill>>uint(B)) << uint(B0>>1)
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitres && itheta != 16384 {
				mbits += rebalance - (3 << bitres)
			}
			cm |= quantPartition(ctx, X, n, mbits, B, lowband, lm, gain*mid, fill)
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
		cm = algUnquant(X, n, K, spread, B, ctx.dec, gain)
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
					tmp := 1.0 / 256.0
					if ctx.seed&0x8000 != 0 {
						X[j] = lowband[j] + tmp
					} else {
						X[j] = lowband[j] - tmp
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
		if lowband != nil {
			haar1(lowband, n>>uint(k), 1<<uint(k))
		}
		fill = bitInterleave[fill&0xF] | bitInterleave[fill>>4]<<2
	}
	B >>= recombine
	nB <<= recombine

	for (nB&1) == 0 && tfChange < 0 {
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
		nrm := math.Sqrt(float64(n0))
		for j := 0; j < n0; j++ {
			lowbandOut[j] = nrm * X[j]
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
	sc := computeTheta(ctx, X, Y, n, &b, B, B, lm, true, &fill)
	inv, imid, iside := sc.inv, sc.imid, sc.iside
	delta, itheta, qalloc := sc.delta, sc.itheta, sc.qalloc
	mid := float64(imid) / 32768.0
	side := float64(iside) / 32768.0

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
			sign = int(ctx.dec.DecodeBits(1))
		}
		signf := 1.0 - 2.0*float64(sign)
		cm = quantBand(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill)
		y2[0] = -signf * x2[1]
		y2[1] = signf * x2[0]
		// resynth N=2
		X[0] *= mid
		X[1] *= mid
		Y[0] *= side
		Y[1] *= side
		t0, t1 := X[0], X[1]
		X[0] = t0 - Y[0]
		Y[0] = t0 + Y[0]
		X[1] = t1 - Y[1]
		Y[1] = t1 + Y[1]
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
	var xp, side float64
	for j := 0; j < n; j++ {
		xp += X[j] * Y[j]
		side += Y[j] * Y[j]
	}
	xp *= mid
	mid2 := mid // SHR16(mid,1) is for Q-domain; in float we follow reference: mid2=0.5*mid? see note
	mid2 = 0.5 * mid
	El := mid2*mid2 + side - 2*xp
	Er := mid2*mid2 + side + 2*xp
	if Er < 6e-4 || El < 6e-4 {
		copy(Y[:n], X[:n])
		return
	}
	lgain := 1.0 / math.Sqrt(El)
	rgain := 1.0 / math.Sqrt(Er)
	for j := 0; j < n; j++ {
		l := mid * X[j]
		r := Y[j]
		X[j] = lgain * (l - r)
		Y[j] = rgain * (l + r)
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
		tell := dec.TellFrac()
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
				norm[j] = 0.5 * (norm[j] + norm[normLen+j])
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
			if Yband != nil {
				xcm = quantBandStereo(ctx, Xband, Yband, N, b, B, lb, lm, out, lowbandScratch, int(xcm|ycm))
			} else {
				xcm = quantBand(ctx, Xband, N, b, B, lb, lm, out, 1.0, lowbandScratch, int(xcm|ycm))
			}
			ycm = xcm
		}
		collapseMasks[i*C+0] = byte(xcm)
		collapseMasks[i*C+C-1] = byte(ycm)
		if qabDebug {
			qabLog = append(qabLog, qabBandTrace{i: i, N: N, b: b, tellf: dec.TellFrac(), rng: dec.GetRng(), xcm: xcm})
		}
		balance += pulses[i] + tell
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
