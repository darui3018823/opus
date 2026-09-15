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
	if target > e.bitrate {
		target = e.bitrate
	}
	if target < 5000 {
		target = 5000
	}
	return target
}

// finishPacketBitReservoir updates nBitsExceeded once the packet's SILK bits
// are known (silk_Encode: nBytesOut * 8 minus the packet budget, clamped to
// [0, 10000]).
func (e *Encoder) finishPacketBitReservoir(nFrames, tell int) {
	silkBytes := (tell + 7) >> 3
	e.nBitsExceeded += silkBytes*8 - e.packetBitBudget(nFrames)
	if e.nBitsExceeded < 0 {
		e.nBitsExceeded = 0
	} else if e.nBitsExceeded > 10000 {
		e.nBitsExceeded = 10000
	}
}

// TargetRateBps returns the SILK target rate used for the most recent frame.
func (e *Encoder) TargetRateBps() int {
	return e.targetRateBps
}
