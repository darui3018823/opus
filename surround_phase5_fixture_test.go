package opus

import (
	"math"
	"testing"
)

type surroundFixtureKind int

const (
	surroundFixtureRoleRich surroundFixtureKind = iota
	surroundFixtureSilentRear
	surroundFixtureDuplicateSides
)

// surroundPhase5Fixture is a deterministic, code-generated 5.1/7.1 source in
// Vorbis order. It combines correlated fronts, diffuse side/rear content, an
// isolated low-frequency-effects channel, and explicit silence/duplication
// cases without relying on an external corpus.
func surroundPhase5Fixture(kind surroundFixtureKind, channels, start, frameSize, rate int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		n := start + i
		t := float64(n) / float64(rate)
		front := 0.22*math.Sin(2*math.Pi*233*t) + 0.08*math.Sin(2*math.Pi*997*t)
		center := 0.18*math.Sin(2*math.Pi*233*t+0.04) + 0.05*math.Sin(2*math.Pi*1511*t)
		leftDiffuse := surroundFixtureNoise(uint32(n)*1664525+1013904223)*0.10 + 0.08*math.Sin(2*math.Pi*1703*t)
		rightDiffuse := surroundFixtureNoise(uint32(n)*22695477+1)*0.10 + 0.08*math.Sin(2*math.Pi*1879*t)
		lfe := 0.30*math.Sin(2*math.Pi*53*t) + 0.08*math.Sin(2*math.Pi*91*t)

		values := make([]float64, channels)
		values[0] = front + 0.025*math.Sin(2*math.Pi*421*t)      // front left
		values[1] = center                                       // center
		values[2] = 0.94*front + 0.025*math.Sin(2*math.Pi*557*t) // front right
		if channels == 6 {
			values[3], values[4], values[5] = leftDiffuse, rightDiffuse, lfe
		} else {
			values[3], values[4] = leftDiffuse, rightDiffuse
			values[5] = 0.75*leftDiffuse + 0.07*math.Sin(2*math.Pi*2399*t)
			values[6] = 0.75*rightDiffuse + 0.07*math.Sin(2*math.Pi*2683*t)
			values[7] = lfe
		}
		switch kind {
		case surroundFixtureSilentRear:
			if channels == 6 {
				values[4] = 0
			} else {
				values[5], values[6] = 0, 0
			}
		case surroundFixtureDuplicateSides:
			if channels == 6 {
				values[4] = values[3]
			} else {
				values[5], values[6] = values[3], values[4]
			}
		}
		for ch, value := range values {
			pcm[i*channels+ch] = float32(value)
		}
	}
	return pcm
}

func surroundFixtureNoise(state uint32) float64 {
	state ^= state << 13
	state ^= state >> 17
	state ^= state << 5
	return float64(int32(state)) / float64(math.MaxInt32)
}

func TestSurroundPhase5FixturesCoverChannelRoles(t *testing.T) {
	const (
		rate      = 48000
		frameSize = 960
	)
	for _, channels := range []int{6, 8} {
		for _, kind := range []surroundFixtureKind{surroundFixtureRoleRich, surroundFixtureSilentRear, surroundFixtureDuplicateSides} {
			pcm := surroundPhase5Fixture(kind, channels, 0, frameSize, rate)
			if len(pcm) != channels*frameSize {
				t.Fatalf("%dch fixture length=%d", channels, len(pcm))
			}
			lfe := channels - 1
			if rms := surroundFixtureChannelRMS(pcm, channels, lfe); rms < 0.1 {
				t.Fatalf("%dch kind=%d LFE RMS=%f", channels, kind, rms)
			}
			if kind == surroundFixtureSilentRear {
				silent := 4
				if channels == 8 {
					silent = 5
				}
				if rms := surroundFixtureChannelRMS(pcm, channels, silent); rms != 0 {
					t.Fatalf("%dch silent channel RMS=%f", channels, rms)
				}
			}
		}
	}
}

