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
		return nil
	}
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
		e.silkEncoder.Prefill(e.silkInput(silkPrefill, e.silkSampleRate/100*streamChannels))
	}
	// The main call's control pass (opusCanSwitch was cleared after the
	// prefill); the rate cannot change here.
	fs, ready := e.silkEncoder.ControlAudioBandwidth(desiredRate, false, payloadMs)
	if fs*1000 != e.silkSampleRate {
		return false, fmt.Errorf("SILK internal rate %d kHz requested outside a switch (encoder at %d Hz)", fs, e.silkSampleRate)
	}
	return ready, nil
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
	frameSize := e.frameSize * nFrames
	frameRate := e.sampleRate / frameSize
	streamChannels := e.streamChannelsOrInput()
	desiredRate := e.silkInternalRateForBandwidth(d.bandwidth)
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
	silkRate := bitsToBitrate(bits-8, e.sampleRate, frameSize)
	if silkRate < 5000 {
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
	// The coded bandwidth is the SILK rate actually in use.
	bw, ok := nativeSilkFramingBandwidth(e.silkSampleRate)
	if !ok {
		return nil, fmt.Errorf("SILK-only encoding not available for %d Hz", e.silkSampleRate)
	}
	toc, err := framing.GenerateTOCExt(framing.ModeSILKOnly, bw, streamChannels, nFrames*framing.FrameSize20ms)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SILK TOC: %w", err)
	}

	silkFrameSize := e.silkSampleRate * 20 / 1000
	silkPCM := e.silkInput(pcm, silkFrameSize*nFrames*streamChannels)
	e.inDTX = false
	enc := entcode.NewEncoder(maxDataBytes - 1)
	if err := e.silkEncoder.EncodeMultiWithEncoder(enc, silkPCM, nFrames); err != nil {
		return nil, fmt.Errorf("SILK encoding failed: %w", err)
	}
	e.silkFramesCoded = true

	redundancy, redBytes, celtToSilk := d.redundancy, d.redundancyBytes, d.celtToSilk
	if switchReady && !e.nonfinalFrame {
		// For the first frame at a new SILK bandwidth: a trailing redundant
		// frame now, the switch (with prefill) on the next packet.
		redBytes = computeRedundancyBytes(maxDataBytes, d.bitrate, frameRate, streamChannels)
		redundancy = redBytes != 0
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
	ret := (enc.ECTell() + 7) >> 3
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
	rangeFinal := e.silkEncoder.LastFinalRange()
	if redundancy {
		// CELT_SET_END_BAND / CELT_SET_CHANNELS follow the packet's bandwidth
		// and stream channels on every packet.
		e.celtEncoder.SetStreamChannels(streamChannels)
		redFrame, err := e.encodeCELTRedundancy(celtPCM, redBytes, celtEndBandForFramingBW(bw), celtToSilk)
		if err != nil {
			return nil, fmt.Errorf("CELT redundant frame encoding failed: %w", err)
		}
		stream = append(stream, redFrame...)
		rangeFinal ^= e.celtEncoder.FinalRange()
	} else {
		// LPC-only packets may drop trailing zero bytes (the decoder fills
		// them in).
		for len(stream) > 2 && stream[len(stream)-1] == 0 {
			stream = stream[:len(stream)-1]
		}
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
func (e *Encoder) silkOnlyLibopusPath(nFrames int) bool {
	return e.libopusModePolicy && nFrames >= 1 && nFrames <= 3 && e.silkEncoder != nil
}
