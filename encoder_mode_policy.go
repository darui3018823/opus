package opus

import (
	"fmt"
	"math"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
)

// libopus opus_encode_native mode, channel and bandwidth policy for the
// automatic settings: the equivalent rate, voice_est (from the signal hint,
// the application or the tonality analysis), the stereo width follower, the
// SILK/CELT threshold with its VOIP bias and hysteresis, the automatic
// bandwidth thresholds, the caps and the SILK/hybrid choice by bandwidth.

// mode_thresholds[channels-1][voice/music] (opus_encoder.c).
var modeThresholds = [2][2]int{
	{64000, 10000}, // mono
	{44000, 10000}, // stereo
}

// stereoWidthState is libopus StereoWidthState.
type stereoWidthState struct {
	xx, xy, yy    float32
	smoothedWidth float32
	maxFollower   float32
}

// computeStereoWidth mirrors compute_stereo_width (float build): a slowly
// smoothed inter-channel correlation / loudness-difference measure in
// [0, 1] that interpolates the mode thresholds for stereo input.
func (m *stereoWidthState) compute(pcm []float64, frameSize, fs int) float32 {
	frameRate := fs / frameSize
	den := frameRate
	if den < 50 {
		den = 50
	}
	shortAlpha := float32(25) / float32(den)
	var xx, xy, yy float32
	// Unroll by 4 (the last two samples of a 2.5 ms 12 kHz frame are dropped).
	for i := 0; i < frameSize-3; i += 4 {
		var pxx, pxy, pyy float32
		for k := 0; k < 4; k++ {
			x := float32(pcm[2*(i+k)])
			y := float32(pcm[2*(i+k)+1])
			pxx += float32(x * x)
			pxy += float32(x * y)
			pyy += float32(y * y)
		}
		xx += pxx
		xy += pxy
		yy += pyy
	}
	if !(xx < 1e9) || xx != xx || !(yy < 1e9) || yy != yy {
		xy, xx, yy = 0, 0, 0
	}
	m.xx += float32(shortAlpha * (xx - m.xx))
	// Rewritten to avoid overflows on abrupt sign change.
	m.xy = float32((1-shortAlpha)*m.xy) + float32(shortAlpha*xy)
	m.yy += float32(shortAlpha * (yy - m.yy))
	if m.xx < 0 {
		m.xx = 0
	}
	if m.xy < 0 {
		m.xy = 0
	}
	if m.yy < 0 {
		m.yy = 0
	}
	mx := m.xx
	if m.yy > mx {
		mx = m.yy
	}
	if mx > 8e-4 {
		sqrtXX := float32(math.Sqrt(float64(m.xx)))
		sqrtYY := float32(math.Sqrt(float64(m.yy)))
		qrrtXX := float32(math.Sqrt(float64(sqrtXX)))
		qrrtYY := float32(math.Sqrt(float64(sqrtYY)))
		// Inter-channel correlation.
		if p := float32(sqrtXX * sqrtYY); p < m.xy {
			m.xy = p
		}
		corr := m.xy / (float32(1e-15) + float32(sqrtXX*sqrtYY))
		// Approximate loudness difference.
		d := qrrtXX - qrrtYY
		if d < 0 {
			d = -d
		}
		ldiff := d / (float32(float32(1e-15)+qrrtXX) + qrrtYY)
		w := float32(math.Sqrt(float64(float32(1) - float32(corr*corr))))
		if w > 1 {
			w = 1
		}
		width := float32(w * ldiff)
		// Smoothing over one second.
		m.smoothedWidth += (width - m.smoothedWidth) / float32(frameRate)
		// Peak follower.
		f := m.maxFollower - float32(0.02)/float32(frameRate)
		if m.smoothedWidth > f {
			f = m.smoothedWidth
		}
		m.maxFollower = f
	}
	r := float32(20) * m.maxFollower
	if r > 1 {
		r = 1
	}
	return r
}

