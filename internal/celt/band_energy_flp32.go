package celt

import "math"

// Float32-faithful ports of the libopus float build's band analysis
// (celt/bands.c compute_band_energies / normalise_bands and
// celt/quant_bands.c amp2Log2). libopus is built without FLOAT_APPROX, so
// celt_sqrt is (float)sqrt(x) and celt_log2 is (float)(log2(e)*log(x)).

// bandEnergy32 mirrors compute_band_energies: 1e-27f plus the sequential
// float32 inner product of the band's MDCT coefficients, then celt_sqrt.
func bandEnergy32(coeffs []float64) float64 {
	sum := float32(1e-27)
	for _, v := range coeffs {
		x := float32(v)
		sum += float32(x * x)
	}
	return float64(float32(math.Sqrt(float64(sum))))
}

// celtLog2F32 is libopus' non-FLOAT_APPROX celt_log2:
// (float)(1.442695040888963387*log(x)).
func celtLog2F32(x float64) float64 {
	return float64(float32(1.442695040888963387 * math.Log(float64(float32(x)))))
}

// amp2Log2Band mirrors one amp2Log2 band: celt_log2(bandE) - eMeans[i] in
// float32.
func amp2Log2Band(bandE float64, band int) float64 {
	return float64(float32(celtLog2F32(bandE)) - float32(EMean(band)))
}

// normaliseGain32 mirrors normalise_bands' per-band gain 1.f/(1e-27f+bandE).
func normaliseGain32(bandE float64) float64 {
	return float64(float32(1) / (float32(1e-27) + float32(bandE)))
}
