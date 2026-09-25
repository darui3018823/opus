package opus

import (
	"fmt"

	"github.com/darui3018823/opus/internal/celt"
)

// libopus codes every CELT frame of a stream — the 20 ms frame, the 2.5 ms
// prefill after a mode change and the 5 ms redundant frame of a transition
// packet — with its single celt_enc. The Go encoder keeps one CELT encoder
// per frame size, so the helpers below thread the state (and the settings)
// of the current encoder through the encoder of the requested size and back,
// which makes the set behave as one libopus encoder.

// celtWithFrameSize runs fn on the CELT encoder for the given 48 kHz frame
// size, seeded with the current encoder's state and settings; the resulting
// state flows back into the current encoder.
func (e *Encoder) celtWithFrameSize(size int, fn func(enc *celt.Encoder) error) error {
	main := e.celtEncoder
	if main.FrameSize() == size {
		return fn(main)
	}
	idx := celtEncoderIndex(size)
	if idx < 0 {
		return fmt.Errorf("%w: CELT frame size %d", ErrUnsupportedFrameSize, size)
	}
	sub := e.celtEncoders[idx]
	if sub == nil {
		var err error
		sub, err = celt.NewEncoder(size, SampleRate48kHz, e.channels, celt.DefaultEncoderConfig())
		if err != nil {
			return fmt.Errorf("failed to create %d-sample CELT encoder: %w", size, err)
		}
		e.celtEncoders[idx] = sub
	}
	sub.CopyConfigFrom(main)
	sub.CopyStateFrom(main)
	err := fn(sub)
	main.CopyStateFrom(sub)
	return err
}

// celtUpsample is resampling_factor(Fs) under the libopus policy: the
// zero-stuffing factor that brings the input to the CELT encoder's 48 kHz
// mode (the Go policy resamples instead).
func (e *Encoder) celtUpsample() int {
	if !e.libopusModePolicy || e.sampleRate >= 48000 {
		return 1
	}
	return 48000 / e.sampleRate
}

// celtPrefill is the 2-byte "dummy" encode of 2.5 ms that follows an
// OPUS_RESET_STATE in opus_encode_native, filling the CELT history before a
// frame that starts a new mode. pcm is the input-rate signal.
func (e *Encoder) celtPrefill(pcm []float64, startBand int) error {
	return e.celtWithFrameSize(celt.FrameSize2_5ms, func(enc *celt.Encoder) error {
		return enc.EncodePrefill(e.celtInputFrame(pcm), startBand)
	})
}

// celtModeTransition is what opus_encode_native does to celt_enc when the
// packet's mode differs from the previous packet's: reset the state,
// prefill it with the 2.5 ms of conditioned input just before the frame
// (tmp_prefill from the delay buffer) and disable prediction for the frame.
// startBand is the CELT start band already set for the packet (17 for
// hybrid, 0 for CELT-only), which the prefill encode also uses.
func (e *Encoder) celtModeTransition(startBand int) error {
	e.celtEncoder.Reset()
	n := e.sampleRate / 400 * e.channels
	prefill := e.celtPrefillTail
	if len(prefill) != n {
		prefill = make([]float64, n)
	}
	if err := e.celtPrefill(prefill, startBand); err != nil {
		return err
	}
	e.celtEncoder.SetPrediction(0)
	return nil
}

