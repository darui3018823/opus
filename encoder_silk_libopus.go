package opus

import (
	"fmt"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/entcode"
	"github.com/darui3018823/opus/internal/silk"
)

// silkInternalRateControl runs the SILK internal sample rate machinery of
// silk_Encode for the packet about to be coded (SILK-only or hybrid) under
// the libopus policy: a CELT-only -> SILK switch re-initialises the encoder
// at the rate of the decided bandwidth and prefills it (prefill 1); a
// bandwidth switch prepared by the previous packet (silk_bw_switch,
// prefill 2) lets silk_control_audio_bandwidth move the rate one step,
// re-initialises the encoder at that rate keeping the transition filter,
// and prefills it; then the main call's control pass runs. It returns
// switchReady for the packet (the Opus layer then appends a trailing
// redundant CELT frame and arms silk_bw_switch). The caller must have set
// the SILK bitrate, maxBits and stream channels; they are re-applied to a
// re-initialised encoder.
func (e *Encoder) silkInternalRateControl(frameSize, desiredRate int, silkPrefill []float64, prefill2 bool,
	silkRate, silkMaxBits, streamChannels int) (switchReady bool, err error) {
	payloadMs := frameSize * 1000 / e.sampleRate
	reapply := func() error {
		if err := e.applyBitrateSetting(frameSize); err != nil {
			return err
		}
		if err := e.silkEncoder.SetBitrate(silkRate); err != nil {
			return err
		}
		e.silkEncoder.SetMaxBits(silkMaxBits)
		e.silkEncoder.SetStreamChannels(streamChannels, e.toMono)
		e.silkEncoder.SetLBRRCoded(e.lbrrCoded)
		e.silkEncoder.SetInternalRateLimits(e.silkMinInternalRate, e.silkMaxInternalRate)
		return nil
	}
	e.silkEncoder.SetInternalRateLimits(e.silkMinInternalRate, e.silkMaxInternalRate)
	if silkPrefill != nil {
		// silk_Encode(prefill): the channel states are re-initialised (the
		// top-level silk_encoder state stays); prefill 2 first runs the
		// control pass with opusCanSwitch on the encoder that ran the
		// transition (libopus: on the re-initialised state with saved_fs_kHz
		// as the previous rate) and keeps the transition filter, prefill 1
		// starts it afresh.
		rate := e.silkSampleRate
		if prefill2 {
			fs, _ := e.silkEncoder.ControlAudioBandwidth(desiredRate, true, payloadMs)
			rate = fs * 1000
		}
		old := e.silkEncoder
		oldPrevChannels := e.silkPrevChannels
		// The prefill re-initialises only the nChannelsInternal channel
		// states: on a stereo -> mono switch the side channel keeps its
		// resampler and inputBuf at the old rate, and the first mono frame
		// averages with them.
		var oldSideRes *silk.Resampler
		var oldSideBuf []int16
		if streamChannels == 1 && oldPrevChannels == 2 && len(e.silkInputResamplers) > 1 {
			oldSideRes = e.silkInputResamplers[1]
			oldSideBuf = old.SideInputBuf()
			if oldSideBuf == nil {
				oldSideBuf = []int16{} // never written: zeros
			}
		}
		if err := e.rebuildSILKEncoder(rate); err != nil {
			return false, err
		}
		if err := reapply(); err != nil {
			return false, err
		}
		e.silkEncoder.CarryPacketState(old)
		if !prefill2 {
			e.silkEncoder.SetLPState(silk.LPState{})
		}
		e.silkPrevChannels = oldPrevChannels
		if oldSideRes != nil && len(e.silkInputResamplers) > 1 {
			e.silkInputResamplers[1] = oldSideRes
			e.silkStaleSideBuf = oldSideBuf
		}
		e.silkEncoder.Prefill(e.silkInput(silkPrefill, e.silkSampleRate/100*streamChannels))
		e.silkStaleSideBuf = nil
		// silk_Encode ends every call, the prefill included, with
		// nPrevChannelsInternal = nChannelsInternal.
		e.silkPrevChannels = streamChannels
	}
	// The main call's control pass. silk_mode.opusCanSwitch keeps the
	// previous packet's switchReady && !nonfinal_frame (cleared after a
	// prefill): when the redundant frame of a prepared switch did not fit,
	// no prefill runs and this pass switches the rate itself.
	canSwitch := e.silkOpusCanSwitch && silkPrefill == nil
	fs, ready := e.silkEncoder.ControlAudioBandwidth(desiredRate, canSwitch, payloadMs)
	if fs*1000 != e.silkSampleRate {
		// A switch the Opus layer allowed, or a rate above
		// maxInternalSampleRate (or below the minimum), which the control
		// pass enforces at once.
		if err := e.switchSILKRateMidStream(fs*1000, reapply); err != nil {
			return false, err
		}
	}
	return ready, nil
}

