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

func (pf *PostFilter) copyFrom(src *PostFilter) {
	if src == nil || src == pf {
		return
	}
	pf.period = src.period
	pf.gain = src.gain
	pf.prevPeriod = src.prevPeriod
	pf.prevGain = src.prevGain
	copy(pf.buf, src.buf)
}

// tapsetIcdf is the ICDF table for tapset, ft=4 (logp=2).
// Matches libopus tapset_icdf = {2, 1, 0} in celt/celt.h.
// P(tapset=0)=1/2, P(tapset=1)=1/4, P(tapset=2)=1/4.
var tapsetIcdf = [3]uint8{2, 1, 0}

// tapGains[tapset][tap] where taps are [g0, g1, g2] normalized to 1.0 gain.
var tapGains = [3][3]float64{
	{0, 1, 0},       // tapset=0: single tap
	{0.5, 1, 0.5},   // tapset=1: 3-tap symmetric, flanks at 0.5
	{0.25, 1, 0.25}, // tapset=2: wider 3-tap
}

// pfCombGains are the synthesis tap weights per tapset, from libopus comb_filter
// (celt/celt.c gains[3][3]). Index [tapset][tap].
var pfCombGains = [3][3]float64{
	{0.3066406250, 0.2170410156, 0.1296386719},
	{0.4638671875, 0.2680664062, 0.0},
	{0.7998046875, 0.1000976562, 0.0},
}

// DecodePostFilterParams reads post-filter parameters from the range decoder,
// matching libopus celt_decode_with_ec exactly (read ONCE, before isTransient):
//
//	if (ec_dec_bit_logp(dec,1)) {
//	    octave = ec_dec_uint(dec,6);
//	    pitch  = (16<<octave)+ec_dec_bits(dec,4+octave)-1;
//	    qg     = ec_dec_bits(dec,3);
//	    if (ec_tell(dec)+2<=total_bits) tapset = ec_dec_icdf(dec,tapset_icdf,2);
//	    gain   = 0.09375*(qg+1);
//	}
//
// The caller is responsible for the `start==0 && ec_tell+16<=total_bits` guard.
// Returns (period, taps[3], enabled).
func DecodePostFilterParams(dec *entcode.Decoder, totalBits, lm int) (int, [3]float64, bool) {
	if !dec.DecodeBitLogp(1) {
		return 0, [3]float64{}, false
	}

	octave := int(dec.DecodeUint(6))
	period := (16 << uint(octave)) + int(dec.DecodeBits(uint(4+octave))) - 1
	qg := int(dec.DecodeBits(3))

	tapset := 0
	if dec.ECTell()+2 <= totalBits {
		tapset = dec.DecodeIcdf(tapsetIcdf[:], 2)
	}
	if tapset >= len(pfCombGains) {
		tapset = 0
	}

	gain := 0.09375 * float64(qg+1)
	taps := [3]float64{
		gain * pfCombGains[tapset][0],
		gain * pfCombGains[tapset][1],
		gain * pfCombGains[tapset][2],
	}
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
