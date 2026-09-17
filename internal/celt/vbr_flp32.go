package celt

// computeVBR mirrors libopus compute_vbr (celt_encoder.c, float build) for an
// encoder without the tonality analysis: the VBR target in eighth bits for
// this frame from the nominal base target, the dynalloc boost, the transient
// estimate, the spectral depth and the temporal VBR follower. equivRate is
// libopus equiv_rate; the surround masking terms are not used (energy_mask
// is nil).
func computeVBR(baseTarget, lm, equivRate, lastCodedBands, C, intensity int, constrainedVBR bool,
	stereoSaving float32, totBoost int, tfEstimate float32, maxDepth, temporalVBR float32,
	analysis *AnalysisInfo, pitchChange bool) int {
	codedBands := lastCodedBands
	if codedBands == 0 {
		codedBands = NumBands48000
	}
	codedBins := int(EBands48000[codedBands]) << uint(lm)
	if C == 2 {
		ib := intensity
		if codedBands < ib {
			ib = codedBands
		}
		codedBins += int(EBands48000[ib]) << uint(lm)
	}
	target := baseTarget
	if analysis.Valid && analysis.Activity < 0.4 {
		target -= int(float32(codedBins<<3) * (float32(0.4) - analysis.Activity))
	}
	// Stereo savings.
	if C == 2 {
		codedStereoBands := intensity
		if codedBands < codedStereoBands {
			codedStereoBands = codedBands
		}
		codedStereoDof := (int(EBands48000[codedStereoBands]) << uint(lm)) - codedStereoBands
		// Maximum fraction of the bits we can save if the signal is mono.
		maxFrac := float32(float32(0.8)*float32(codedStereoDof)) / float32(codedBins)
		if stereoSaving > 1 {
			stereoSaving = 1
		}
		a := float32(maxFrac * float32(target))
		b := float32(float32(stereoSaving-float32(0.1)) * float32(codedStereoDof<<3))
		if b < a {
			a = b
		}
		target -= int(a)
	}
	// Boost the rate according to dynalloc (minus the dynalloc average for
	// calibration).
	target += totBoost - (19 << uint(lm))
	// Apply transient boost, compensating for average boost (SHL32 by one is
	// the identity in the float build).
	target += int(float32(tfEstimate-float32(0.044)) * float32(target))
	// Apply tonality boost (compensating for the average).
	if analysis.Valid {
		tonal := analysis.Tonality - float32(0.15)
		if tonal < 0 {
			tonal = 0
		}
		tonal -= 0.12
		tonalTarget := target + int(float32(float32(codedBins<<3)*float32(1.2))*tonal)
		if pitchChange {
			tonalTarget += int(float32(codedBins<<3) * float32(0.8))
		}
		target = tonalTarget
	}

	{
		bins := int(EBands48000[NumBands48000-2]) << uint(lm)
		floorDepth := int(float32(C*bins<<3) * maxDepth)
		if floorDepth < target>>2 {
			floorDepth = target >> 2
		}
		if floorDepth < target {
			target = floorDepth
		}
	}
	// Make VBR less aggressive for constrained VBR because we can't keep a
	// higher bitrate for long.
	if constrainedVBR {
		target = baseTarget + int(float32(0.67)*float32(target-baseTarget))
	}
	if tfEstimate < 0.2 {
		r := 96000 - equivRate
		if r > 32000 {
			r = 32000
		}
		if r < 0 {
			r = 0
		}
		amount := float32(0.0000031) * float32(r)
		tvbrFactor := temporalVBR * amount
		target += int(tvbrFactor * float32(target))
	}
	// Don't allow more than doubling the rate.
	if target > 2*baseTarget {
		target = 2 * baseTarget
	}
	return target
}
