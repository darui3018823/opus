package opus

import "github.com/darui3018823/opus/internal/celt"

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
