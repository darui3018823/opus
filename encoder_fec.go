package opus

import (
	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
)

// Opus-layer in-band FEC decision (src/opus_encoder.c): LBRR_coded is
// re-evaluated for every packet from the equivalent rate and the coded
// bandwidth, with hysteresis on the previous decision.

// fecThresholds holds fec_thresholds: the minimum equivalent rate at which
// LBRR is worth coding per bandwidth, each followed by its hysteresis.
var fecThresholds = [...]int{
	12000, 1000, // NB
	14000, 1000, // MB
	16000, 1000, // WB
	20000, 1000, // SWB
	22000, 1000, // FB
}

// computeEquivRate ports compute_equiv_rate: the bitrate corrected for the
// frame-rate overhead, CBR, complexity and (for SILK/hybrid) the LBRR cost
// implied by the packet-loss estimate.
func computeEquivRate(bitrate, channels, frameRate int, vbr bool, mode, complexity, loss int) int {
	equiv := bitrate
	if frameRate > 50 {
		equiv -= (40*channels + 20) * (frameRate - 50)
	}
	if !vbr {
		equiv -= equiv / 12
	}
	equiv = equiv * (90 + complexity) / 100
	switch mode {
	case framing.ModeSILKOnly, framing.ModeHybrid:
		if complexity < 2 {
			equiv = equiv * 4 / 5
		}
		equiv -= equiv * loss / (6*loss + 10)
	case framing.ModeCELTOnly:
		if complexity < 5 {
			equiv = equiv * 9 / 10
		}
	default:
		equiv -= equiv * loss / (12*loss + 20)
	}
	return equiv
}

// decideFEC ports decide_fec. bandwidth is the framing bandwidth the packet
// would be coded at; libopus lowers it until FEC fits when the loss exceeds
// 5 %, and the reduced bandwidth is returned alongside the decision (the Go
// encoder's SILK bandwidth follows the input rate, so the caller may not be
// able to apply it).
func decideFEC(useInbandFEC bool, lossPerc int, lastFEC bool, mode, bandwidth, rate int) (bool, int) {
	if !useInbandFEC || lossPerc == 0 || mode == framing.ModeCELTOnly {
		return false, bandwidth
	}
	orig := bandwidth
	for {
		thres := fecThresholds[2*(bandwidth-framing.BandwidthNarrowband)]
		hysteresis := fecThresholds[2*(bandwidth-framing.BandwidthNarrowband)+1]
		if lastFEC {
			thres -= hysteresis
		} else {
			thres += hysteresis
		}
		loss := lossPerc
		if loss > 25 {
			loss = 25
		}
		thres = int(silkSMULWBMacro(int32(thres*(125-loss)), 655)) // SILK_FIX_CONST(0.01, 16)
		switch {
		case rate > thres:
			return true, bandwidth
		case lossPerc <= 5:
			return false, bandwidth
		case bandwidth > framing.BandwidthNarrowband:
			bandwidth--
		default:
			// No bandwidth can carry FEC at this rate: keep the original.
			return false, orig
		}
	}
}

// updateLBRRCoded runs the per-packet FEC decision for a SILK-only or
// hybrid packet coded at bandwidth and pushes LBRR_coded to the SILK encoder.
func (e *Encoder) updateLBRRCoded(mode, bandwidth, frameRate int) {
	if e.silkEncoder == nil {
		return
	}
	channels := e.channels
	equiv := computeEquivRate(e.bitrate, channels, frameRate, e.rateMode != celt.RateModeCBR, mode, e.complexity, e.packetLossPerc)
	coded, _ := decideFEC(e.useInbandFEC, e.packetLossPerc, e.lbrrCoded, mode, bandwidth, equiv)
	e.lbrrCoded = coded
	e.silkEncoder.SetLBRRCoded(coded)
}