func TestSurroundMaskTrimImprovesCenterAtIdenticalBytes(t *testing.T) {
	const (
		rate      = 48000
		channels  = 6
		frameSize = 960
		frames    = 18
		bitrate   = 256000
	)
	withMask, err := NewSurroundEncoder(rate, channels, MappingFamilyVorbis, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	withoutMask, err := NewSurroundEncoder(rate, channels, MappingFamilyVorbis, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	// Package-private baseline used only to isolate the trim decision. Production
	// family-1 encoders always retain the analyzer callback.
	withoutMask.beforeEncodeFloat = nil
	for _, enc := range []*SurroundEncoder{withMask, withoutMask} {
		enc.SetVBR(true)
		enc.SetVBRConstraint(true)
		if err := enc.SetBitrate(bitrate); err != nil {
			t.Fatal(err)
		}
	}
	withDec, err := NewSurroundDecoder(rate, channels, MappingFamilyVorbis)
	if err != nil {
		t.Fatal(err)
	}
	withoutDec, err := NewSurroundDecoder(rate, channels, MappingFamilyVorbis)
	if err != nil {
		t.Fatal(err)
	}
	var input, withOutput, withoutOutput []float64
	for frame := 0; frame < frames; frame++ {
		pcm := surroundPhase5Fixture(surroundFixtureRoleRich, channels, frame*frameSize, frameSize, rate)
		for _, sample := range pcm {
			input = append(input, float64(sample))
		}
		withPacket, err := withMask.EncodeFloat32(pcm, frameSize)
		if err != nil {
			t.Fatal(err)
		}
		withoutPacket, err := withoutMask.EncodeFloat32(pcm, frameSize)
		if err != nil {
			t.Fatal(err)
		}
		withChildren, _, err := splitMultistreamPackets(withPacket, withMask.Streams(), rate)
		if err != nil {
			t.Fatal(err)
		}
		withoutChildren, _, err := splitMultistreamPackets(withoutPacket, withoutMask.Streams(), rate)
		if err != nil {
			t.Fatal(err)
		}
		for stream := range withChildren {
			if len(withChildren[stream]) != len(withoutChildren[stream]) {
				t.Fatalf("frame %d stream %d bytes=%d, baseline=%d", frame, stream, len(withChildren[stream]), len(withoutChildren[stream]))
			}
		}
		withFrame, err := withDec.DecodeFloat32(withPacket)
		if err != nil {
			t.Fatal(err)
		}
		if withMask.FinalRange() != withDec.FinalRange() {
			t.Fatalf("frame %d masked final range encoder=%08x decoder=%08x", frame, withMask.FinalRange(), withDec.FinalRange())
		}
		withoutFrame, err := withoutDec.DecodeFloat32(withoutPacket)
		if err != nil {
			t.Fatal(err)
		}
		if withoutMask.FinalRange() != withoutDec.FinalRange() {
			t.Fatalf("frame %d baseline final range encoder=%08x decoder=%08x", frame, withoutMask.FinalRange(), withoutDec.FinalRange())
		}
		for _, sample := range withFrame {
			withOutput = append(withOutput, float64(sample))
		}
		for _, sample := range withoutFrame {
			withoutOutput = append(withoutOutput, float64(sample))
		}
	}
	withSNR := surroundChannelSNRs(input, withOutput, channels, frameSize)
	withoutSNR := surroundChannelSNRs(input, withoutOutput, channels, frameSize)
	if withSNR[1] < withoutSNR[1]+5 {
		t.Fatalf("center SNR %.2f dB, baseline %.2f dB", withSNR[1], withoutSNR[1])
	}
	for _, channel := range []int{0, 2, 3, 4} {
		if withSNR[channel] < withoutSNR[channel]-0.3 {
			t.Fatalf("channel %d SNR regressed %.2f -> %.2f dB", channel, withoutSNR[channel], withSNR[channel])
		}
	}
	if math.Abs(withSNR[5]-withoutSNR[5]) > 1e-9 {
		t.Fatalf("LFE SNR changed %.6f -> %.6f dB", withoutSNR[5], withSNR[5])
	}
}

func surroundFixtureChannelRMS(pcm []float32, channels, channel int) float64 {
	var energy float64
	for i := channel; i < len(pcm); i += channels {
		v := float64(pcm[i])
		energy += v * v
	}
	return math.Sqrt(energy / float64(len(pcm)/channels))
}

func surroundChannelSNRs(input, output []float64, channels, maxDelay int) []float64 {
	result := make([]float64, channels)
	for channel := 0; channel < channels; channel++ {
		in := make([]float64, len(input)/channels)
		out := make([]float64, len(output)/channels)
		for i := range in {
			in[i] = input[i*channels+channel]
			out[i] = output[i*channels+channel]
		}
		result[channel] = surroundAlignedSNR(in, out, maxDelay)
	}
	return result
}

func surroundAlignedSNR(input, output []float64, maxDelay int) float64 {
	best := math.Inf(1)
	for delay := 0; delay <= maxDelay; delay++ {
		n := min(len(input), len(output)-delay)
		if n <= 2*maxDelay {
			continue
		}
		lo, hi := maxDelay, n-maxDelay
		var xy, yy float64
		for i := lo; i < hi; i++ {
			x, y := input[i], output[i+delay]
			xy += x * y
			yy += y * y
		}
		scale := 0.0
		if yy > 0 {
			scale = xy / yy
		}
		var signal, err float64
		for i := lo; i < hi; i++ {
			x := input[i]
			delta := x - scale*output[i+delay]
			signal += x * x
			err += delta * delta
		}
		if signal > 0 && err < best {
			best = err / signal
		}
	}
	if math.IsInf(best, 1) {
		return 0
	}
	if best == 0 {
		return 300
	}
	return -10 * math.Log10(best)
}