// modeDecision is the result of decideLibopusMode for one packet.
type modeDecision struct {
	mode      int // framing.ModeSILKOnly / ModeHybrid / ModeCELTOnly
	bandwidth int // framing bandwidth (NB..FB)
	equivRate int
	voiceEst  int
	// Transition handling (libopus policy): the packet carries a 5 ms
	// redundant CELT frame of redundancyBytes; celtToSilk says the previous
	// packet was CELT-only (leading redundancy) rather than this one being
	// the deferred last SILK/hybrid packet before CELT (to_celt, trailing
	// redundancy); silkPrefill asks for the SILK re-init + prefill of a
	// CELT-only -> SILK/hybrid switch.
	redundancy      bool
	celtToSilk      bool
	toCelt          bool
	silkPrefill     bool
	silkPrefill2    bool // silk_bw_switch: re-init keeping the LP transition
	lbrrCoded       bool // decide_fec for the packet (libopus policy)
	redundancyBytes int
	// bitrate is st->bitrate_bps for the packet (rounded to cbr_bytes in CBR).
	bitrate int
}

// libopusVoiceEst is opus_encode_native's voice_est: 127 for a voice hint,
// 0 for music, otherwise the tonality analysis' music probability (through
// voice_ratio, with the previous mode selecting the min/max estimate), and
// without analysis 115 for VOIP and 48 for the other applications.
func (e *Encoder) libopusVoiceEst(analysis celt.AnalysisInfo) int {
	switch e.signalSetting {
	case SignalVoice:
		return 127
	case SignalMusic:
		return 0
	}
	if analysis.Valid {
		var prob float32
		switch {
		case e.prevMode < 0:
			prob = analysis.MusicProb
		case e.prevMode == framing.ModeCELTOnly:
			prob = analysis.MusicProbMax
		default:
			prob = analysis.MusicProbMin
		}
		voiceRatio := int(math.Floor(0.5 + float64(float32(100)*(1-prob))))
		ve := voiceRatio * 327 >> 8
		// For AUDIO, never be more than 90% confident of having speech.
		if e.application == ApplicationAudio && ve > 115 {
			ve = 115
		}
		return ve
	}
	if e.application == ApplicationVOIP {
		return 115
	}
	return 48
}

// libopusDetectedBandwidth maps the analysis bandwidth to st->detected_bandwidth
// (a framing bandwidth, or -1 when the analysis is off).
func libopusDetectedBandwidth(analysis celt.AnalysisInfo) int {
	if !analysis.Valid {
		return -1
	}
	switch {
	case analysis.Bandwidth <= 12:
		return framing.BandwidthNarrowband
	case analysis.Bandwidth <= 14:
		return framing.BandwidthMediumband
	case analysis.Bandwidth <= 16:
		return framing.BandwidthWideband
	case analysis.Bandwidth <= 18:
		return framing.BandwidthSuperwideband
	default:
		return framing.BandwidthFullband
	}
}

