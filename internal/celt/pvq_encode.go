package celt

import (
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// This file is the encoder-side counterpart to the decoder PVQ path in
// quant_pvq.go / pvq.go. It is a faithful Go port of the libopus CELT
// quantizer leaf operations (celt/vq.c op_pvq_search / alg_quant and
// celt/cwrs.c icwrs / encode_pulses), float build.
//
// The decoder reads each band's pulse vector with decodePulses → cwrsiLibopus.
// For a lossless round-trip the encoder must (1) search for an integer pulse
// vector iy with sum(|iy|)=K (op_pvq_search), (2) map iy to the exact same
// codebook index the decoder will invert (icwrsLibopus, the inverse of
// cwrsiLibopus), and (3) range-code that index with the identical ft used by
// decodePulses. The search heuristic only affects quality; round-trip
// correctness depends solely on the index mapping and ft matching the decoder.

// icwrsLibopus maps a signed pulse vector y (length n, ||y||_1 = k) to its
// codebook index. It is the exact inverse of cwrsiLibopus:
// cwrsiLibopus(n, k, icwrsLibopus(n, k, y)) == y. Requires n >= 2 (the N==1
// band is handled by quantBandN1, not this PVQ path).
//
// It is derived by accumulating, position by position, the exact amount that
// cwrsiLibopus subtracts from the running index `i` for the chosen y[pos].
// Both decode branches (k>=n and k<n) reduce to the same per-position delta:
//
//	delta = U(n, k-|y[pos]|) + (y[pos]<0 ? U(n, k+1) : 0)
//
// where U is celtPVQU (symmetric). The n==2 tail uses the closed-form
// (2k0+1) sign offset and (2*kAfter-1) magnitude offset, and the final n==1
// element contributes its sign bit as the residual index.
func icwrsLibopus(n, k int, y []int) uint32 {
	var idx uint64
	pos := 0

	for n > 2 {
		m := y[pos]
		sign := m < 0
		if sign {
			m = -m
		}
		kNew := k - m
		idx += celtPVQU(n, kNew)
		if sign {
			idx += celtPVQU(n, k+1)
		}
		k = kNew
		n--
		pos++
	}

	// n == 2 tail: y[pos] is the 2-D element, y[pos+1] the trailing 1-D sign.
	a := y[pos]
	b := y[pos+1]
	m2 := a
	if m2 < 0 {
		m2 = -m2
		idx += uint64(2*k + 1)
	}
	kAfter := k - m2 // == abs(b)
	if kAfter != 0 {
		idx += uint64(2*kAfter - 1)
	}
	if b < 0 {
		idx++
	}
	return uint32(idx)
}

// encodePulses range-codes the pulse vector iy, mirroring decodePulses. ft is
// computed identically (clamped V(n,k)); idx = icwrsLibopus(n, iy) is < ft.
func encodePulses(enc *entcode.Encoder, iy []int, n, k int) {
	if k == 0 {
		return
	}
	v := cwrsV(n, k)
	ft := uint32(v)
	if v > uint64(0xFFFFFFFF) {
		ft = 0xFFFFFFFF
	}
	idx := icwrsLibopus(n, k, iy)
	enc.EncodeUint(idx, ft)
}

// opPVQSearch finds an integer pulse vector iy (length n, sum(|iy|)=k) that
// approximately maximizes the projection of the unit-norm target X onto the
// quantized direction. Faithful float port of libopus op_pvq_search_c
// (celt/vq.c). It mutates X (takes absolute values / pre-search scratch) and
// returns yy = sum(iy[j]^2), which normalise_residual needs as 1/sqrt(yy).
func opPVQSearch(X []float32, iy []int, k, n int) float64 {
	// Float32 port of libopus op_pvq_search_c (float build): every sum,
	// product and comparison rounds to float32 exactly as opus_val16 /
	// opus_val32 do there.
	// Zeroed stack buffers for every band width CELT codes.
	var yBuf, xBuf [maxPVQN]float32
	var signBuf [maxPVQN]int
	var y, x []float32
	var signx []int
	if n <= maxPVQN {
		y, x, signx = yBuf[:n], xBuf[:n], signBuf[:n]
	} else {
		y, x, signx = make([]float32, n), make([]float32, n), make([]int, n)
	}
	var sum, xy, yy float32
	// Equal-length views let the compiler drop the per-element bounds checks.
	y, signx = y[:len(x)], signx[:len(x)]
	X, iy = X[:len(x)], iy[:len(x)]

	// Get rid of the sign.
	for j := range x {
		x[j] = float32(X[j])
		if x[j] < 0 {
			signx[j] = 1
			x[j] = -x[j]
		}
		iy[j] = 0
		y[j] = 0
	}

	pulsesLeft := k

	// Do a pre-search by projecting on the pyramid.
	if k > (n >> 1) {
		for _, v := range x {
			sum += v
		}
		// Prevents infinities and NaNs from causing too many pulses to be
		// allocated. 64 is an approximation of infinity here.
		if !(sum > 1e-15 && sum < 64) {
			x[0] = 1
			clear(x[1:])
			sum = 1
		}
		// Using K+e with e < 1 guarantees we cannot get more than K pulses.
		rcp := float32(float32(k)+float32(0.8)) * (float32(1) / sum)
		for j := range x {
			iy[j] = int(math.Floor(float64(float32(rcp * x[j]))))
			y[j] = float32(iy[j])
			yy += float32(y[j] * y[j])
			xy += float32(x[j] * y[j])
			y[j] *= 2
			pulsesLeft -= iy[j]
		}
	}

	// This should never happen, but just in case it does (e.g. on silence)
	// we fill the first bin with pulses.
	if pulsesLeft > n+3 {
		tmp := float32(pulsesLeft)
		yy += float32(tmp * tmp)
		yy += float32(tmp * y[0])
		iy[0] += pulsesLeft
		pulsesLeft = 0
	}

	for i := 0; i < pulsesLeft; i++ {
		// The squared magnitude term gets added anyway, so we might as well
		// add it outside the loop.
		yy += 1
		bestID := 0
		rxy := xy + x[0]
		ryy := yy + y[0]
		// Approximate score: we maximise Rxy/sqrt(Ryy) (we're guaranteed
		// that Rxy is positive because the sign is pre-computed).
		rxy = float32(rxy * rxy)
		bestDen := ryy
		bestNum := rxy
		for j := 1; j < len(x); j++ {
			rxy = xy + x[j]
			ryy = yy + y[j]
			rxy = float32(rxy * rxy)
			// num/den >= best_num/best_den without any division.
			if float32(bestDen*rxy) > float32(ryy*bestNum) {
				bestDen = ryy
				bestNum = rxy
				bestID = j
			}
		}
		xy += x[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}

	// Put the original sign back.
	for j := range x {
		if signx[j] != 0 {
			iy[j] = -iy[j]
		}
		X[j] = x[j]
	}
	return float64(yy)
}

// algQuant is the encoder-side counterpart of algUnquant (celt/vq.c alg_quant,
// float build with resynth). It forward-rotates X, searches for the pulse
// vector, range-codes it, then reconstructs X exactly as the decoder will
// (normalise_residual + inverse rotation) so that subsequent bands fold off the
// identical normalised spectrum. Returns the band collapse mask.
func algQuant(X []float32, n, k, spread, b int, enc *entcode.Encoder, gain float64) uint {
	expRotation(X, n, 1, b, k, spread)
	var iyBuf [maxPVQN]int
	var iy []int
	if n <= maxPVQN {
		iy = iyBuf[:n]
	} else {
		iy = make([]int, n)
	}
	yy := opPVQSearch(X, iy, k, n)
	encodePulses(enc, iy, n, k)
	normaliseResidual(iy, X, n, yy, gain)
	expRotation(X, n, -1, b, k, spread)
	return extractCollapseMask(iy, n, b)
}
