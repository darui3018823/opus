//go:build opusref

package opus

import (
	"fmt"
	"testing"
)

func auditMultiInput(src auditSrc, rate, ch, samples int) []float32 {
	p := auditResample(src.pcm, src.ch, 48000, rate)
	n := len(p) / src.ch
	out := make([]float32, samples*ch)
	for i := 0; i < samples; i++ {
		for c := 0; c < ch; c++ {
			j := i - 37*c
			if j < 0 || j >= n {
				continue
			}
			g := float32(1.0)
			for k := 0; k < c%6; k++ {
				g *= 0.85
			}
			out[i*ch+c] = p[j*src.ch+c%src.ch] * g
		}
	}
	return auditSnap16(out)
}

// TestAuditMultistream: multistream / surround / projection beyond the
// oracle test cells, on real music.
func TestAuditMultistream(t *testing.T) {
	auditSkip(t)
	srcs := auditLoadSources(t)
	music, speech := srcs[6], srcs[0]
	type lay struct{ family, ch int }
	lays := []lay{{-1, 3}, {-1, 6}, {0, 1}, {0, 2}, {1, 1}, {1, 2}, {1, 3}, {1, 4}, {1, 5}, {1, 6}, {1, 7}, {1, 8}, {255, 1}, {255, 5}, {255, 11},
		{2, 4}, {2, 6}, {2, 9}, {2, 11}, {2, 16}, {3, 4}, {3, 9}, {3, 16}}
	for _, l := range lays {
		for _, br := range []int{BitrateAuto, 12000, 96000, 600000, BitrateMax} {
			for _, us := range []int{2500, 10000, 20000, 60000} {
				for _, vbr := range []int{0, 1} {
					src := music
					if us == 10000 {
						src = speech
					}
					l, br, us, vbr, src := l, br, us, vbr, src
					rate := 48000
					if us == 60000 {
						rate = 16000
					}
					name := fmt.Sprintf("fam%d/ch%d/br%d/%gms/vbr%d/%dk/%s", l.family, l.ch, br, float64(us)/1000, vbr, rate/1000, src.name)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						fs := rate * us / 1000000
						frames := max(20, 3000000/us)
						c := aCfg{name: name, rate: rate, ch: l.ch, app: "audio", ms: true, family: l.family}
						c.pcm = auditMultiInput(src, rate, l.ch, frames*fs)
						c.steps = append([]aStep{{op: 'q'}, cs("bitrate", br), cs("complexity", 9), cs("vbr", vbr), cs("vbrc", 1)}, framesSteps(frames, fs, 'e', nil)...)
						auditReport(t, c)
					})
				}
			}
		}
	}
	// Mid-stream controls on 5.1.
	type scen struct {
		name string
		at   func(f int) []aStep
	}
	scens := []scen{
		{"bitrate", func(f int) []aStep {
			if f%17 == 3 {
				return []aStep{cs("bitrate", []int{24000, 512000, 64000, BitrateAuto, 160000, 9000}[(f/17)%6])}
			}
			return nil
		}},
		{"complexity", func(f int) []aStep {
			if f%19 == 3 {
				return []aStep{cs("complexity", (f/19*7)%11)}
			}
			return nil
		}},
		{"bw", func(f int) []aStep {
			if f%21 == 3 {
				return []aStep{cs("bw", []int{1101, 1103, 1105, -1000, 1104}[(f/21)%5])}
			}
			return nil
		}},
		{"dtx-fec-loss", func(f int) []aStep {
			switch f % 90 {
			case 10:
				return []aStep{cs("dtx", 1), cs("fec", 1), cs("loss", 15)}
			case 50:
				return []aStep{cs("dtx", 0), cs("loss", 0)}
			}
			return nil
		}},
		{"reset", func(f int) []aStep {
			if f%61 == 40 {
				return []aStep{{op: 'r'}}
			}
			return nil
		}},
		{"vbr", func(f int) []aStep {
			if f%25 == 7 {
				return []aStep{cs("vbr", (f/25)%2)}
			}
			return nil
		}},
		{"signal-app", func(f int) []aStep {
			if f%30 == 7 {
				return []aStep{cs("signal", []int{3001, 3002, -1000}[(f/30)%3])}
			}
			return nil
		}},
	}
	for _, l := range []lay{{1, 6}, {1, 8}, {3, 9}, {-1, 6}} {
		for _, sc := range scens {
			l, sc := l, sc
			name := fmt.Sprintf("ctl-%s/fam%d/ch%d", sc.name, l.family, l.ch)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fs := 960
				frames := 300
				c := aCfg{name: name, rate: 48000, ch: l.ch, app: "audio", ms: true, family: l.family}
				c.pcm = auditMultiInput(music, 48000, l.ch, frames*fs)
				steps := []aStep{cs("bitrate", 256000), cs("complexity", 9)}
				for f := 0; f < frames; f++ {
					steps = append(steps, sc.at(f)...)
					steps = append(steps, aStep{op: 'e', val: fs})
				}
				c.steps = steps
				auditReport(t, c)
			})
		}
	}
}
