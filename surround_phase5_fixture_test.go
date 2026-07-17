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

func surroundFixtureChannelRMS(pcm []float32, channels, channel int) float64 {
	var energy float64
	for i := channel; i < len(pcm); i += channels {
		v := float64(pcm[i])
		energy += v * v
	}
	return math.Sqrt(energy / float64(len(pcm)/channels))
}
