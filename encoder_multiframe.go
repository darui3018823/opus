package opus

import (
	"fmt"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
)

// defaultOutDataBytes stands in for the caller's output buffer size that
// opus_encode receives (out_data_bytes): libopus bounds a VBR multi-frame
// packet by it. The Go API has no such buffer, so the usual buffer size of
// the reference tools (and the oracle) applies; it only binds at extreme
// bitrates.
const defaultOutDataBytes = 1500

// encodeMultiframeLibopus is opus_encode_native's multi-frame path under the
// libopus policy: a packet longer than 20 ms in hybrid or CELT-only mode (or
// longer than 60 ms) is coded as consecutive 20 ms frames that all use the
// packet's decisions — mode, bandwidth, stream channels, equivalent rate,
// prefill — with the leading redundancy on the first frame, the deferred
// switch to CELT-only (to_celt) and its trailing redundancy on the last
// frame, and nonfinal_frame holding back a SILK bandwidth switch. Each frame
// is bounded by its share of the packet (curr_max) and the frames are
// repacketized, padded to cbr_bytes in CBR. As in libopus a packet coded
// during the stereo->mono delay (toMono) leaves the channels forced to
// mono, and the prefill of a CELT-only -> SILK switch repeats on every
// frame of the packet.
func (e *Encoder) encodeMultiframeLibopus(raw []float64, frameSize, nFrames, maxDataBytes int, decision modeDecision) ([]byte, error) {
	// SILK-only packets over 60 ms are split into 40 or 60 ms native SILK
	// packets where possible (80 -> 2x40, 120 -> 2x60, 100 -> 5x20).
	encFrameSize := e.frameSize
	if decision.mode == framing.ModeSILKOnly {
		switch frameSize {
		case 4 * e.frameSize:
			encFrameSize = 2 * e.frameSize
		case 6 * e.frameSize:
			encFrameSize = 3 * e.frameSize
		}
	}
	nFrames = frameSize / encFrameSize
	subFrames := encFrameSize / e.frameSize
	ch := e.channels
	vbr := e.rateMode != celt.RateModeCBR
	// Worst cases: 2 frames: code 2 with different compressed sizes;
	// >2 frames: code 3 VBR.
	maxHeaderBytes := 2 + (nFrames-1)*2
	if nFrames == 2 {
		maxHeaderBytes = 3
	}
	repacketizeLen := defaultOutDataBytes
	if !vbr {
		repacketizeLen = maxDataBytes
		if repacketizeLen > defaultOutDataBytes {
			repacketizeLen = defaultOutDataBytes
		}
	}
	maxLenSum := nFrames + repacketizeLen - maxHeaderBytes

	bakToMono := e.toMono
	if bakToMono {
		e.forceChannels = ChannelsMono
	} else {
		e.prevStreamChannels = e.streamChannels
	}
	// Reset the analysis position to the beginning of the first frame so
	// it can be read one frame at a time.
	perFrameAnalysis := e.analysisReadPos != -1 && e.analysis != nil
	if perFrameAnalysis {
		e.analysis.SetReadPosition(e.analysisReadPos, e.analysisReadSubframe)
	}
	frames := make([][]byte, 0, nFrames)
	totSize := 0
	dtxCount := 0
	var rangeFinal uint32
	for i := 0; i < nFrames; i++ {
		e.toMono = false
		e.nonfinalFrame = i < nFrames-1
		d := decision
		// When switching from SILK/hybrid to CELT, only ask for a switch at
		// the last frame.
		d.toCelt = decision.toCelt && i == nFrames-1
		d.redundancy = decision.redundancy && (d.toCelt || (!decision.toCelt && i == 0))
		currMax := bitrateToBits(decision.bitrate, e.sampleRate, encFrameSize) / 8
		if share := maxLenSum / nFrames; share < currMax {
			currMax = share
		}
		if left := maxLenSum - totSize; left < currMax {
			currMax = left
		}
		sub := raw[i*encFrameSize*ch : (i+1)*encFrameSize*ch]
		// The frame's analysis (tonality_get_info, which CELT_SET_ANALYSIS
		// hands to the CELT encoder) and is_digital_silence.
		if perFrameAnalysis {
			e.frameAnalysis = e.analysis.GetInfo(encFrameSize)
			e.celtEncoder.SetAnalysis(e.frameAnalysis)
		}
		e.frameIsSilence = isDigitalSilence(sub, e.packetLSBDepth)
		pkt, err := e.encodeDecidedFrame(sub, encFrameSize, subFrames, currMax, d)
		if err != nil {
			e.nonfinalFrame = false
			return nil, err
		}
		if len(pkt) == 1 {
			dtxCount++
		}
		totSize += len(pkt)
		rangeFinal = e.lastFinalRange
		frames = append(frames, pkt)
	}
	e.nonfinalFrame = false
	e.toMono = bakToMono

	// opus_repacketizer_out_range_impl: the frames share the TOC; code 1 for
	// two equal frames, code 2 otherwise, code 3 (CBR or VBR flag) for more,
	// padded to the CBR size when the rate is constant.
	toc := frames[0][0] &^ 0x03
	payloads := make([][]byte, nFrames)
	allEqual := true
	for i, f := range frames {
		if f[0]&^0x03 != toc {
			return nil, fmt.Errorf("multi-frame packet mixes TOCs %#x and %#x", toc, f[0]&^0x03)
		}
		payloads[i] = f[1:]
		if len(f) != len(frames[0]) {
			allEqual = false
		}
	}
	var payload []byte
	var code int
	var err error
	// CBR pads the packet unless every frame is a DTX frame.
	if !vbr && dtxCount != nFrames {
		payload, code, err = packOpusFramesToPacketSize(payloads, !allEqual, repacketizeLen)
	} else {
		payload, code, err = packOpusFrames(payloads, !allEqual)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to repacketize %d frames: %w", nFrames, err)
	}
	e.lastFinalRange = rangeFinal
	return append([]byte{toc | byte(code)}, payload...), nil
}
