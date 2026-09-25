package opus

import (
	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
)

// libopusPLCFrame is opus_encode_native's "PLC frame" under the libopus
// policy: when the space is too low to code anything useful (fewer than 3
// bytes, less than 3 bytes' worth of bitrate per frame, or a long frame
// with too little room), the packet is the TOC alone - of the previous
// packet's mode, bandwidth and stream channels - and the decoder conceals
// it. Nothing else of the encoder state moves; a CBR packet is padded to its
// size.
func (e *Encoder) libopusPLCFrame(frameSize, maxDataBytes int) ([]byte, bool) {
	frameRate := e.sampleRate / frameSize
	vbr := e.rateMode != celt.RateModeCBR
	bitrate := e.bitrate
	if !vbr {
		bitrate = bitsToBitrate(maxDataBytes*8, e.sampleRate, frameSize)
	}
	if !(maxDataBytes < 3 || bitrate < 3*frameRate*8 ||
		(frameRate < 50 && (maxDataBytes*frameRate < 300 || bitrate < 2400))) {
		return nil, false
	}
	tocMode := e.libopusMode
	bw := e.libopusBandwidth
	if bw < 0 {
		bw = framing.BandwidthFullband
	}
	packetCode := 0
	numMultiframes := 0
	if frameRate > 100 {
		tocMode = framing.ModeCELTOnly
	}
	// 40 ms -> 2 x 20 ms if in CELT_ONLY or HYBRID mode.
	if frameRate == 25 && tocMode != framing.ModeSILKOnly {
		frameRate = 50
		packetCode = 1
	}
	// >= 60 ms frames.
	if frameRate <= 16 {
		// 1 x 60 ms, 2 x 40 ms, 2 x 60 ms (out_data_bytes is never 1 here).
		if tocMode == framing.ModeSILKOnly && frameRate != 10 {
			if frameRate <= 12 {
				packetCode = 1
			}
			if frameRate == 12 {
				frameRate = 25
			} else {
				frameRate = 16
			}
		} else {
			numMultiframes = 50 / frameRate
			frameRate = 50
			packetCode = 3
		}
	}
	switch {
	case tocMode == framing.ModeSILKOnly && bw > framing.BandwidthWideband:
		bw = framing.BandwidthWideband
	case tocMode == framing.ModeCELTOnly && bw == framing.BandwidthMediumband:
		bw = framing.BandwidthNarrowband
	case tocMode == framing.ModeHybrid && bw <= framing.BandwidthSuperwideband:
		bw = framing.BandwidthSuperwideband
	}
	// gen_toc's frame duration: 16 means 60 ms (48000/16 = 3000 is not a
	// frame size), so map the frame rate to 48 kHz samples explicitly.
	durations := map[int]int{400: 120, 200: 240, 100: 480, 50: 960, 25: 1920, 16: 2880}
	toc, err := framing.GenerateTOCExt(tocMode, bw, e.streamChannelsOrInput(), durations[frameRate])
	if err != nil {
		return nil, false
	}
	pkt := []byte{toc | byte(packetCode)}
	if packetCode == 3 {
		pkt = append(pkt, byte(numMultiframes))
	}
	if !vbr && maxDataBytes > len(pkt) {
		if padded, err := PacketPad(pkt, maxDataBytes); err == nil {
			pkt = padded
		}
	}
	return pkt, true
}