// encodeCELTRedundancy codes the 5 ms redundant CELT frame of a transition
// packet with the main CELT encoder, as libopus does: a CELT->SILK frame
// continues from the current CELT state (which is reset afterwards) and
// covers the first 5 ms of the frame; a SILK->CELT frame starts from a reset
// state prefilled with the 2.5 ms before the last 5 ms, with prediction
// disabled, and covers the last 5 ms. endBand < 0 keeps the encoder's band
// limit (libopus leaves it as the previous CELT frame set it). The input
// is the frame's CELT input at the input rate (delayed, faded).
func (e *Encoder) encodeCELTRedundancy(celtPCM []float64, nbytes, endBand int, celtToSilk bool) ([]byte, error) {
	ch := e.channels
	frame := len(celtPCM) / ch
	n2, n4 := e.sampleRate/200, e.sampleRate/400
	if endBand >= 0 {
		// CELT_SET_END_BAND is a packet-level setting of celt_enc: the
		// 2.5 ms prefill before the trailing frame runs under it too (its
		// temporal VBR follower averages the coded bands only).
		e.celtEncoder.SetEndBand(endBand)
	}
	encode := func(part []float64) ([]byte, error) {
		var out []byte
		err := e.celtWithFrameSize(celt.FrameSize5ms, func(enc *celt.Encoder) error {
			if endBand >= 0 {
				enc.SetEndBand(endBand)
			}
			var err error
			out, err = enc.EncodeRedundant(e.celtInputFrame(part), nbytes)
			return err
		})
		return out, err
	}
	if celtToSilk {
		out, err := encode(celtPCM[:n2*ch])
		e.celtEncoder.Reset()
		return out, err
	}
	e.celtEncoder.Reset()
	e.celtEncoder.SetPrediction(0)
	// OPUS_SET_VBR(0) + OPUS_SET_BITRATE(OPUS_BITRATE_MAX) precede the
	// prefill too, so it leaves the VBR state alone.
	savedMode, savedBitrate := e.celtEncoder.GetRateMode(), e.celtEncoder.Bitrate()
	e.celtEncoder.SetRateMode(celt.RateModeCBR)
	e.celtEncoder.SetBitrateMax()
	err := e.celtPrefill(celtPCM[(frame-n2-n4)*ch:(frame-n2)*ch], 0)
	e.celtEncoder.SetRateMode(savedMode)
	if savedBitrate > 0 {
		e.celtEncoder.SetBitrate(savedBitrate)
	} else {
		e.celtEncoder.SetBitrateMax()
	}
	if err != nil {
		return nil, err
	}
	return encode(celtPCM[(frame-n2)*ch:])
}

// silkPrefillInput builds the 10 ms buffer opus_encode_native hands to the
// SILK prefill of a CELT-only -> SILK/hybrid switch: the delay buffer
// (encoder_buffer = 10 ms of the previous conditioned input) with everything
// before the last delay_compensation + 2.5 ms zeroed and those 2.5 ms faded
// in from silence. The same faded 2.5 ms are then the CELT prefill
// (tmp_prefill), so the fade is applied to the remembered tail in place.
// It must run before the delay buffer advances to the current frame.
func (e *Encoder) silkPrefillInput() []float64 {
	ch := e.channels
	encoderBuffer := e.sampleRate / 100
	n4 := e.sampleRate / 400
	delay := e.delayCompensation()
	offset := encoderBuffer - delay - n4
	if offset < 0 {
		return nil
	}
	buf := make([]float64, encoderBuffer*ch)
	if len(e.celtPrefillTail) != n4*ch {
		e.celtPrefillTail = make([]float64, n4*ch)
	}
	applyGainFade(e.celtPrefillTail, 0, 1, ch, e.sampleRate)
	copy(buf[offset*ch:], e.celtPrefillTail)
	if len(e.delayBuffer) == delay*ch {
		copy(buf[(offset+n4)*ch:], e.delayBuffer)
	}
	return buf
}

// rememberCELTPrefillTail keeps the last 2.5 ms of the frame's (unfaded)
// CELT input: the tmp_prefill of a mode transition in the next packet.
func (e *Encoder) rememberCELTPrefillTail(celtPCM []float64) {
	n := e.sampleRate / 400 * e.channels
	if len(celtPCM) < n {
		return
	}
	e.celtPrefillTail = append(e.celtPrefillTail[:0], celtPCM[len(celtPCM)-n:]...)
}
