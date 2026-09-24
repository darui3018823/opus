package opus

import (
	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
)

// setLibopusBitrate is OPUS_SET_BITRATE as opus_multistream_encode_native
// issues it on an elementary encoder: numeric rates are clamped to
// [500, 750000 x channels] instead of rejected.
func (e *Encoder) setLibopusBitrate(bitrate int) {
	if bitrate != BitrateAuto && bitrate != BitrateMax {
		bitrate = max(500, min(750000*e.channels, bitrate))
	}
	e.bitrateSetting = bitrate
}

// applyOutDataBytes applies opus_encode_native's out_data_bytes (the
// multistream encoder's per-stream budget): the bitrate is capped to what
// the budget can carry (user_bitrate_to_bitrate) and, in CBR, cbr_bytes is
// bounded by it.
func (e *Encoder) applyOutDataBytes(frameSize int, maxDataBytes *int) error {
	limit := min(e.outDataBytes, 6*(MaxFrameBytes+1))
	if maxRate := bitsToBitrate(limit*8, e.sampleRate, frameSize); e.bitrate > maxRate {
		if err := e.setPacketBitrate(maxRate, frameSize); err != nil {
			return err
		}
	}
	if e.rateMode == celt.RateModeCBR {
		cbrBytes := min((bitrateToBits(e.bitrate, e.sampleRate, frameSize)+4)/8, limit)
		if err := e.setPacketBitrate(bitsToBitrate(cbrBytes*8, e.sampleRate, frameSize), frameSize); err != nil {
			return err
		}
		*maxDataBytes = min(*maxDataBytes, max(1, cbrBytes))
		return nil
	}
	*maxDataBytes = min(*maxDataBytes, limit)
	return nil
}

// surroundSILKRate is opus_encode_frame_native's surround masking for SILK
// (libopus policy, VBR, not LFE): the SILK rate moves with the average
// masking of the bands SILK codes, split with CELT in a hybrid packet. bw
// is the packet's framing bandwidth.
func (e *Encoder) surroundSILKRate(silkRate, bw int) int {
	if !e.libopusModePolicy || len(e.surroundEnergyMask) == 0 || e.rateMode == celt.RateModeCBR || e.lfe {
		return silkRate
	}
	end, srate := 17, 16000
	switch bw {
	case framing.BandwidthNarrowband:
		end, srate = 13, 8000
	case framing.BandwidthMediumband:
		end, srate = 15, 12000
	}
	var maskSum float32
	for c := 0; c < e.channels; c++ {
		for i := 0; i < end; i++ {
			m := max(min(float32(e.surroundEnergyMask[21*c+i]), 0.5), -2)
			if m > 0 {
				m = float32(0.5) * m
			}
			maskSum += m
		}
	}
	// Conservative rate reduction, we cut the masking in half.
	maskingDepth := float32(float32(maskSum/float32(end)) * float32(e.channels))
	maskingDepth += 0.2
	rateOffset := int(float32(float32(srate) * maskingDepth))
	rateOffset = max(rateOffset, -2*silkRate/3)
	// Split the rate change between the SILK and CELT part for hybrid.
	if bw == framing.BandwidthSuperwideband || bw == framing.BandwidthFullband {
		return silkRate + 3*rateOffset/5
	}
	return silkRate + rateOffset
}
