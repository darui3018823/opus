package opus

import (
	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/celt"
)

// celtOnlyStereoWidthQ14 mirrors the silk_mode.stereoWidth_Q14 that
// opus_encode_native derives from the equivalent rate for CELT-only (and
// mono-stream hybrid) packets: full width above 32 kb/s, mono below 16 kb/s
// and a linear ramp in between.
func celtOnlyStereoWidthQ14(equivRate int) int {
	switch {
	case equivRate > 32000:
		return 16384
	case equivRate < 16000:
		return 0
	default:
		return 16384 - 2048*(32000-equivRate)/(equivRate-14000)
	}
}

// celtEquivRate is the libopus equiv_rate for a 20 ms CELT-only frame: the
// packet bitrate (rounded to the CBR packet size in CBR) adjusted for VBR,
// complexity and loss.
func (e *Encoder) celtEquivRate(streamChannels int) int {
	bitrate := e.bitrate
	if e.rateMode == celt.RateModeCBR {
		cbrBytes := (bitrateToBits(e.bitrate, e.sampleRate, e.frameSize) + 4) / 8
		if cbrBytes > 1276 {
			cbrBytes = 1276
		}
		bitrate = bitsToBitrate(cbrBytes*8, e.sampleRate, e.frameSize)
	}
	return computeEquivRate(bitrate, streamChannels, e.sampleRate/e.frameSize, e.rateMode != celt.RateModeCBR,
		framing.ModeCELTOnly, e.complexity, e.packetLossPerc)
}

// applyGainFade mirrors gain_fade: the CELT input is scaled from the
// previous frame's high-band gain g1 to g2 with a crossfade over the CELT
// overlap, then by g2.
func applyGainFade(pcm []float64, g1, g2 float32, channels, sampleRate int) {
	inc := 48000 / sampleRate
	if inc < 1 {
		inc = 1
	}
	overlap := celt.OverlapSamples48k / inc
	window := celt.OverlapWindow48k()
	frameSize := len(pcm) / channels
	for i := 0; i < overlap && i < frameSize; i++ {
		w := window[i*inc]
		w = float32(w * w)
		g := float32(w*g2) + float32((1-w)*g1)
		for c := 0; c < channels; c++ {
			pcm[i*channels+c] = float64(g * float32(pcm[i*channels+c]))
		}
	}
	for i := overlap; i < frameSize; i++ {
		for c := 0; c < channels; c++ {
			pcm[i*channels+c] = float64(g2 * float32(pcm[i*channels+c]))
		}
	}
}

// applyStereoWidthFade mirrors the stereo_fade step of opus_encode_native for
// one 20 ms CELT input chunk at the input rate: when the previous or the
// current stereo width is below full, the side signal is attenuated with a
// crossfade over the CELT overlap from the previous width to the new one.
// The fade only touches the CELT input (it runs after the delay buffer), and
// is skipped when a surround energy mask is active.
func (e *Encoder) applyStereoWidthFade(pcm []float64, widthQ14 int) {
	if e.channels != 2 || len(e.surroundEnergyMask) > 0 {
		return
	}
	if e.hybridStereoWidthQ14 >= 1<<14 && widthQ14 >= 1<<14 {
		return
	}
	g1 := float32(float32(e.hybridStereoWidthQ14) * float32(1.0/16384))
	g2 := float32(float32(widthQ14) * float32(1.0/16384))
	inc := 48000 / e.sampleRate
	if inc < 1 {
		inc = 1
	}
	overlap := celt.OverlapSamples48k / inc
	window := celt.OverlapWindow48k()
	frameSize := len(pcm) / 2
	g1 = 1 - g1
	g2 = 1 - g2
	i := 0
	for ; i < overlap && i < frameSize; i++ {
		w := window[i*inc]
		w = float32(w * w)
		g := float32(w*g2) + float32((1-w)*g1)
		diff := float32(float32(0.5) * (float32(pcm[2*i]) - float32(pcm[2*i+1])))
		diff = float32(g * diff)
		pcm[2*i] = float64(float32(pcm[2*i]) - diff)
		pcm[2*i+1] = float64(float32(pcm[2*i+1]) + diff)
	}
	for ; i < frameSize; i++ {
		diff := float32(float32(0.5) * (float32(pcm[2*i]) - float32(pcm[2*i+1])))
		diff = float32(g2 * diff)
		pcm[2*i] = float64(float32(pcm[2*i]) - diff)
		pcm[2*i+1] = float64(float32(pcm[2*i+1]) + diff)
	}
	e.hybridStereoWidthQ14 = widthQ14
}
