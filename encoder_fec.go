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

// hybridSILKRateTable is compute_silk_rate_for_hybrid's rate_table:
// total rate, then the SILK share for {10 ms, 20 ms} x {no FEC, FEC}.
var hybridSILKRateTable = [...][5]int{
	{0, 0, 0, 0, 0},
	{12000, 10000, 10000, 11000, 11000},
	{16000, 13500, 13500, 15000, 15000},
	{20000, 16000, 16000, 18000, 18000},
	{24000, 18000, 18000, 21000, 21000},
	{32000, 22000, 22000, 28000, 28000},
	{64000, 38000, 38000, 50000, 50000},
}

// computeSILKRateForHybrid ports compute_silk_rate_for_hybrid: the SILK
// bitrate of a hybrid packet, interpolated per channel from the table, with
// the CBR, superwideband and stereo adjustments.
func computeSILKRateForHybrid(rate, bandwidth int, frame20ms, vbr, fec bool, channels int) int {
	rate /= channels
	entry := 1
	if frame20ms {
		entry++
	}
	if fec {
		entry += 2
	}
	n := len(hybridSILKRateTable)
	i := 1
	for ; i < n; i++ {
		if hybridSILKRateTable[i][0] > rate {
			break
		}
	}
	var silkRate int
	if i == n {
		silkRate = hybridSILKRateTable[i-1][entry]
		// For now, just give 50% of the extra bits to SILK.
		silkRate += (rate - hybridSILKRateTable[i-1][0]) / 2
	} else {
		lo, hi := hybridSILKRateTable[i-1][entry], hybridSILKRateTable[i][entry]
		x0, x1 := hybridSILKRateTable[i-1][0], hybridSILKRateTable[i][0]
		silkRate = (lo*(x1-rate) + hi*(rate-x0)) / (x1 - x0)
	}
	if !vbr {
		// Tiny boost to SILK for CBR.
		silkRate += 100
	}
	if bandwidth == framing.BandwidthSuperwideband {
		silkRate += 300
	}
	silkRate *= channels
	// Small adjustment for stereo (calibrated for 32 kb/s).
	if channels == 2 && rate >= 12000 {
		silkRate -= 1000
	}
	return silkRate
}

// stereoVoiceThreshold / stereoMusicThreshold are stereo_voice_threshold and
// stereo_music_threshold: the equivalent rates above which a stereo input is
// coded as two stream channels.
const (
	stereoVoiceThreshold = 19000
	stereoMusicThreshold = 17000
)

// voiceEst is opus_encode_native's voice_est without the tonality analysis
// (which libopus only runs at complexity >= 7): 127 for a voice signal hint,
// 0 for music, 115 for VOIP and 48 otherwise.
func (e *Encoder) voiceEst() int {
	switch e.signalSetting {
	case SignalVoice:
		return 127
	case SignalMusic:
		return 0
	}
	if e.application == ApplicationVOIP {
		return 115
	}
	return 48
}

// decideStreamChannels is the rate-dependent mono/stereo decision for a
// stereo input (st->stream_channels) with its 1000 bps hysteresis, followed
// by the toMono rule: the first packet after a stereo->mono decision is still
// coded stereo with the width collapsed so SILK can downmix smoothly.
func (e *Encoder) decideStreamChannels(frameSize, mode int) {
	if e.channels != 2 {
		e.streamChannels = 1
		e.toMono = false
		return
	}
	if e.streamChannels == 0 {
		e.streamChannels = 2
	}
	if e.forceChannels != ChannelsAuto {
		e.streamChannels = e.forceChannels
	} else {
		equiv := computeEquivRate(e.bitrate, e.channels, e.sampleRate/frameSize, e.rateMode != celt.RateModeCBR, -1, e.complexity, e.packetLossPerc)
		ve := e.voiceEst()
		threshold := stereoMusicThreshold + ((ve * ve * (stereoVoiceThreshold - stereoMusicThreshold)) >> 14)
		if e.streamChannels == 2 {
			threshold -= 1000
		} else {
			threshold += 1000
		}
		if equiv > threshold {
			e.streamChannels = 2
		} else {
			e.streamChannels = 1
		}
	}
	if e.streamChannels == 1 && e.prevStreamChannels == 2 && !e.toMono &&
		mode != framing.ModeCELTOnly && e.prevMode != framing.ModeCELTOnly {
		// Delay the stereo->mono transition by one packet.
		e.toMono = true
		e.streamChannels = 2
	} else {
		e.toMono = false
	}
}

// Bandwidth transition tables (opus_encoder.c): the middle (memoryless)
// threshold and the hysteresis for NB<->MB, MB<->WB, WB<->SWB, SWB<->FB.
var (
	monoVoiceBandwidthThresholds   = [8]int{9000, 700, 9000, 700, 13500, 1000, 14000, 2000}
	monoMusicBandwidthThresholds   = [8]int{9000, 700, 9000, 700, 11000, 1000, 12000, 2000}
	stereoVoiceBandwidthThresholds = [8]int{9000, 700, 9000, 700, 13500, 1000, 14000, 2000}
	stereoMusicBandwidthThresholds = [8]int{9000, 700, 9000, 700, 11000, 1000, 12000, 2000}
)

