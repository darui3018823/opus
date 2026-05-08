package celt

import (
	"github.com/darui3018823/opus/internal/entcode"
)

// Post-filter constants from libopus celt_decoder.c / celt.h.
const (
	// COMBFILTER_MINPERIOD = 15 samples (≈ 3200 Hz at 48 kHz)
	combFilterMinPeriod = 15
	// COMBFILTER_MAXPERIOD = 1022 samples (period encoded as 10-bit field → max 1022+15-1)
	combFilterMaxPeriod = 1022
	// The period is coded as an offset from combFilterMinPeriod with range
	// MAX_PERIOD - (COMBFILTER_MINPERIOD-1) = 1024-14 = 1010 values.
	combFilterPeriodRange = combFilterMaxPeriod - combFilterMinPeriod + 1
)

// pfGainTable maps 3-bit gain index (0..7) to the gain scalar used in
// the comb filter.  From libopus celt_decoder.c COMBFILTER_GAIN_TABLE.
var pfGainTable = [8]float64{
	0.09375, // 6/64
	0.125,   // 8/64
	0.15625, // 10/64
	0.1875,  // 12/64
	0.25,    // 16/64
	0.3125,  // 20/64
	0.375,   // 24/64
	0.4375,  // 28/64
}

// PostFilter implements RFC 6716 §5.4.1 comb (pitch) post-filter.
// It is applied after IMDCT in the decoder.
type PostFilter struct {
	period     int        // Pitch period in samples
	gain       [3]float64 // Tap weights [g0, g1, g2]
	prevPeriod int        // Previous frame's period
	prevGain   [3]float64 // Previous frame's tap weights
	// History buffer — must hold at least combFilterMaxPeriod + overlap samples.
	buf []float64
}

// NewPostFilter allocates a PostFilter with an empty history.
func NewPostFilter() *PostFilter {
	return &PostFilter{
		buf: make([]float64, combFilterMaxPeriod+MaxOverlap+2),
	}
}

// Reset clears filter state.
func (pf *PostFilter) Reset() {
	for i := range pf.buf {
		pf.buf[i] = 0
	}
	pf.period = 0
	pf.prevPeriod = 0
	pf.gain = [3]float64{}
	pf.prevGain = [3]float64{}
}

// DecodePostFilterParams reads post-filter parameters from the range decoder.
// Returns (period, taps[3], enabled).
// This matches the reading order in libopus celt_decoder.c:celt_decode_with_ec().
func DecodePostFilterParams(dec *entcode.Decoder, frameSize int) (int, [3]float64, bool) {
	// enabled = ec_dec_bit_logp(dec, 1)
	enabled := dec.DecodeBitLogp(1)
	if !enabled {
		return 0, [3]float64{}, false
	}

	// period = ec_dec_uint(dec, MAX_PERIOD-(COMBFILTER_MINPERIOD-1))
	//        + COMBFILTER_MINPERIOD
	period := int(dec.DecodeUint(uint32(combFilterPeriodRange))) + combFilterMinPeriod

	// gain_index = ec_dec_uint(dec, 8)
	gainIndex := int(dec.DecodeUint(8))
	if gainIndex >= len(pfGainTable) {
		gainIndex = len(pfGainTable) - 1
	}
	g := pfGainTable[gainIndex]

	// Tap weights: libopus uses a 3-tap symmetric filter.
	// From libopus celt_decoder.c:
	//   gain1 = g * 0.09375 / g_max  * g_max  →  taps = g * [0.5, 1.0, 0.5] normalized
	// The actual tap structure in libopus (pitch_filter.c) for the decoder is:
	//   out[n] += g * (in[n-T-1]*0.09375 + in[n-T]*0.125 + in[n-T+1]*0.09375)
	// where the 0.09375 and 0.125 are fixed coefficients scaled by g.
	// Note: 0.09375 = 6/64, 0.125 = 8/64, sum = 20/64 → normalized so centre = g,
	// flanks = g * (6/20) = g * 0.3  — but libopus keeps the raw values.
	taps := [3]float64{g * 0.09375, g * 0.125, g * 0.09375}

	return period, taps, true
}

// Apply applies the post-filter to one frame of decoded samples in-place.
// samples is a mono frame (length = frameSize).
// The history buffer is updated so the next call sees the previous frame.
func (pf *PostFilter) Apply(samples []float64, period int, taps [3]float64) []float64 {
	n := len(samples)
	if period <= 0 || (taps[0] == 0 && taps[1] == 0 && taps[2] == 0) {
		// No filtering — just update history and return unchanged.
		pf.updateHistory(samples)
		return samples
	}

	out := make([]float64, n)
	bufLen := len(pf.buf)

	for i := 0; i < n; i++ {
		// Fetch pitched samples from the combined history+current buffer.
		// Position in the "virtual" stream: bufLen + i is current sample.
		// We need samples at offsets -period-1, -period, -period+1 relative to current.
		s1 := pf.getHistorySample(samples, i-period-1, bufLen)
		s2 := pf.getHistorySample(samples, i-period, bufLen)
		s3 := pf.getHistorySample(samples, i-period+1, bufLen)

		out[i] = samples[i] + taps[0]*s1 + taps[1]*s2 + taps[2]*s3
	}

	pf.updateHistory(samples)
	pf.period = period
	pf.gain = taps
	return out
}

// getHistorySample retrieves sample at index i relative to current frame start.
// Negative indices are looked up in pf.buf (history).
func (pf *PostFilter) getHistorySample(samples []float64, i int, bufLen int) float64 {
	if i < 0 {
		// index into history buffer; most recent sample is at bufLen-1
		idx := bufLen + i
		if idx < 0 || idx >= bufLen {
			return 0
		}
		return pf.buf[idx]
	}
	if i >= len(samples) {
		return 0
	}
	return samples[i]
}

// updateHistory shifts the history buffer and copies the current frame into it.
func (pf *PostFilter) updateHistory(samples []float64) {
	n := len(samples)
	bufLen := len(pf.buf)
	if n >= bufLen {
		// Frame is larger than the history — keep only the tail.
		copy(pf.buf, samples[n-bufLen:])
		return
	}
	// Shift existing history left by n positions.
	copy(pf.buf, pf.buf[n:])
	// Copy new samples into the tail.
	copy(pf.buf[bufLen-n:], samples)
}
