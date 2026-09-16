package celt

import (
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// Float32-faithful ports of the libopus float build's encoder-side energy
// quantisation (celt/quant_bands.c: loss_distortion, quant_coarse_energy_impl,
// quant_coarse_energy, quant_fine_energy, quant_energy_finalise). celt_glog,
// opus_val16 and opus_val32 are all float there, and the fixed-point shift
// macros are identities, so every arithmetic step below rounds to float32.
// The slices are the encoder's float64 storage but hold float32 values.

// lossDistortion mirrors loss_distortion: min(200, Σ (eBands-oldEBands)²).
func lossDistortion(eBands, oldEBands []float64, start, end, nbEBands, C int) float32 {
	var dist float32
	for c := 0; c < C; c++ {
		for i := start; i < end; i++ {
			d := float32(eBands[i+c*nbEBands]) - float32(oldEBands[i+c*nbEBands])
			dist += float32(d * d)
		}
	}
	if dist > 200 {
		return 200
	}
	return dist
}

// quantCoarseEnergyImpl mirrors quant_coarse_energy_impl. It writes the intra
// flag when it fits, codes one residual per band and channel, updates
// oldEBands in place and returns the badness (Σ|qi0-qi|).
func quantCoarseEnergyImpl(enc *entcode.Encoder, start, end int, eBands, oldEBands []float64,
	budget, tell int, probModel []uint8, errOut []float64, C, lm int, intra bool, maxDecay float32, nbEBands int) int {
	badness := 0
	var prev [2]float32
	var coef, beta float32
	if tell+3 <= budget {
		enc.EncodeBitLogp(intra, 3)
	}
	if intra {
		coef = 0
		beta = float32(betaIntra)
	} else {
		beta = float32(betaCoef[lm])
		coef = float32(predCoef[lm])
	}
	for i := start; i < end; i++ {
		for c := 0; c < C; c++ {
			idx := i + c*nbEBands
			x := float32(eBands[idx])
			oldE := float32(oldEBands[idx])
			if oldE < -9 {
				oldE = -9
			}
			f := float32(x-float32(coef*oldE)) - prev[c]
			// Rounding to nearest integer here is really important!
			qi := int(math.Floor(float64(float32(0.5) + f)))
			decayBound := float32(oldEBands[idx])
			if decayBound < -28 {
				decayBound = -28
			}
			decayBound -= maxDecay
			// Prevent the energy from going down too quickly (e.g. for bands
			// that have just one bin).
			if qi < 0 && x < decayBound {
				qi += int(decayBound - x)
				if qi > 0 {
					qi = 0
				}
			}
			qi0 := qi
			// If we don't have enough bits to encode all the energy, just
			// assume something safe.
			tell = enc.ECTell()
			bitsLeft := budget - tell - 3*C*(end-i)
			if i != start && bitsLeft < 30 {
				if bitsLeft < 24 && qi > 1 {
					qi = 1
				}
				if bitsLeft < 16 && qi < -1 {
					qi = -1
				}
			}
			switch {
			case budget-tell >= 15:
				pi := 2 * i
				if pi > 40 {
					pi = 40
				}
				enc.EncodeLaplace(&qi, uint32(probModel[pi])<<7, int(probModel[pi+1])<<6)
			case budget-tell >= 2:
				if qi < -1 {
					qi = -1
				}
				if qi > 1 {
					qi = 1
				}
				enc.EncodeIcdf(encodeSmallEnergySym(qi), smallEnergyIcdf, 2)
			case budget-tell >= 1:
				if qi > 0 {
					qi = 0
				}
				enc.EncodeBitLogp(qi < 0, 1)
			default:
				qi = -1
			}
			errOut[idx] = float64(f - float32(qi))
			if qi0 > qi {
				badness += qi0 - qi
			} else {
				badness += qi - qi0
			}
			q := float32(qi)
			tmp := float32(float32(coef*oldE)+prev[c]) + q
			oldEBands[idx] = float64(tmp)
			prev[c] = float32(prev[c]+q) - float32(beta*q)
		}
	}
	return badness
}

// quantCoarseEnergy mirrors quant_coarse_energy: the intra decision from the
// delayed-intra distortion follower, the optional two-pass intra/inter
// comparison (complexity >= 4) and the follower update. eBands is bandLogE
// (already biased by the caller), oldEBands the encoder's oldBandE, errOut
// receives the coarse residual for the fine quantiser. It returns whether the
// frame was coded intra.
func (e *Encoder) quantCoarseEnergy(enc *entcode.Encoder, start, end, effEnd int, eBands, oldEBands []float64,
	budget int, errOut []float64, C, lm, nbAvailableBytes int, twoPass bool, nbEBands int) bool {
	lm = lmClamp(lm)
	intra := e.forceIntra || (!twoPass && e.delayedIntra > float32(2*C*(end-start)) && nbAvailableBytes > (end-start)*C)
	intraBias := int32(float32(budget) * e.delayedIntra * float32(e.lossRate) / float32(C*512))
	newDistortion := lossDistortion(eBands, oldEBands, start, effEnd, nbEBands, C)

	tell := enc.ECTell()
	if tell+3 > budget {
		twoPass = false
		intra = false
	}

	maxDecay := float32(16)
	if end-start > 10 {
		if d := float32(0.125) * float32(nbAvailableBytes); d < maxDecay {
			maxDecay = d
		}
	}
	encStart := enc.Clone()

	oldEBandsIntra := make([]float64, len(oldEBands))
	copy(oldEBandsIntra, oldEBands)
	errIntra := make([]float64, len(errOut))

	badness1 := 0
	if twoPass || intra {
		badness1 = quantCoarseEnergyImpl(enc, start, end, eBands, oldEBandsIntra, budget, tell,
			eProbModel[lm][1][:], errIntra, C, lm, true, maxDecay, nbEBands)
	}

	if !intra {
		tellIntra := int32(enc.TellFrac())
		encIntra := enc.Clone()
		enc.Restore(encStart)
		interIdx := 0
		badness2 := quantCoarseEnergyImpl(enc, start, end, eBands, oldEBands, budget, tell,
			eProbModel[lm][interIdx][:], errOut, C, lm, false, maxDecay, nbEBands)
		if twoPass && (badness1 < badness2 || (badness1 == badness2 && int32(enc.TellFrac())+intraBias > tellIntra)) {
			enc.Restore(encIntra)
			copy(oldEBands[:C*end], oldEBandsIntra[:C*end])
			copy(errOut[:C*end], errIntra[:C*end])
			intra = true
		}
	} else {
		copy(oldEBands[:C*end], oldEBandsIntra[:C*end])
		copy(errOut[:C*end], errIntra[:C*end])
	}

	if intra {
		e.delayedIntra = newDistortion
	} else {
		pc := float32(predCoef[lm])
		e.delayedIntra = float32(float32(pc*pc)*e.delayedIntra) + newDistortion
	}
	return intra
}

// quantFineEnergy mirrors quant_fine_energy (prev_quant == NULL).
func quantFineEnergy(enc *entcode.Encoder, start, end int, oldEBands, errOut []float64, extraQuant []int, C, nbEBands, storageBits int) {
	for i := start; i < end; i++ {
		if extraQuant[i] <= 0 {
			continue
		}
		if enc.ECTell()+C*extraQuant[i] > storageBits {
			continue
		}
		extra := 1 << uint(extraQuant[i])
		for c := 0; c < C; c++ {
			idx := i + c*nbEBands
			// Has to be without rounding.
			q2 := int(math.Floor(float64(float32(float32(errOut[idx])*1+float32(0.5)) * float32(extra))))
			if q2 > extra-1 {
				q2 = extra - 1
			}
			if q2 < 0 {
				q2 = 0
			}
			enc.EncodeBits(uint32(q2), uint(extraQuant[i]))
			offset := float32(float32(float32(q2)+0.5)*float32(int32(1)<<uint(14-extraQuant[i])))*float32(1.0/16384) - 0.5
			oldEBands[idx] = float64(float32(oldEBands[idx]) + offset)
			errOut[idx] = float64(float32(errOut[idx]) - offset)
		}
	}
}

// quantEnergyFinalise mirrors quant_energy_finalise.
func quantEnergyFinalise(enc *entcode.Encoder, start, end int, oldEBands, errOut []float64, fineQuant, finePriority []int, bitsLeft, C, nbEBands int) {
	for prio := 0; prio < 2; prio++ {
		for i := start; i < end && bitsLeft >= C; i++ {
			if fineQuant[i] >= MaxFineEnergy || finePriority[i] != prio {
				continue
			}
			for c := 0; c < C; c++ {
				idx := i + c*nbEBands
				q2 := 1
				if float32(errOut[idx]) < 0 {
					q2 = 0
				}
				enc.EncodeBits(uint32(q2), 1)
				offset := float32(float32(float32(q2)-0.5)*float32(int32(1)<<uint(14-fineQuant[i]-1))) * float32(1.0/16384)
				oldEBands[idx] = float64(float32(oldEBands[idx]) + offset)
				errOut[idx] = float64(float32(errOut[idx]) - offset)
				bitsLeft--
			}
		}
	}
}