// decideAutoBandwidth is opus_encode_native's rate-dependent bandwidth
// selection for the automatic setting: the thresholds are interpolated by
// voice_est, walked down from fullband with hysteresis against the previous
// automatic choice (none on the first packet), mediumband is promoted to
// wideband, and the result is capped by the input rate. It returns a framing
// bandwidth constant.
func (e *Encoder) decideAutoBandwidth(equivRate int, first bool) int {
	voice, music := &monoVoiceBandwidthThresholds, &monoMusicBandwidthThresholds
	if e.channels == 2 && e.forceChannels != ChannelsMono {
		voice, music = &stereoVoiceBandwidthThresholds, &stereoMusicBandwidthThresholds
	}
	ve := e.voiceEst()
	var thresholds [8]int
	for i := range thresholds {
		thresholds[i] = music[i] + ((ve * ve * (voice[i] - music[i])) >> 14)
	}
	bandwidth := framing.BandwidthFullband
	for {
		threshold := thresholds[2*(bandwidth-framing.BandwidthMediumband)]
		hysteresis := thresholds[2*(bandwidth-framing.BandwidthMediumband)+1]
		if !first {
			if e.autoBandwidth >= bandwidth {
				threshold -= hysteresis
			} else {
				threshold += hysteresis
			}
		}
		if equivRate >= threshold {
			break
		}
		bandwidth--
		if bandwidth <= framing.BandwidthNarrowband {
			break
		}
	}
	// We don't use mediumband anymore, except when explicitly requested or
	// during mode transitions.
	if bandwidth == framing.BandwidthMediumband {
		bandwidth = framing.BandwidthWideband
	}
	e.autoBandwidth = bandwidth
	// Prevents Opus from wasting bits on frequencies that are above the
	// Nyquist rate of the input signal.
	switch {
	case e.sampleRate <= 8000 && bandwidth > framing.BandwidthNarrowband:
		bandwidth = framing.BandwidthNarrowband
	case e.sampleRate <= 12000 && bandwidth > framing.BandwidthMediumband:
		bandwidth = framing.BandwidthMediumband
	case e.sampleRate <= 16000 && bandwidth > framing.BandwidthWideband:
		bandwidth = framing.BandwidthWideband
	case e.sampleRate <= 24000 && bandwidth > framing.BandwidthSuperwideband:
		bandwidth = framing.BandwidthSuperwideband
	}
	return bandwidth
}

// silkInternalRateForBandwidth is desiredInternalSampleRate: NB -> 8 kHz,
// MB -> 12 kHz, WB and above -> 16 kHz, never above the input rate.
func (e *Encoder) silkInternalRateForBandwidth(bandwidth int) int {
	rate := 16000
	switch bandwidth {
	case framing.BandwidthNarrowband:
		rate = 8000
	case framing.BandwidthMediumband:
		rate = 12000
	}
	if rate > e.sampleRate {
		rate = e.sampleRate
	}
	return rate
}

// selectSILKInternalRate applies the automatic bandwidth decision to the
// SILK internal rate before the first SILK packet after (re)initialisation
// (silk_control_audio_bandwidth with fs_kHz == 0: min(desired, API rate)).
// Once frames have been coded the rate stays: libopus' mid-stream switching
// (the sLP transition filter and its redundancy) is not ported.
func (e *Encoder) selectSILKInternalRate(frameSize int) error {
	if e.silkEncoder == nil || e.forcedBandwidth != BandwidthAuto || e.silkFramesCoded {
		return nil
	}
	equiv := computeEquivRate(e.bitrate, e.streamChannelsOrInput(), e.sampleRate/frameSize, e.rateMode != celt.RateModeCBR, -1, e.complexity, e.packetLossPerc)
	bw := e.decideAutoBandwidth(equiv, e.prevMode < 0)
	if publicBW := silkFramingBWToPublic(bw); bandwidthRank(publicBW) > bandwidthRank(e.maxBandwidth) {
		bw = publicToCeltFramingBW(e.maxBandwidth)
	}
	rate := e.silkInternalRateForBandwidth(bw)
	if rate == e.silkSampleRate {
		return nil
	}
	if err := e.rebuildSILKEncoder(rate); err != nil {
		return err
	}
	return e.applyBitrateSetting(frameSize)
}

func (e *Encoder) streamChannelsOrInput() int {
	if e.channels == 2 && e.streamChannels == 1 {
		return 1
	}
	return e.channels
}

// updateLBRRCoded runs the per-packet FEC decision for a SILK-only or
// hybrid packet coded at bandwidth and pushes LBRR_coded to the SILK encoder.
func (e *Encoder) updateLBRRCoded(mode, bandwidth, frameRate int) {
	if e.silkEncoder == nil {
		return
	}
	channels := e.channels
	if channels == 2 && e.streamChannels == 1 {
		channels = 1
	}
	equiv := computeEquivRate(e.bitrate, channels, frameRate, e.rateMode != celt.RateModeCBR, mode, e.complexity, e.packetLossPerc)
	coded, _ := decideFEC(e.useInbandFEC, e.packetLossPerc, e.lbrrCoded, mode, bandwidth, equiv)
	e.lbrrCoded = coded
	e.silkEncoder.SetLBRRCoded(coded)
}