// decideLibopusMode mirrors the mode / bandwidth part of opus_encode_native
// for the automatic mode: it must run after the stream channel decision and
// receives the raw (unconditioned) input for the stereo width. maxDataBytes
// is the packet size bound (cbr_bytes in CBR, 1276 otherwise); the
// resulting bandwidth is persisted as st->bandwidth for SILK/hybrid packets.
func (e *Encoder) decideLibopusMode(raw []float64, frameSize, maxDataBytes int, analysis celt.AnalysisInfo) modeDecision {
	frameRate := e.sampleRate / frameSize
	vbr := e.rateMode != celt.RateModeCBR
	bitrate := e.bitrate
	if !vbr {
		// CBR: the bitrate is rounded to the packet size.
		bitrate = bitsToBitrate(maxDataBytes*8, e.sampleRate, frameSize)
	}
	streamChannels := e.streamChannelsOrInput()
	equivRate := computeEquivRate(bitrate, streamChannels, frameRate, vbr, -1, e.complexity, e.packetLossPerc)
	voiceEst := e.libopusVoiceEst(analysis)
	maxRate := bitsToBitrate(maxDataBytes*8, e.sampleRate, frameSize)

	var mode int
	switch {
	case e.application == ApplicationRestrictedLowDelay:
		mode = framing.ModeCELTOnly
	default:
		var stereoWidth float32
		if e.channels == 2 && e.forceChannels != ChannelsMono {
			stereoWidth = e.stereoWidth.compute(raw, frameSize, e.sampleRate)
		}
		// Interpolate based on stereo width, then on the speech/music probability.
		modeVoice := int(float32(float32(1-stereoWidth)*float32(modeThresholds[0][0])) + float32(stereoWidth*float32(modeThresholds[1][0])))
		modeMusic := int(float32(float32(1-stereoWidth)*float32(modeThresholds[1][1])) + float32(stereoWidth*float32(modeThresholds[1][1])))
		threshold := modeMusic + ((voiceEst * voiceEst * (modeVoice - modeMusic)) >> 14)
		// Bias towards SILK for VoIP because of some useful features.
		if e.application == ApplicationVOIP {
			threshold += 8000
		}
		// Hysteresis.
		if e.prevMode == framing.ModeCELTOnly {
			threshold -= 4000
		} else if e.prevMode >= 0 {
			threshold += 4000
		}
		if equivRate >= threshold {
			mode = framing.ModeCELTOnly
		} else {
			mode = framing.ModeSILKOnly
		}
		// When FEC is enabled and there's enough packet loss, use SILK.
		if e.useInbandFEC && e.packetLossPerc > (128-voiceEst)>>4 {
			mode = framing.ModeSILKOnly
		}
		// When encoding voice and DTX is enabled but the generalized DTX cannot
		// be used, use SILK in order to make use of its DTX.
		if e.dtx && !analysis.Valid && voiceEst > 100 {
			mode = framing.ModeSILKOnly
		}
		// If max_data_bytes represents less than 6 kb/s, switch to CELT-only mode.
		minRate := 6000
		if frameRate > 50 {
			minRate = 9000
		}
		if maxDataBytes < bitrateToBits(minRate, e.sampleRate, frameSize)/8 {
			mode = framing.ModeCELTOnly
		}
	}
	if frameSize < e.sampleRate/100 {
		mode = framing.ModeCELTOnly
	}

	// Mode transitions to or from CELT-only carry a redundant CELT frame; a
	// switch to CELT-only is deferred by one packet (the last SILK/hybrid
	// packet, to_celt) when the frame is at least 10 ms.
	var d modeDecision
	if e.prevMode >= 0 && ((mode != framing.ModeCELTOnly && e.prevMode == framing.ModeCELTOnly) ||
		(mode == framing.ModeCELTOnly && e.prevMode != framing.ModeCELTOnly)) {
		d.redundancy = true
		d.celtToSilk = mode != framing.ModeCELTOnly
		if !d.celtToSilk {
			if frameSize >= e.sampleRate/100 {
				mode = e.prevMode
				d.toCelt = true
			} else {
				d.redundancy = false
			}
		}
	}
	if mode != framing.ModeCELTOnly && e.prevMode == framing.ModeCELTOnly {
		d.silkPrefill = true
	}

	// Update equivalent rate with mode decision.
	equivRate = computeEquivRate(bitrate, streamChannels, frameRate, vbr, mode, e.complexity, e.packetLossPerc)

	// Automatic (rate-dependent) bandwidth selection: every CELT-only packet,
	// the first packet, and whenever SILK's speech activity allows a switch.
	first := e.prevMode < 0
	bandwidth := e.libopusBandwidth
	allowSwitch := e.silkEncoder != nil && e.silkEncoder.AllowBandwidthSwitch()
	if mode == framing.ModeCELTOnly || first || bandwidth < 0 || allowSwitch {
		bandwidth = e.decideAutoBandwidthVE(equivRate, first, voiceEst)
		// Prevents any transition to SWB/FB until the SILK layer has fully
		// switched to WB mode and turned the variable LP filter off.
		if !first && mode != framing.ModeCELTOnly && e.silkSampleRate != 16000 && bandwidth > framing.BandwidthWideband {
			bandwidth = framing.BandwidthWideband
		}
	}
	if maxBW := publicToCeltFramingBW(e.maxBandwidth); e.maxBandwidth != BandwidthAuto && bandwidth > maxBW {
		bandwidth = maxBW
	}
	if e.forcedBandwidth != BandwidthAuto {
		bandwidth = publicToFramingBW(e.forcedBandwidth)
	}
	// This prevents us from using hybrid at unsafe CBR/max rates.
	if mode != framing.ModeCELTOnly && maxRate < 15000 && bandwidth > framing.BandwidthWideband {
		bandwidth = framing.BandwidthWideband
	}
	// Prevents Opus from wasting bits on frequencies above the input Nyquist.
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
	// Use detected bandwidth to reduce the encoded bandwidth.
	if detected := libopusDetectedBandwidth(analysis); detected >= 0 && e.forcedBandwidth == BandwidthAuto {
		var minDetected int
		switch {
		case equivRate <= 18000*streamChannels && mode == framing.ModeCELTOnly:
			minDetected = framing.BandwidthNarrowband
		case equivRate <= 24000*streamChannels && mode == framing.ModeCELTOnly:
			minDetected = framing.BandwidthMediumband
		case equivRate <= 30000*streamChannels:
			minDetected = framing.BandwidthWideband
		case equivRate <= 44000*streamChannels:
			minDetected = framing.BandwidthSuperwideband
		default:
			minDetected = framing.BandwidthFullband
		}
		if detected < minDetected {
			detected = minDetected
		}
		if detected < bandwidth {
			bandwidth = detected
		}
	}
	// decide_fec: the packet's FEC flag, which also narrows the bandwidth
	// until the rate can carry the redundant frames when the loss is over
	// 5 % (opus_encode_native calls it with the packet's equivalent rate,
	// and a CELT-only packet always clears it).
	d.lbrrCoded, bandwidth = decideFEC(e.useInbandFEC, e.packetLossPerc, e.lbrrCoded, mode, bandwidth, equivRate)
	e.lbrrCoded = d.lbrrCoded

	// CELT mode doesn't support mediumband, use wideband instead.
	if mode == framing.ModeCELTOnly && bandwidth == framing.BandwidthMediumband {
		bandwidth = framing.BandwidthWideband
	}
	// Chooses the appropriate mode for speech; never switch to/from
	// CELT-only here.
	if mode == framing.ModeSILKOnly && bandwidth > framing.BandwidthWideband {
		mode = framing.ModeHybrid
	}
	if mode == framing.ModeHybrid && bandwidth <= framing.BandwidthWideband {
		mode = framing.ModeSILKOnly
	}
	e.libopusBandwidth = bandwidth
	// If we decided to go with CELT, make sure redundancy is off, no matter
	// what we decided earlier; otherwise size it (none when too small).
	if mode == framing.ModeCELTOnly {
		d.redundancy = false
	}
	if d.redundancy {
		d.redundancyBytes = computeRedundancyBytes(maxDataBytes, bitrate, frameRate, streamChannels)
		if d.redundancyBytes == 0 {
			d.redundancy = false
		}
	}
	d.mode, d.bandwidth, d.equivRate, d.voiceEst = mode, bandwidth, equivRate, voiceEst
	d.bitrate = bitrate
	return d
}

