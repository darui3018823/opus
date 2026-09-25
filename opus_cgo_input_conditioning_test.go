//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
	"github.com/darui3018823/opus/internal/silk"
)

// TestCGOInputConditioningExact compares the Go ports of hp_cutoff,
// dc_reject, and the variable_HP_smth2_Q15 smoother with the libopus float
// bodies compiled by the reference C compiler, on identical float32 input
// with filter state carried across frames. Outputs and state are compared
// by float32 bits; the fixed-point cutoff path by exact integers.
func TestCGOInputConditioningExact(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())
	if got, want := variableHPSmth2Initial(), cgoref.HPMinCutoffLog(); got != want {
		t.Fatalf("initial variable_HP_smth2_Q15: Go=%d C=%d", got, want)
	}

	// Cutoff smoother: drive it with the SILK-side values it can see.
	smth2Go, smth2C := variableHPSmth2Initial(), cgoref.HPMinCutoffLog()
	for i, hz := range []int32{60, 60, 100, 100, 100, 75, 60, 90, 100, 60} {
		target := silk.Lin2Log(hz) << 8
		smth2Go = silkSMLAWBMacro(smth2Go, target-smth2Go, variableHPSmthQ16)
		cutGo := silk.Log2Lin(smth2Go >> 8)
		var cutC int32
		smth2C, cutC = cgoref.HPSmooth(smth2C, target)
		if smth2Go != smth2C || cutGo != cutC {
			t.Fatalf("step %d: smth2 Go=%d C=%d cutoff Go=%d C=%d", i, smth2Go, smth2C, cutGo, cutC)
		}
	}

	type fixture struct {
		rate, channels int
	}
	fixtures := []fixture{{8000, 1}, {12000, 2}, {16000, 1}, {24000, 2}, {48000, 1}, {48000, 2}}
	const frames = 6
	for _, fx := range fixtures {
		frameSize := fx.rate / 50
		gen := func(frame int) ([]float64, []float32) {
			n := frameSize * fx.channels
			in64 := make([]float64, n)
			in32 := make([]float32, n)
			state := uint32(fx.rate + 31*fx.channels + 7*frame)
			for i := 0; i < frameSize; i++ {
				tm := float64(frame*frameSize+i) / float64(fx.rate)
				for c := 0; c < fx.channels; c++ {
					state = state*1664525 + 1013904223
					noise := float64(int32(state)) / 2147483648.0
					v := 0.4*math.Sin(2*math.Pi*(120+40*float64(c))*tm) + 0.2*math.Sin(2*math.Pi*1700*tm) + 0.15*noise
					if frame == 3 {
						v *= 4 // drive the high-pass hard once
					}
					f := float32(v)
					in64[i*fx.channels+c] = float64(f)
					in32[i*fx.channels+c] = f
				}
			}
			return in64, in32
		}
		for _, cutoff := range []int32{60, 73, 100} {
			var memGo, memC [4]float32
			for frame := 0; frame < frames; frame++ {
				in64, in32 := gen(frame)
				outGo := make([]float64, len(in64))
				hpCutoff(in64, cutoff, outGo, &memGo, frameSize, fx.channels, fx.rate)
				outC, err := cgoref.HPCutoff(in32, cutoff, &memC, frameSize, fx.channels, fx.rate)
				if err != nil {
					t.Fatal(err)
				}
				for i := range outC {
					if float32(outGo[i]) != outC[i] {
						t.Fatalf("%d Hz/%dch cutoff %d frame %d sample %d: hp_cutoff Go=%.9g (%08x) C=%.9g (%08x)",
							fx.rate, fx.channels, cutoff, frame, i, outGo[i], math.Float32bits(float32(outGo[i])), outC[i], math.Float32bits(outC[i]))
					}
				}
				if memGo != memC {
					t.Fatalf("%d Hz/%dch cutoff %d frame %d: hp_mem Go=%v C=%v", fx.rate, fx.channels, cutoff, frame, memGo, memC)
				}
			}
		}
		var memGo, memC [4]float32
		for frame := 0; frame < frames; frame++ {
			in64, in32 := gen(frame)
			outGo := make([]float64, len(in64))
			dcReject(in64, dcRejectCutoffHz, outGo, &memGo, frameSize, fx.channels, fx.rate)
			outC, err := cgoref.DCReject(in32, dcRejectCutoffHz, &memC, frameSize, fx.channels, fx.rate)
			if err != nil {
				t.Fatal(err)
			}
			for i := range outC {
				if float32(outGo[i]) != outC[i] {
					t.Fatalf("%d Hz/%dch frame %d sample %d: dc_reject Go=%.9g C=%.9g", fx.rate, fx.channels, frame, i, outGo[i], outC[i])
				}
			}
			if memGo != memC {
				t.Fatalf("%d Hz/%dch frame %d: dc_reject hp_mem Go=%v C=%v", fx.rate, fx.channels, frame, memGo, memC)
			}
		}
		t.Logf("%d Hz/%dch: hp_cutoff (60/73/100 Hz) and dc_reject exact over %d frames", fx.rate, fx.channels, frames)
	}
}
