package opus

import (
	"errors"
	"math"
	"testing"
)

func testTone(rate, channels, frameSize, frame int) []float64 {
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		t := float64(frame*frameSize+i) / float64(rate)
		for c := 0; c < channels; c++ {
			pcm[i*channels+c] = 0.3 * math.Sin(2*math.Pi*(220+40*float64(c))*t)
		}
	}
	return pcm
}

func TestRestrictedApplications(t *testing.T) {
	for _, app := range []Application{ApplicationRestrictedSILK, ApplicationRestrictedCELT} {
		enc, err := NewEncoder(48000, 2, app)
		if err != nil {
			t.Fatal(err)
		}
		if enc.ModePolicy() != ModePolicyLibopus {
			t.Fatalf("application %d: policy %d, want the libopus policy", app, enc.ModePolicy())
		}
		if err := enc.SetModePolicy(ModePolicyLegacy); !errors.Is(err, ErrBadArg) {
			t.Fatalf("application %d: SetModePolicy(Legacy) = %v, want ErrBadArg", app, err)
		}
		if err := enc.SetApplication(ApplicationAudio); !errors.Is(err, ErrBadArg) {
			t.Fatalf("application %d: SetApplication = %v, want ErrBadArg", app, err)
		}
		if err := enc.SetApplication(app); err != nil {
			t.Fatalf("application %d: SetApplication(same) = %v", app, err)
		}
		// libopus reports the delay compensation for RESTRICTED_SILK only.
		want := 48000/400 + 48000/250
		if app == ApplicationRestrictedCELT {
			want = 48000 / 400
		}
		if got := enc.Lookahead(); got != want {
			t.Fatalf("application %d: Lookahead %d, want %d", app, got, want)
		}
		if err := enc.SetBitrate(96000); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 5; f++ {
			pkt, err := enc.EncodeFloat(testTone(48000, 2, 960, f), 960)
			if err != nil {
				t.Fatal(err)
			}
			info, err := InspectPacket(pkt, 48000)
			if err != nil {
				t.Fatal(err)
			}
			switch app {
			case ApplicationRestrictedSILK:
				if info.Mode != ModeSILKOnly || info.Bandwidth > BandwidthWideband {
					t.Fatalf("restricted SILK packet mode %v bandwidth %d", info.Mode, info.Bandwidth)
				}
			case ApplicationRestrictedCELT:
				if info.Mode != ModeCELTOnly {
					t.Fatalf("restricted CELT packet mode %v", info.Mode)
				}
			}
		}
	}
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetApplication(ApplicationRestrictedSILK); !errors.Is(err, ErrBadArg) {
		t.Fatalf("switching to restricted SILK = %v, want ErrBadArg", err)
	}
	silkOnly, err := NewEncoder(48000, 1, ApplicationRestrictedSILK)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := silkOnly.EncodeFloat(make([]float64, 240), 240); !errors.Is(err, ErrUnsupportedFrameSize) {
		t.Fatalf("5 ms restricted SILK = %v, want ErrUnsupportedFrameSize", err)
	}
	if _, err := silkOnly.EncodeFloat(testTone(48000, 1, 480, 0), 480); err != nil {
		t.Fatalf("10 ms restricted SILK: %v", err)
	}
}

func TestLowBitrateClamp(t *testing.T) {
	for _, policy := range []ModePolicy{ModePolicyLegacy, ModePolicyLibopus} {
		for _, rate := range []int{8000, 48000} {
			for _, ch := range []int{1, 2} {
				enc, err := NewEncoder(rate, ch, ApplicationVOIP)
				if err != nil {
					t.Fatal(err)
				}
				if err := enc.SetModePolicy(policy); err != nil {
					t.Fatal(err)
				}
				if err := enc.SetBitrate(0); !errors.Is(err, ErrBadArg) {
					t.Fatalf("SetBitrate(0) = %v, want ErrBadArg", err)
				}
				// Positive rates below 500 b/s are clamped, as libopus does.
				if err := enc.SetBitrate(100); err != nil {
					t.Fatal(err)
				}
				for _, frameSize := range []int{rate / 400, rate / 50, rate * 3 / 50} {
					for f := 0; f < 3; f++ {
						if _, err := enc.EncodeFloat(testTone(rate, ch, frameSize, f), frameSize); err != nil {
							t.Fatalf("policy %d %d Hz %d ch %d samples: %v", policy, rate, ch, frameSize, err)
						}
					}
				}
			}
		}
	}
	ms, err := NewSurroundEncoder(48000, 6, MappingFamilyVorbis, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.SetBitrate(0); !errors.Is(err, ErrBadArg) {
		t.Fatalf("surround SetBitrate(0) = %v, want ErrBadArg", err)
	}
	if err := ms.SetBitrate(1000); err != nil {
		t.Fatal(err)
	}
	if got := ms.Bitrate(); got != 3000 {
		t.Fatalf("surround bitrate %d, want 500 x 6 channels", got)
	}
	if _, err := ms.EncodeFloat(testTone(48000, 6, 960, 0), 960); err != nil {
		t.Fatal(err)
	}
}