// publicToFramingBW maps a public bandwidth setting to the framing constant,
// keeping mediumband (the SILK path can code it).
func publicToFramingBW(pub int) int {
	switch pub {
	case BandwidthNarrowband:
		return framing.BandwidthNarrowband
	case BandwidthMediumband:
		return framing.BandwidthMediumband
	case BandwidthWideband:
		return framing.BandwidthWideband
	case BandwidthSuperWideband:
		return framing.BandwidthSuperwideband
	default:
		return framing.BandwidthFullband
	}
}

// decideAutoBandwidthVE is decideAutoBandwidth with an explicit voice_est
// (the analysis-aware value), without the input-rate caps (applied by the
// caller in libopus order).
func (e *Encoder) decideAutoBandwidthVE(equivRate int, first bool, ve int) int {
	voice, music := &monoVoiceBandwidthThresholds, &monoMusicBandwidthThresholds
	if e.channels == 2 && e.forceChannels != ChannelsMono {
		voice, music = &stereoVoiceBandwidthThresholds, &stereoMusicBandwidthThresholds
	}
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
	if bandwidth == framing.BandwidthMediumband {
		bandwidth = framing.BandwidthWideband
	}
	e.autoBandwidth = bandwidth
	return bandwidth
}

// goModeDecision is the Go encoder's own mode selection (the bitrate
// boundaries of shouldEncodeSILKOnly / shouldEncodeHybrid and the
// signal-analysis bandwidth narrowing), returned in the modeDecision form.
// A bandwidth of -1 means "decide later along the Go path".
func (e *Encoder) goModeDecision(raw []float64, frameSize, nFrames int) modeDecision {
	if e.shouldEncodeSILKOnly() {
		return modeDecision{mode: framing.ModeSILKOnly, bandwidth: -1}
	}
	if e.shouldEncodeHybrid(nFrames) {
		bw := e.narrowAutoHybridBandwidth(raw, e.selectHybridBandwidth())
		if bw == framing.BandwidthSuperwideband || bw == framing.BandwidthFullband {
			return modeDecision{mode: framing.ModeHybrid, bandwidth: bw}
		}
	}
	return modeDecision{mode: framing.ModeCELTOnly, bandwidth: -1}
}

