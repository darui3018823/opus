//go:build opusref

package silk

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestSILKStereoLRToMSOpusRef runs the Go silk_stereo_LR_to_MS port and the
// libopus function side by side over a long, varied stereo stream (rate
// sweeps, panning, near-mono passages, silence, a toMono frame) and requires
// identical mid/side frames, predictor indices, mid-only flags and rates.
func TestSILKStereoLRToMSOpusRef(t *testing.T) {
	for _, fsKHz := range []int{8, 12, 16} {
		for _, frameMs := range []int{10, 20} {
			frameLength := frameMs * fsKHz
			var goState stereoEncState
			goState.reset()
			ref := cgoref.NewSILKStereoLRToMS()
			seed := uint32(0x1234567)
			rnd := func() float64 {
				seed = seed*1664525 + 1013904223
				return float64(int32(seed)) / 2147483648.0
			}
			const frames = 160
			prevAct := 255
			for f := 0; f < frames; f++ {
				left := make([]int16, frameLength)
				right := make([]int16, frameLength)
				pan := 0.5 + 0.5*math.Sin(float64(f)*0.11)
				amp := 6000.0
				switch (f / 20) % 4 {
				case 1:
					amp = 400 // quiet
				case 2:
					amp = 0 // digital silence
				case 3:
					amp = 15000
				}
				for i := 0; i < frameLength; i++ {
					tm := float64(f*frameLength+i) / float64(fsKHz*1000)
					s := math.Sin(2*math.Pi*180*tm) + 0.4*math.Sin(2*math.Pi*370*tm+0.3) + 0.15*rnd()
					d := 0.3*math.Sin(2*math.Pi*215*tm+1.1) + 0.1*rnd()
					l := amp * (pan*s + (1-pan)*d)
					r := amp * ((1-pan)*s + pan*d)
					if f%40 == 39 {
						// Hard-panned frame drives the predictors to the limits.
						l, r = amp*s*2.5, amp*s*0.02
					}
					left[i] = int16(math.Max(-32768, math.Min(32767, math.Round(l))))
					right[i] = int16(math.Max(-32768, math.Min(32767, math.Round(r))))
				}
				totalRate := int32(8000 + (f*1731)%40000)
				toMono := f == 77
				got := goState.lrToMS(left, right, fsKHz, frameLength, totalRate, prevAct, toMono)
				want := ref.Run(left, right, fsKHz, frameLength, totalRate, prevAct, toMono)
				if got.ix != want.Ix || got.midOnly != want.MidOnly || got.midSideRates != want.MidSideRates {
					t.Fatalf("fs %d frame %d ms, frame %d: ix Go %v C %v; midOnly Go %v C %v; rates Go %v C %v",
						fsKHz, frameMs, f, got.ix, want.Ix, got.midOnly, want.MidOnly, got.midSideRates, want.MidSideRates)
				}
				for i := 0; i < frameLength; i++ {
					if got.mid[i] != want.Mid[i] || got.side[i] != want.Side[i] {
						t.Fatalf("fs %d frame %d ms, frame %d sample %d: mid Go %d C %d, side Go %d C %d",
							fsKHz, frameMs, f, i, got.mid[i], want.Mid[i], got.side[i], want.Side[i])
					}
				}
				prevAct = int(seed>>24) & 0xff
			}
		}
	}
}
