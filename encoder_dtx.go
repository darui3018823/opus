package opus

import (
	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/silk"
)

// libopus DTX under ModePolicyLibopus (src/opus_encoder.c). Two mechanisms:
//
//   - the generalized DTX (decide_dtx_mode): after NB_SPEECH_FRAMES_BEFORE_DTX
//     x 20 ms without activity a fully coded frame is replaced by a TOC-only
//     packet, for at most MAX_CONSECUTIVE_DTX x 20 ms before one frame is sent
//     again. Activity comes from digital silence, the tonality analysis'
//     activity probability with a pseudo-SNR against the peak signal energy,
//     the frame energy in CELT-only mode, or SILK's signal type.
//   - SILK's own DTX (silk_mode.useDTX) when the generalized DTX cannot run
//     (no tonality analysis, and not digital silence): SILK drops the packet
//     and opus_encode_native returns the TOC alone, before any CELT
//     processing or delay-buffer update.
const (
	dtxNBSpeechFramesBeforeDTX = 10     // NB_SPEECH_FRAMES_BEFORE_DTX (200 ms)
	dtxMaxConsecutiveDTX       = 20     // MAX_CONSECUTIVE_DTX (400 ms)
	dtxActivityThreshold       = 0.1    // DTX_ACTIVITY_THRESHOLD
	dtxPseudoSNRThreshold      = 316.23 // PSEUDO_SNR_THRESHOLD, 10^(25/10)
)

// silkDTXPacket is returned by the SILK-only and hybrid paths when SILK DTX
// dropped the packet; toc is the packet's TOC (gen_toc with the coded
// bandwidth), sent alone.
type silkDTXPacket struct{ toc byte }

// Error implements error; the value never leaves the encoder.
func (silkDTXPacket) Error() string { return "SILK DTX packet" }

// isDigitalSilence is is_digital_silence: the peak sample is at most one LSB
// of the effective input depth.
func isDigitalSilence(pcm []float64, lsbDepth int) bool {
	var sampleMax float32
	for _, v := range pcm {
		a := float32(v)
		if a < 0 {
			a = -a
		}
		if a > sampleMax {
			sampleMax = a
		}
	}
	return sampleMax <= float32(1)/float32(int32(1)<<uint(lsbDepth))
}

// frameEnergy is compute_frame_energy (float build): the mean square of the
// interleaved samples, summed in float32 in order (celt_inner_prod).
func frameEnergy(pcm []float64) float32 {
	if len(pcm) == 0 {
		return 0
	}
	var acc float32
	for _, v := range pcm {
		x := float32(v)
		acc = acc + float32(x*x)
	}
	return acc / float32(len(pcm))
}

// trackPeakSignalEnergy follows st->peak_signal_energy over the packet's
// raw input, only on active (or unanalysed) non-silent packets.
func (e *Encoder) trackPeakSignalEnergy(pcm []float64, isSilence bool, analysis celt.AnalysisInfo) {
	if analysis.Valid && !(analysis.ActivityProbability > float32(dtxActivityThreshold)) {
		return
	}
	if isSilence {
		return
	}
	decayed := float32(float32(0.999) * e.peakSignalEnergy)
	if en := frameEnergy(pcm); en > decayed {
		decayed = en
	}
	e.peakSignalEnergy = decayed
}

// frameActivity is the per-frame voice activity decision of
// opus_encode_frame_native (VAD_NO_DECISION when neither applies; SILK's
// signal type fills it in after the SILK encode).
func (e *Encoder) frameActivity(pcm []float64, isSilence bool, analysis celt.AnalysisInfo, mode int) int {
	switch {
	case isSilence:
		return silk.VADNoActivity
	case analysis.Valid:
		if analysis.ActivityProbability >= float32(dtxActivityThreshold) {
			return silk.VADActivity
		}
		// Mark as active if this noise frame is sufficiently loud.
		if e.peakSignalEnergy < float32(float32(dtxPseudoSNRThreshold)*frameEnergy(pcm)) {
			return silk.VADActivity
		}
		return silk.VADNoActivity
	case mode == framing.ModeCELTOnly:
		// Boosting peak energy a bit because we didn't just average the
		// active frames.
		if e.peakSignalEnergy < float32(float32(dtxPseudoSNRThreshold)*float32(float32(0.5)*frameEnergy(pcm))) {
			return silk.VADActivity
		}
		return silk.VADNoActivity
	}
	return silk.VADNoDecision
}

// decideDTXMode is decide_dtx_mode: true when this frame is sent as a
// TOC-only packet.
func (e *Encoder) decideDTXMode(activity, frameSizeMsQ1 int) bool {
	if activity != silk.VADNoActivity {
		e.nbNoActivityMsQ1 = 0
		return false
	}
	e.nbNoActivityMsQ1 += frameSizeMsQ1
	if e.nbNoActivityMsQ1 > dtxNBSpeechFramesBeforeDTX*20*2 {
		if e.nbNoActivityMsQ1 <= (dtxNBSpeechFramesBeforeDTX+dtxMaxConsecutiveDTX)*20*2 {
			// Valid frame for DTX!
			return true
		}
		e.nbNoActivityMsQ1 = dtxNBSpeechFramesBeforeDTX * 20 * 2
	}
	return false
}

// libopusInDTX is OPUS_GET_IN_DTX under the libopus policy.
func (e *Encoder) libopusInDTX() bool {
	if e.silkUseDTX && (e.prevMode == framing.ModeSILKOnly || e.prevMode == framing.ModeHybrid) && e.silkEncoder != nil {
		return e.silkEncoder.NoSpeechDTX()
	}
	if e.dtx {
		return e.nbNoActivityMsQ1 >= dtxNBSpeechFramesBeforeDTX*20*2
	}
	return false
}

// silkEncoderDTX reports that SILK DTX dropped the frame being coded.
func (e *Encoder) silkEncoderDTX() bool { return e.frameSILKDTX }

// celtDTX is the CELT encoder's own minimal-packet DTX: a Go-policy
// feature; libopus handles DTX at the Opus layer.
func (e *Encoder) celtDTX() bool { return e.dtx && !e.libopusModePolicy }