// ModePolicy selects the rules behind the encoder's automatic decisions:
// the coding mode (SILK, hybrid, CELT), the coded stream channels and the
// bandwidth, together with the transitions between them. Every policy
// produces standard Opus packets; only the encoder's choices differ.
type ModePolicy int

const (
	// ModePolicyLegacy is the Go encoder's historical policy: bitrate
	// boundaries for the mode and signal-analysis bandwidth narrowing. It is
	// NewEncoder's default.
	ModePolicyLegacy ModePolicy = iota
	// ModePolicyLibopus follows libopus 1.6.1's opus_encode_native: mode
	// thresholds interpolated by the stereo width and the voice estimate,
	// the VOIP bias and hysteresis, the rate-dependent bandwidth thresholds,
	// the tonality analysis' detected bandwidth, decide_fec, the transition
	// redundancy and prefills, and a sub-48 kHz CELT input zero-stuffed as
	// libopus does. With the same settings and input its packets are
	// byte-identical to libopus 1.6.1 (float build without SIMD kernels).
	// EncoderProfileLibopus selects it.
	ModePolicyLibopus
)

// SetModePolicy selects the automatic decision policy.
//
// Call it before the first Encode. The policies keep different per-stream
// state, so changing the policy after encoding has started resets the
// stream state exactly as Reset does (every setting is kept): the next
// packet is coded as the first packet of a new stream. Setting the current
// policy again is a no-op. An unknown policy returns ErrBadArg and leaves
// the encoder unchanged.
func (e *Encoder) SetModePolicy(policy ModePolicy) error {
	var libopus bool
	switch policy {
	case ModePolicyLegacy:
	case ModePolicyLibopus:
		libopus = true
	default:
		return fmt.Errorf("%w: unsupported mode policy %d", ErrBadArg, policy)
	}
	if libopus == e.libopusModePolicy {
		return nil
	}
	e.libopusModePolicy = libopus
	if e.streamStarted {
		return e.Reset()
	}
	return nil
}

// ModePolicy reports the automatic decision policy.
func (e *Encoder) ModePolicy() ModePolicy {
	if e.libopusModePolicy {
		return ModePolicyLibopus
	}
	return ModePolicyLegacy
}

// LibopusModePolicy reports whether the libopus mode policy is selected.
func (e *Encoder) LibopusModePolicy() bool { return e.libopusModePolicy }
