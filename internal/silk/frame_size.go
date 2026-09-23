package silk

import "fmt"

// SetFrameMs changes the SILK frame duration (10 or 20 ms) between packets
// without resetting the encoder, as silk_setup_fs does when PacketSize_ms
// changes: the frame length and subframe count follow (and with them the
// pitch analysis window and the pitch contour tables), while the carried
// history - the LTP memory and look-ahead in x_buf, the NSQ state, the
// noise-shaping and gain smoothing, the VAD - is kept. Both channel
// encoders of a stereo encoder switch together.
func (e *Encoder) SetFrameMs(ms int) error {
	if ms != 10 && ms != 20 {
		return fmt.Errorf("invalid SILK frame duration: %d ms (must be 10 or 20)", ms)
	}
	if ms != e.frameMs {
		frameSize := e.sampleRate / 1000 * ms
		ltpMem := e.ltpMemLength()
		keep := ltpMem + e.laShapeLength()

		// x_buf: only the LTP memory and the look-ahead carry over; the
		// next frame is written behind them.
		xBuf := make([]float64, keep+frameSize)
		copy(xBuf, e.xBuf[:min(keep, len(e.xBuf))])
		e.xBuf = xBuf
		e.pitchHist = e.xBuf[:ltpMem]

		// NSQ: xq / sLTP_shp keep their first ltp_mem_length samples.
		e.nsq.xq = resizeKeepInt16(e.nsq.xq, ltpMem+frameSize, ltpMem)
		e.nsq.sLTPShpQ14 = resizeKeepInt32(e.nsq.sLTPShpQ14, ltpMem+frameSize, ltpMem)

		e.frameMs = ms
		e.frameSize = frameSize
		e.nSubframes = 4
		if ms == 10 {
			e.nSubframes = 2
		}
	}
	if e.side != nil {
		return e.side.SetFrameMs(ms)
	}
	return nil
}

// FrameMs reports the SILK frame duration in milliseconds.
func (e *Encoder) FrameMs() int { return e.frameMs }

func resizeKeepInt16(s []int16, n, keep int) []int16 {
	out := make([]int16, n)
	copy(out, s[:min(keep, len(s))])
	return out
}

func resizeKeepInt32(s []int32, n, keep int) []int32 {
	out := make([]int32, n)
	copy(out, s[:min(keep, len(s))])
	return out
}
