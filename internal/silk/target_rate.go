package silk

// Per-frame SILK target rate (silk/enc_API.c silk_Encode): the packet's bit
// budget minus the LBRR usage average, spread over the frames, converted to
// bits/second, then reduced by the bits the previous packets spent beyond
// their budget (nBitsExceeded, decaying over BITRESERVOIR_DECAY_TIME_MS) and,
// inside a packet, by the balance of the frames already coded. The result
// selects SNR_dB_Q7 through silk_control_SNR.

const silkBitReservoirDecayTimeMs = 500

// packetBitBudget returns nBits for the packet: bitRate * payloadSize_ms /
// 1000 (integer arithmetic like silk_DIV32_16(silk_MUL(...), 1000)).
func (e *Encoder) packetBitBudget(nFrames int) int {
	return e.bitrate * (e.frameMs * nFrames) / 1000
}

// updateLBRRUsage folds the LBRR bits of this packet into the exponential
// moving average libopus keeps in nBitsUsedLBRR.
func (e *Encoder) updateLBRRUsage(currBits int) {
	switch {
	case currBits < 10:
		e.nBitsUsedLBRR = 0
	case e.nBitsUsedLBRR < 10:
		e.nBitsUsedLBRR = currBits
	default:
		e.nBitsUsedLBRR = (e.nBitsUsedLBRR + currBits) / 2
	}
}

// frameTargetRate computes TargetRate_bps for frame index frame of a packet
// with nFrames frames, tell being the range coder position before the frame.
func (e *Encoder) frameTargetRate(nFrames, frame, tell int) int {
	nBits := e.packetBitBudget(nFrames) - e.nBitsUsedLBRR
	nBits /= nFrames
	target := nBits * 50
	if e.frameMs == 10 {
		target = nBits * 100
	}
	target -= e.nBitsExceeded * 1000 / silkBitReservoirDecayTimeMs
	if frame > 0 {
		bitsBalance := tell - e.nBitsUsedLBRR - nBits*frame
		target -= bitsBalance * 1000 / silkBitReservoirDecayTimeMs
	}
	// Never exceed input bitrate.
	return silkLimit(target, e.bitrate, 5000)
}

// silkLimit is silk_LIMIT: a clamped to the range between the two limits,
// in either order.
func silkLimit(a, limit1, limit2 int) int {
	if limit1 > limit2 {
		return max(limit2, min(a, limit1))
	}
	return max(limit1, min(a, limit2))
}

// finishPacketBitReservoir updates nBitsExceeded once the packet's SILK bits
// are known (silk_Encode: nBytesOut * 8 minus the packet budget, clamped to
// [0, 10000]).
func (e *Encoder) finishPacketBitReservoir(nFrames, tell int) {
	silkBytes := (tell + 7) >> 3
	if e.lastPacketDTX {
		// A DTXed packet counts as zero bytes.
		silkBytes = 0
	}
	e.nBitsExceeded += silkBytes*8 - e.packetBitBudget(nFrames)
	if e.nBitsExceeded < 0 {
		e.nBitsExceeded = 0
	} else if e.nBitsExceeded > 10000 {
		e.nBitsExceeded = 10000
	}
	// Update the flag indicating whether bandwidth switching is allowed:
	// SPEECH_ACTIVITY_DTX_THRES in Q8 relaxed by (1 - thres) /
	// MAX_BANDWIDTH_SWITCH_DELAY_MS per millisecond since the last switch.
	speechActThrForSwitchQ8 := silkSMLAWB(13, 3188, int16(e.timeSinceSwitchAllowedMs))
	if int32(e.speechActivityQ8) < speechActThrForSwitchQ8 {
		e.allowBandwidthSwitch = true
		e.timeSinceSwitchAllowedMs = 0
	} else {
		e.allowBandwidthSwitch = false
		e.timeSinceSwitchAllowedMs += int32(nFrames * e.frameMs)
	}
}

// recordRateTrace stores the frame's rate-control inputs in the frame trace.
func (e *Encoder) recordRateTrace(nFrames, tell, lbrrBits int) {
	e.lastTrace.NBits = (e.packetBitBudget(nFrames) - e.nBitsUsedLBRR) / nFrames
	e.lastTrace.TargetRateBps = e.targetRateBps
	e.lastTrace.NBitsExceeded = e.nBitsExceeded
	e.lastTrace.NBitsUsedLBRR = e.nBitsUsedLBRR
	e.lastTrace.LBRRBits = lbrrBits
	e.lastTrace.Tell = tell
}

// frameSNRdBQ7 returns the frame's SNR_dB_Q7 (silk_control_SNR of the
// per-channel target rate).
func (e *Encoder) frameSNRdBQ7() int {
	if e.channelRateBps > 0 {
		return silkControlSNR(e.channelRateBps, e.sampleRate/1000, e.nSubframes)
	}
	targetRate := e.targetRateBps
	if targetRate == 0 {
		targetRate = e.bitrate
	}
	if n := e.StreamChannels(); n > 0 {
		targetRate /= n
	}
	return silkControlSNR(targetRate, e.sampleRate/1000, e.nSubframes)
}

// TargetRateBps returns the SILK target rate used for the most recent frame.
func (e *Encoder) TargetRateBps() int {
	return e.targetRateBps
}
