//go:build opusref

package opus

import (
	"fmt"
	"math"
	"testing"
)

func auditLCG(seed uint32) func() float32 {
	s := seed
	return func() float32 {
		s = s*1664525 + 1013904223
		return float32(int32(s>>8)-(1<<23)) / float32(1<<23)
	}
}

// auditSynth returns n frames of a synthetic edge signal (48 kHz-style,
// rate-aware) for ch channels.
func auditSynth(kind string, rate, ch, samples int) []float32 {
	out := make([]float32, samples*ch)
	rnd := auditLCG(12345)
	for i := 0; i < samples; i++ {
		tm := float64(i) / float64(rate)
		for c := 0; c < ch; c++ {
			var v float64
			switch kind {
			case "silence":
				v = 0
			case "lsbnoise":
				v = float64(rnd()) / 32768
			case "nearsilence":
				v = 1e-6 * math.Sin(2*math.Pi*440*tm)
			case "denormal":
				v = 1e-40 * float64(rnd())
			case "square":
				if math.Sin(2*math.Pi*(220+float64(c)*3)*tm) >= 0 {
					v = 1
				} else {
					v = -1
				}
			case "clip2":
				v = 2 * math.Sin(2*math.Pi*300*tm)
			case "dc":
				v = 0.5
			case "impulse":
				if i%(rate/7) == 0 {
					v = 1
				}
			case "hftone":
				v = 0.5 * math.Sin(2*math.Pi*float64(rate)*0.47*tm)
			case "tone19k":
				v = 0.5 * math.Sin(2*math.Pi*19000*tm)
			case "whitefs":
				v = float64(rnd())
			case "chirp":
				f := 50 + (float64(rate)/2-100)*math.Mod(tm, 4)/4
				v = 0.4 * math.Sin(2*math.Pi*f*tm*0.5)
			case "nan":
				v = 0.3 * math.Sin(2*math.Pi*300*tm)
				if i%997 == 0 {
					v = math.NaN()
				}
			case "inf":
				v = 0.3 * math.Sin(2*math.Pi*300*tm)
				if i%1499 == 0 {
					v = math.Inf(1 - 2*(i&1))
				}
			case "huge":
				v = 1e6 * math.Sin(2*math.Pi*300*tm)
			case "antiphase":
				v = 0.5 * math.Sin(2*math.Pi*440*tm)
				if c == 1 {
					v = -v
				}
			case "leftonly":
				if c == 0 {
					v = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
			case "gaps":
				// speech-ish bursts, silence, low noise (for DTX)
				p := math.Mod(tm, 3.0)
				switch {
				case p < 1.0:
					v = 0.3 * math.Sin(2*math.Pi*180*tm) * (0.6 + 0.4*math.Sin(2*math.Pi*3*tm))
					v += 0.05 * float64(rnd())
				case p < 2.0:
					v = 0
				default:
					v = 0.0005 * float64(rnd())
				}
			}
			out[i*ch+c] = float32(v)
		}
	}
	return out
}

// TestAuditEdgeInputs: edge signals at several bitrates and rate modes.
func TestAuditEdgeInputs(t *testing.T) {
	auditSkip(t)
	kinds := []string{"silence", "lsbnoise", "nearsilence", "denormal", "square", "clip2", "dc", "impulse", "hftone", "tone19k", "whitefs", "chirp", "nan", "inf", "huge", "antiphase", "leftonly"}
	for _, kind := range kinds {
		for _, rate := range []int{48000, 16000} {
			for _, ch := range []int{1, 2} {
				if (kind == "antiphase" || kind == "leftonly") && ch == 1 {
					continue
				}
				for _, br := range []int{10000, 32000, 128000} {
					for _, vbr := range []int{0, 1} {
						for _, in16 := range []bool{false, true} {
							if in16 && (kind == "nan" || kind == "inf" || kind == "denormal" || kind == "nearsilence") {
								continue
							}
							kind, rate, ch, br, vbr, in16 := kind, rate, ch, br, vbr, in16
							name := fmt.Sprintf("%s/%dk/ch%d/%dk/vbr%d/int16=%v", kind, rate/1000, ch, br/1000, vbr, in16)
							t.Run(name, func(t *testing.T) {
								t.Parallel()
								fs := rate / 50
								frames := 150
								c := aCfg{name: name, rate: rate, ch: ch, app: "audio", profile: true}
								c.pcm = auditSynth(kind, rate, ch, frames*fs)
								if kind != "nan" && kind != "inf" && kind != "denormal" && kind != "nearsilence" && kind != "huge" && kind != "clip2" {
									c.pcm = auditSnap16(c.pcm)
								}
								op := byte('e')
								if in16 {
									op = 'i'
								}
								c.steps = append([]aStep{cs("bitrate", br), cs("complexity", 9), cs("vbr", vbr)}, framesSteps(frames, fs, op, nil)...)
								auditReport(t, c)
							})
						}
					}
				}
			}
		}
	}
}

// TestAuditBitrateExtremes: every input rate, frame size, and extreme bitrates.
func TestAuditBitrateExtremes(t *testing.T) {
	auditSkip(t)
	srcs := auditLoadSources(t)
	music := srcs[6] // tv05
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, ch := range []int{1, 2} {
			for _, us := range []int{2500, 5000, 10000, 20000, 40000, 60000, 80000, 100000, 120000} {
				for _, br := range []int{500, 4000, 6000, 9000, 256000 * ch, 510000 * ch, 750000 * ch, BitrateMax, BitrateAuto} {
					for _, vbr := range []int{0, 1} {
						rate, ch, us, br, vbr := rate, ch, us, br, vbr
						name := fmt.Sprintf("%dk/ch%d/%gms/br%d/vbr%d", rate/1000, ch, float64(us)/1000, br, vbr)
						t.Run(name, func(t *testing.T) {
							t.Parallel()
							fs := rate * us / 1000000
							frames := max(10, 2000000/us)
							c := aCfg{name: name, rate: rate, ch: ch, app: "audio", profile: true}
							c.pcm = auditInput(music, rate, ch, frames, fs)
							c.steps = append([]aStep{{op: 'q'}, cs("bitrate", br), cs("complexity", 10), cs("vbr", vbr)}, framesSteps(frames, fs, 'e', nil)...)
							auditReport(t, c)
						})
					}
				}
			}
		}
	}
}

// TestAuditLookahead: OPUS_GET_LOOKAHEAD for every rate and application.
func TestAuditLookahead(t *testing.T) {
	auditSkip(t)
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, app := range []string{"voip", "audio", "lowdelay", "rsilk", "rcelt"} {
			c := aCfg{name: fmt.Sprintf("la/%d/%s", rate, app), rate: rate, ch: 1, app: app, profile: true}
			c.steps = []aStep{{op: 'q'}}
			auditReport(t, c)
		}
	}
}
