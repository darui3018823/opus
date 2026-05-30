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
	period     int     // Current post-filter period in samples
	gain       float64 // Current post-filter gain
	tapset     int     // Current post-filter tapset
	prevPeriod int     // Previous post-filter period in samples
	prevGain   float64 // Previous post-filter gain
	prevTapset int     // Previous post-filter tapset
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
	pf.gain = 0
	pf.prevGain = 0
	pf.tapset = 0
	pf.prevTapset = 0
}

func (pf *PostFilter) copyFrom(src *PostFilter) {
	if src == nil || src == pf {
		return
	}
	pf.period = src.period
	pf.gain = src.gain
	pf.tapset = src.tapset
	pf.prevPeriod = src.prevPeriod
	pf.prevGain = src.prevGain
	pf.prevTapset = src.prevTapset
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
// Returns (period, gain, tapset, enabled).
func DecodePostFilterParams(dec *entcode.Decoder, totalBits, lm int) (int, float64, int, bool) {
	if !dec.DecodeBitLogp(1) {
		return 0, 0, 0, false
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
	return period, gain, tapset, true
}

// Apply applies libopus' CELT comb post-filter to one frame of decoded samples
// in-place. samples is a mono frame (length = frameSize). shortMdctSize is
// mode->shortMdctSize (N/M), and lm controls whether the decoded packet's new
// parameters are applied in the second segment or deferred to the next frame.
func (pf *PostFilter) Apply(samples []float64, period int, gain float64, tapset int, shortMdctSize, lm int, window []float64) []float64 {
	n := len(samples)
	if tapset < 0 || tapset >= len(pfCombGains) {
		tapset = 0
	}

	if pf.prevGain == 0 && pf.gain == 0 && (lm == 0 || gain == 0) {
		pf.advanceState(period, gain, tapset, lm)
		pf.updateHistory(samples)
		return samples
	}

	if shortMdctSize > n {
		shortMdctSize = n
	}

	pf.period = max(pf.period, combFilterMinPeriod)
	pf.prevPeriod = max(pf.prevPeriod, combFilterMinPeriod)

	pf.combFilter(samples, 0, shortMdctSize,
		pf.prevPeriod, pf.period,
		pf.prevGain, pf.gain,
		pf.prevTapset, pf.tapset,
		window, len(window))

	if lm != 0 && shortMdctSize < n {
		pf.combFilter(samples, shortMdctSize, n-shortMdctSize,
			pf.period, period,
			pf.gain, gain,
			pf.tapset, tapset,
			window, len(window))
	}

	pf.updateHistory(samples)
	pf.advanceState(period, gain, tapset, lm)
	return samples
}

func (pf *PostFilter) advanceState(period int, gain float64, tapset int, lm int) {
	pf.prevPeriod = pf.period
	pf.prevGain = pf.gain
	pf.prevTapset = pf.tapset
	pf.period = period
	pf.gain = gain
	pf.tapset = tapset
	if lm != 0 {
		pf.prevPeriod = pf.period
		pf.prevGain = pf.gain
		pf.prevTapset = pf.tapset
	}
}

func (pf *PostFilter) combFilter(samples []float64, offset, n, period0, period1 int, gain0, gain1 float64, tapset0, tapset1 int, window []float64, overlap int) {
	if n <= 0 {
		return
	}
	if gain0 == 0 && gain1 == 0 {
		return
	}
	period0 = max(period0, combFilterMinPeriod)
	period1 = max(period1, combFilterMinPeriod)
	if tapset0 < 0 || tapset0 >= len(pfCombGains) {
		tapset0 = 0
	}
	if tapset1 < 0 || tapset1 >= len(pfCombGains) {
		tapset1 = 0
	}

	g00 := gain0 * pfCombGains[tapset0][0]
	g01 := gain0 * pfCombGains[tapset0][1]
	g02 := gain0 * pfCombGains[tapset0][2]
	g10 := gain1 * pfCombGains[tapset1][0]
	g11 := gain1 * pfCombGains[tapset1][1]
	g12 := gain1 * pfCombGains[tapset1][2]

	if gain0 == gain1 && period0 == period1 && tapset0 == tapset1 {
		overlap = 0
	}
	if overlap > n {
		overlap = n
	}
	if overlap > len(window) {
		overlap = len(window)
	}

	x1 := pf.getHistorySample(samples, offset-period1+1)
	x2 := pf.getHistorySample(samples, offset-period1)
	x3 := pf.getHistorySample(samples, offset-period1-1)
	x4 := pf.getHistorySample(samples, offset-period1-2)
	for i := 0; i < overlap; i++ {
		pos := offset + i
		x0 := pf.getHistorySample(samples, pos-period1+2)
		f := window[i] * window[i]
		samples[pos] = pf.getHistorySample(samples, pos) +
			(1-f)*g00*pf.getHistorySample(samples, pos-period0) +
			(1-f)*g01*(pf.getHistorySample(samples, pos-period0+1)+pf.getHistorySample(samples, pos-period0-1)) +
			(1-f)*g02*(pf.getHistorySample(samples, pos-period0+2)+pf.getHistorySample(samples, pos-period0-2)) +
			f*g10*x2 +
			f*g11*(x1+x3) +
			f*g12*(x0+x4)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
	if gain1 == 0 {
		return
	}

	pf.combFilterConst(samples, offset+overlap, n-overlap, period1, g10, g11, g12)
}

func (pf *PostFilter) combFilterConst(samples []float64, offset, n, period int, g0, g1, g2 float64) {
	if n <= 0 {
		return
	}
	x4 := pf.getHistorySample(samples, offset-period-2)
	x3 := pf.getHistorySample(samples, offset-period-1)
	x2 := pf.getHistorySample(samples, offset-period)
	x1 := pf.getHistorySample(samples, offset-period+1)
	for i := 0; i < n; i++ {
		pos := offset + i
		x0 := pf.getHistorySample(samples, pos-period+2)
		samples[pos] = pf.getHistorySample(samples, pos) + g0*x2 + g1*(x1+x3) + g2*(x0+x4)
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

// getHistorySample retrieves sample at index i relative to current frame start.
// Negative indices are looked up in pf.buf (history).
func (pf *PostFilter) getHistorySample(samples []float64, i int) float64 {
	if i < 0 {
		// index into history buffer; most recent sample is at bufLen-1
		bufLen := len(pf.buf)
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