// switchSILKRateMidStream re-creates the SILK encoder at rate the way
// silk_control_encoder switches without a prefill: silk_setup_resamplers
// and silk_setup_fs keep most of the channel state and resample the input
// history (see silk.Encoder.CarryRateSwitchState).
func (e *Encoder) switchSILKRateMidStream(rate int, reapply func() error) error {
	old := e.silkEncoder
	prevChannels := e.silkPrevChannels
	if err := e.rebuildSILKEncoder(rate); err != nil {
		return err
	}
	if err := reapply(); err != nil {
		return err
	}
	res, err := e.silkEncoder.CarryRateSwitchState(old)
	if err != nil {
		return err
	}
	e.silkPrevChannels = prevChannels
	for ch, r := range res {
		if r != nil && ch < len(e.silkInputResamplers) {
			e.silkInputResamplers[ch] = r
		}
	}
	return nil
}

// encodeSILKOnlyPacketLibopus codes one 20 ms SILK-only packet the way
// opus_encode_native does under the libopus policy: the SILK rate is
// bits_target (minus the redundancy bytes and the TOC), silk_mode.maxBits
// leaves room for a redundant frame, the internal sample rate follows
// silk_control_audio_bandwidth (with the re-init + prefill of a switch),
// and the packet is SILK's bytes plus an optional 5 ms redundant CELT frame
// (celt_to_silk after CELT-only; trailing before a switch to CELT-only or
// before a SILK bandwidth switch). CBR packets are padded to cbr_bytes. A
// 40 or 60 ms packet is one native SILK packet of nFrames frames.
func (e *Encoder) encodeSILKOnlyPacketLibopus(pcm, celtPCM []float64, nFrames int, d modeDecision, maxDataBytes int,
	silkPrefill []float64, prefill2 bool) ([]byte, error) {
	frameSize := e.silkUnitFrameSize() * nFrames
	frameRate := e.sampleRate / frameSize
	streamChannels := e.streamChannelsOrInput()
	desiredRate := e.silkInternalRateForBandwidth(d.bandwidth)
	// A small packet budget caps the SILK internal rate (opus_encode_native:
	// effective_max_rate, 2/3 of it above 50 frames/s).
	e.silkMinInternalRate, e.silkMaxInternalRate = 8000, 16000
	effectiveMaxRate := bitsToBitrate(maxDataBytes*8, e.sampleRate, frameSize)
	if frameRate > 50 {
		effectiveMaxRate = effectiveMaxRate * 2 / 3
	}
	if effectiveMaxRate < 8000 {
		e.silkMaxInternalRate = 12000
		desiredRate = min(desiredRate, 12000)
	}
	if effectiveMaxRate < 7000 {
		e.silkMaxInternalRate = 8000
		desiredRate = min(desiredRate, 8000)
	}
	if !e.silkFramesCoded && e.silkSampleRate != desiredRate {
		// A fresh encoder starts at the rate of the decided bandwidth
		// (silk_control_audio_bandwidth: fs = min(desired, API)).
		if err := e.rebuildSILKEncoder(desiredRate); err != nil {
			return nil, err
		}
		if err := e.applyBitrateSetting(frameSize); err != nil {
			return nil, err
		}
	}
	e.applyLBRRCoded(d.lbrrCoded)

	// bits_target = min(8*(max_data_bytes - redundancy_bytes),
	// bitrate_to_bits(bitrate)) - 8; SILK gets all of it.
	bits := bitrateToBits(d.bitrate, e.sampleRate, frameSize)
	if b := 8 * (maxDataBytes - d.redundancyBytes); b < bits {
		bits = b
	}
	silkRate := e.surroundSILKRate(bitsToBitrate(bits-8, e.sampleRate, frameSize), d.bandwidth)
	if silkRate < 5000 && !e.libopusModePolicy {
		silkRate = 5000
	}
	if err := e.silkEncoder.SetBitrate(silkRate); err != nil {
		return nil, err
	}
	// Max bits for SILK, counting ToC and redundancy bytes (and the
	// celt_to_silk bit).
	silkMaxBits := (maxDataBytes - 1) * 8
	if d.redundancy && d.redundancyBytes >= 2 {
		silkMaxBits -= d.redundancyBytes*8 + 1
	}
	e.silkEncoder.SetMaxBits(silkMaxBits)
	defer e.silkEncoder.SetMaxBits(0)
	if e.channels == 2 && streamChannels == 2 && e.silkPrevChannels == 1 && len(e.silkInputResamplers) > 1 {
		// Mono -> stereo: the side channel's resampler restarts from the
		// mono channel's state (silk_Encode).
		e.silkInputResamplers[1].CopyStateFrom(e.silkInputResamplers[0])
	}
	e.silkEncoder.SetStreamChannels(streamChannels, e.toMono)
	defer func() { e.silkPrevChannels = streamChannels }()

	switchReady, err := e.silkInternalRateControl(frameSize, desiredRate, silkPrefill, prefill2, silkRate, silkMaxBits, streamChannels)
	if err != nil {
		return nil, err
	}
	e.silkOpusCanSwitch = switchReady && !e.nonfinalFrame
	// The coded bandwidth is the SILK rate actually in use.
	bw, ok := nativeSilkFramingBandwidth(e.silkSampleRate)
	if !ok {
		return nil, fmt.Errorf("SILK-only encoding not available for %d Hz", e.silkSampleRate)
	}
	toc, err := framing.GenerateTOCExt(framing.ModeSILKOnly, bw, streamChannels, frameSize*48000/e.sampleRate)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SILK TOC: %w", err)
	}

	silkFrameSize := e.silkSampleRate * e.silkFrameMsOrDefault() / 1000
	if err := e.silkEncoder.SetFrameMs(e.silkFrameMsOrDefault()); err != nil {
		return nil, err
	}
	silkPCM := e.silkInput(pcm, silkFrameSize*nFrames*streamChannels)
	e.inDTX = false
	enc := entcode.NewEncoder(maxDataBytes - 1)
	if err := e.silkEncoder.EncodeMultiWithEncoder(enc, silkPCM, nFrames); err != nil {
		return nil, fmt.Errorf("SILK encoding failed: %w", err)
	}
	if e.silkEncoder.PacketDTX() {
		// SILK DTX: the TOC alone, before any CELT processing.
		e.frameSILKDTX = true
		return nil, silkDTXPacket{toc: toc}
	}
	e.silkFramesCoded = true

	redundancy, redBytes, celtToSilk := d.redundancy, d.redundancyBytes, d.celtToSilk
	if switchReady && !e.nonfinalFrame {
		// For the first frame at a new SILK bandwidth: a trailing redundant
		// frame now, the switch (with prefill) on the next packet.
		// RESTRICTED_SILK has no CELT, so no redundant frame: the switch
		// then happens without a prefill (silk_bw_switch is cleared below).
		if e.application != ApplicationRestrictedSILK {
			redBytes = computeRedundancyBytes(maxDataBytes, d.bitrate, frameRate, streamChannels)
			redundancy = redBytes != 0
		}
		celtToSilk = false
		e.silkBwSwitch = true
	}
	// The redundancy is inferred from the length: only the celt_to_silk bit
	// is coded, when 17 bits are left; the frame is bounded by the remaining
	// bytes and 2..257.
	if redundancy && enc.ECTell()+17 <= 8*(maxDataBytes-1) {
		enc.EncodeBitLogp(celtToSilk, 1)
		maxRedundancy := (maxDataBytes - 1) - ((enc.ECTell() + 7) >> 3)
		if redBytes > maxRedundancy {
			redBytes = maxRedundancy
		}
		if redBytes < 2 {
			redBytes = 2
		}
		if redBytes > 257 {
			redBytes = 257
		}
	} else {
		redundancy = false
		redBytes = 0
		e.silkBwSwitch = false
	}
	tell := enc.ECTell()
	ret := (tell + 7) >> 3
	enc.Shrink(ret)
	enc.Flush()
	stream := enc.Bytes()
	if len(stream) > ret {
		stream = stream[:ret]
	}
	for len(stream) < ret {
		// ec_tell is an upper bound: libopus keeps ret bytes, the range
		// coder's unused tail cleared by ec_enc_done.
		stream = append(stream, 0)
	}
	// st->rangeFinal = enc.rng (ec_enc_done leaves rng unchanged).
	rangeFinal := enc.GetRng()
	if redundancy {
		// CELT_SET_END_BAND / CELT_SET_CHANNELS follow the packet's bandwidth
		// and stream channels on every packet.
		e.celtEncoder.SetStreamChannels(streamChannels)
		redFrame, redRng, err := e.encodeCELTRedundancy(celtPCM, redBytes, celtEndBandForFramingBW(bw), celtToSilk)
		if err != nil {
			return nil, fmt.Errorf("CELT redundant frame encoding failed: %w", err)
		}
		stream = append(stream, redFrame...)
		rangeFinal ^= redRng
	} else {
		// LPC-only packets may drop trailing zero bytes (the decoder fills
		// them in).
		for len(stream) > 2 && stream[len(stream)-1] == 0 {
			stream = stream[:len(stream)-1]
		}
	}
	if tell > (maxDataBytes-1)*8 {
		// SILK busted its target: data[1] = 0 tells the decoder to run the
		// PLC (ret = 1 plus the redundancy bytes that follow in the buffer),
		// and the final range is 0.
		if maxDataBytes < 2 {
			return nil, fmt.Errorf("%w: %d bytes for a SILK frame", ErrBufferTooSmall, maxDataBytes)
		}
		plc := make([]byte, 1+redBytes)
		if len(stream) > 1 {
			copy(plc[1:], stream[1:])
		}
		stream = plc
		rangeFinal = 0
	}
	e.lastFinalRange = rangeFinal
	packet := append([]byte{toc}, stream...)
	if e.rateMode == celt.RateModeCBR && len(packet) < maxDataBytes {
		// opus_packet_pad to cbr_bytes.
		payload, code, err := packOpusFramesToPacketSize([][]byte{stream}, false, maxDataBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to pad SILK packet: %w", err)
		}
		packet = append([]byte{toc | byte(code)}, payload...)
	}
	return packet, nil
}

// silkOnlyLibopusPath reports whether a SILK-only packet takes the libopus
// single-frame path.
// silkFrameMsOrDefault is the SILK frame duration of the packet (10 or 20
// ms; 20 outside the libopus policy).
func (e *Encoder) silkFrameMsOrDefault() int {
	if e.libopusModePolicy && e.silkFrameMs == 10 {
		return 10
	}
	return 20
}

// silkUnitFrameSize is the SILK / hybrid frame length at the input rate.
func (e *Encoder) silkUnitFrameSize() int {
	return e.sampleRate * e.silkFrameMsOrDefault() / 1000
}

func (e *Encoder) silkOnlyLibopusPath(nFrames int) bool {
	return e.libopusModePolicy && nFrames >= 1 && nFrames <= 3 && e.silkEncoder != nil
}
