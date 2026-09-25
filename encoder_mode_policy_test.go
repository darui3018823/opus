package opus

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"testing"
)

// modePolicyTestFrame is a deterministic voiced/noisy test signal: a few
// harmonics with a slow amplitude envelope plus low-level noise, different
// in each channel so that a stereo input exercises the stereo decisions.
func modePolicyTestFrame(rate, channels, start, n int) []float64 {
	pcm := make([]float64, n*channels)
	seed := uint32(start*2654435761 + 12345)
	for i := 0; i < n; i++ {
		t := float64(start+i) / float64(rate)
		env := 0.5 + 0.5*math.Sin(2*math.Pi*1.7*t)
		for c := 0; c < channels; c++ {
			f0 := 180.0 + 40*float64(c)
			v := 0.0
			for h := 1; h <= 6; h++ {
				v += math.Sin(2*math.Pi*f0*float64(h)*t) / float64(h)
			}
			seed = seed*1664525 + 1013904223
			noise := (float64(seed>>9)/float64(1<<23) - 0.5) * 0.02
			x := 0.25*env*v + noise
			pcm[i*channels+c] = math.Floor(x*32768+0.5) / 32768
		}
	}
	return pcm
}

func TestSetModePolicyValidation(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if got := enc.ModePolicy(); got != ModePolicyLegacy {
		t.Fatalf("default ModePolicy = %d, want ModePolicyLegacy", got)
	}
	if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
		t.Fatal(err)
	}
	if got := enc.ModePolicy(); got != ModePolicyLibopus {
		t.Fatalf("ModePolicy = %d, want ModePolicyLibopus", got)
	}
	for _, bad := range []ModePolicy{-1, 2, 99} {
		if err := enc.SetModePolicy(bad); !errors.Is(err, ErrBadArg) {
			t.Fatalf("SetModePolicy(%d) error = %v, want ErrBadArg", bad, err)
		}
		if got := enc.ModePolicy(); got != ModePolicyLibopus {
			t.Fatalf("invalid SetModePolicy(%d) changed the policy to %d", bad, got)
		}
	}
}

// TestSetModePolicyMidStreamResets checks that changing the policy after
// encoding has started behaves exactly like starting a new stream: from the
// switch on, the packets match those of a fresh encoder with the same
// settings and the new policy. It also checks that re-selecting the current
// policy leaves the stream untouched.
func TestSetModePolicyMidStreamResets(t *testing.T) {
	type cfg struct {
		rate, channels, bitrate, frameMs int
		vbr                              bool
		app                              Application
	}
	cfgs := []cfg{
		{48000, 1, 24000, 20, true, ApplicationVOIP},
		{48000, 2, 64000, 20, true, ApplicationAudio},
		{48000, 2, 16000, 40, true, ApplicationVOIP},
		{16000, 1, 20000, 20, false, ApplicationVOIP},
		{24000, 2, 32000, 60, true, ApplicationAudio},
		{8000, 2, 12000, 20, true, ApplicationVOIP},
	}
	const before, after = 5, 8
	for _, c := range cfgs {
		for _, dir := range [][2]ModePolicy{{ModePolicyLegacy, ModePolicyLibopus}, {ModePolicyLibopus, ModePolicyLegacy}} {
			name := fmt.Sprintf("%dk/ch%d/%dk/%dms/vbr=%v/%d->%d", c.rate/1000, c.channels, c.bitrate/1000, c.frameMs, c.vbr, dir[0], dir[1])
			t.Run(name, func(t *testing.T) {
				frameSize := c.rate * c.frameMs / 1000
				newEnc := func(p ModePolicy) *Encoder {
					enc, err := NewEncoder(c.rate, c.channels, c.app)
					if err != nil {
						t.Fatal(err)
					}
					if err := enc.SetModePolicy(p); err != nil {
						t.Fatal(err)
					}
					if err := enc.SetBitrate(c.bitrate); err != nil {
						t.Fatal(err)
					}
					enc.SetVBR(c.vbr)
					enc.SetInbandFEC(true)
					enc.SetPacketLossPerc(10)
					return enc
				}
				switched := newEnc(dir[0])
				for f := 0; f < before; f++ {
					if _, err := switched.EncodeFloat(modePolicyTestFrame(c.rate, c.channels, f*frameSize, frameSize), frameSize); err != nil {
						t.Fatalf("frame %d: %v", f, err)
					}
				}
				if err := switched.SetModePolicy(dir[1]); err != nil {
					t.Fatal(err)
				}
				if got := switched.ModePolicy(); got != dir[1] {
					t.Fatalf("ModePolicy after switch = %d, want %d", got, dir[1])
				}
				fresh := newEnc(dir[1])
				for f := before; f < before+after; f++ {
					pcm := modePolicyTestFrame(c.rate, c.channels, f*frameSize, frameSize)
					got, err := switched.EncodeFloat(pcm, frameSize)
					if err != nil {
						t.Fatalf("switched frame %d: %v", f, err)
					}
					want, err := fresh.EncodeFloat(pcm, frameSize)
					if err != nil {
						t.Fatalf("fresh frame %d: %v", f, err)
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("frame %d after the switch differs from a fresh encoder (%d vs %d bytes, TOC %#x vs %#x)", f, len(got), len(want), got[0], want[0])
					}
					if a, b := switched.FinalRange(), fresh.FinalRange(); a != b {
						t.Fatalf("frame %d final range %08x, fresh encoder %08x", f, a, b)
					}
				}
			})
		}
	}

	// Re-selecting the current policy must not reset the stream.
	t.Run("same-policy-noop", func(t *testing.T) {
		const rate, frameSize = 48000, 960
		a, _ := NewEncoder(rate, 1, ApplicationAudio)
		b, _ := NewEncoder(rate, 1, ApplicationAudio)
		for _, enc := range []*Encoder{a, b} {
			if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
				t.Fatal(err)
			}
		}
		for f := 0; f < 6; f++ {
			pcm := modePolicyTestFrame(rate, 1, f*frameSize, frameSize)
			if f == 3 {
				if err := a.SetModePolicy(ModePolicyLibopus); err != nil {
					t.Fatal(err)
				}
			}
			pa, err := a.EncodeFloat(pcm, frameSize)
			if err != nil {
				t.Fatal(err)
			}
			pb, err := b.EncodeFloat(pcm, frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(pa, pb) {
				t.Fatalf("frame %d: re-selecting the current policy changed the stream", f)
			}
		}
	})
}
