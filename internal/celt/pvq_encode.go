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
func opPVQSearch(X []float64, iy []int, k, n int) float64 {
	y := make([]float64, n)
	signx := make([]int, n)
	var sum, xy, yy float64

	// Strip the sign; remember it to reapply after the (non-negative) search.
	for j := 0; j < n; j++ {
		if X[j] < 0 {
			signx[j] = 1
		}
		X[j] = math.Abs(X[j])
		iy[j] = 0
		y[j] = 0
	}

	pulsesLeft := k

	// Pre-search by projecting onto the pyramid when K is large relative to N.
	if k > (n >> 1) {
		for j := 0; j < n; j++ {
			sum += X[j]
		}
		// Guard against a degenerate (near-zero or huge) sum, like libopus.
		if !(sum > 1e-9 && sum < 64) {
			X[0] = 1.0
			for j := 1; j < n; j++ {
				X[j] = 0
			}
			sum = 1.0
		}
		// K+0.8 with floor guarantees we never overshoot K pulses.
		rcp := (float64(k) + 0.8) / sum
		for j := 0; j < n; j++ {
			iy[j] = int(math.Floor(rcp * X[j]))
			y[j] = float64(iy[j])
			yy += y[j] * y[j]
			xy += X[j] * y[j]
			y[j] *= 2 // y holds 2*iy so the greedy loop need not double it
			pulsesLeft -= iy[j]
		}
	}

	// Should never happen, but mirror libopus' safety distribution.
	if pulsesLeft > n+3 {
		tmp := float64(pulsesLeft)
		yy += tmp * tmp
		yy += tmp * y[0]
		iy[0] += pulsesLeft
		pulsesLeft = 0
	}

	// Greedily add the remaining pulses one at a time, each time choosing the
	// position that maximizes Rxy^2 / Ryy (cross-multiplied to avoid division).
	for i := 0; i < pulsesLeft; i++ {
		bestID := 0
		var bestNum, bestDen float64
		yy += 1.0 // the new pulse contributes +1 to yy regardless of position
		for j := 0; j < n; j++ {
			rxy := xy + X[j]
			ryy := yy + y[j]
			rxy = rxy * rxy
			if j == 0 || bestDen*rxy > ryy*bestNum {
				bestDen = ryy
				bestNum = rxy
				bestID = j
			}
		}
		xy += X[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}

	// Reapply the original signs.
	for j := 0; j < n; j++ {
		if signx[j] != 0 {
			iy[j] = -iy[j]
		}
	}
	return yy
}

// algQuant is the encoder-side counterpart of algUnquant (celt/vq.c alg_quant,
// float build with resynth). It forward-rotates X, searches for the pulse
// vector, range-codes it, then reconstructs X exactly as the decoder will
// (normalise_residual + inverse rotation) so that subsequent bands fold off the
// identical normalised spectrum. Returns the band collapse mask.
func algQuant(X []float64, n, k, spread, b int, enc *entcode.Encoder, gain float64) uint {
	expRotation(X, n, 1, b, k, spread)
	iy := make([]int, n)
	yy := opPVQSearch(X, iy, k, n)
	encodePulses(enc, iy, n, k)
	normaliseResidual(iy, X, n, yy, gain)
	expRotation(X, n, -1, b, k, spread)
	return extractCollapseMask(iy, n, b)
}
