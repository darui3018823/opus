//go:build opusref

package opus

import (
	"fmt"
	"testing"
)

// TestAuditControls: controls changed mid-stream.
func TestAuditControls(t *testing.T) {
	auditSkip(t)
	srcs := auditLoadSources(t)
	speech, music := srcs[0], srcs[6]
	gapsFor := func(rate, ch, n int) []float32 { return auditSnap16(auditSynth("gaps", rate, ch, n)) }
	type scen struct {
		name string
		at   func(f int) []aStep
	}
	every := func(period int, vals []int, ctl string) func(int) []aStep {
		return func(f int) []aStep {
			if f%period == 0 && f > 0 {
				return []aStep{cs(ctl, vals[(f/period)%len(vals)])}
			}
			return nil
		}
	}
	scens := []scen{
		{"bitrate-jumps", every(37, []int{6000, 510000, 8000, 128000, 12000, 64000, 500, 24000, 256000, 16000, 40000, 96000}, "bitrate")},
		{"bitrate-every-frame", func(f int) []aStep {
			b := []int{9000, 17000, 33000, 70000, 140000, 11000}
			return []aStep{cs("bitrate", b[(f*7)%len(b)])}
		}},
		{"complexity", every(23, []int{0, 10, 3, 7, 1, 9, 5, 2, 8, 4, 6}, "complexity")},
		{"vbr", every(41, []int{0, 1}, "vbr")},
		{"vbrc", every(29, []int{0, 1}, "vbrc")},
		{"vbr-vbrc-order", func(f int) []aStep {
			switch f % 120 {
			case 30:
				return []aStep{cs("vbr", 0)}
			case 50:
				return []aStep{cs("vbrc", 0)} // while CBR
			case 70:
				return []aStep{cs("vbr", 1)} // libopus: unconstrained now
			case 100:
				return []aStep{cs("vbrc", 1)}
			}
			return nil
		}},
		{"bandwidth", every(31, []int{1101, 1102, 1103, 1104, 1105, -1000}, "bw")},
		{"maxbw", every(31, []int{1101, 1105, 1102, 1104, 1103}, "maxbw")},
		{"signal", every(33, []int{3001, 3002, -1000}, "signal")},
		{"application", every(45, []int{2049, 2048}, "app")},
		{"application-lowdelay", every(45, []int{2051}, "app")},
		{"fec-loss", func(f int) []aStep {
			switch f % 160 {
			case 20:
				return []aStep{cs("fec", 1), cs("loss", 10)}
			case 60:
				return []aStep{cs("loss", 50)}
			case 90:
				return []aStep{cs("fec", 2)}
			case 120:
				return []aStep{cs("loss", 0)}
			case 140:
				return []aStep{cs("fec", 0), cs("loss", 25)}
			}
			return nil
		}},
		{"lsb", every(27, []int{8, 24, 12, 16, 20}, "lsb")},
		{"pred", every(35, []int{1, 0}, "pred")},
		{"phaseinv", every(35, []int{1, 0}, "phaseinv")},
		{"reset", func(f int) []aStep {
			if f%57 == 30 {
				return []aStep{{op: 'r'}}
			}
			return nil
		}},
	}
	for _, src := range []auditSrc{speech, music} {
		for _, rate := range []int{48000, 16000} {
			for _, ch := range []int{1, 2} {
				for _, base := range []int{16000, 48000} {
					for _, sc := range scens {
						src, rate, ch, base, sc := src, rate, ch, base, sc
						name := fmt.Sprintf("%s/%s/%dk/ch%d/%dk", sc.name, src.name, rate/1000, ch, base/1000)
						t.Run(name, func(t *testing.T) {
							t.Parallel()
							fs := rate / 50
							frames := 480
							c := aCfg{name: name, rate: rate, ch: ch, app: "voip", profile: true}
							c.pcm = auditInput(src, rate, ch, frames, fs)
							c.steps = append([]aStep{cs("bitrate", base), cs("complexity", 9)}, framesSteps(frames, fs, 'e', map[int][]aStep{})...)
							var steps []aStep
							steps = append(steps, cs("bitrate", base), cs("complexity", 9))
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
		}
	}
	// DTX toggling on a gappy signal.
	for _, rate := range []int{48000, 16000} {
		for _, ch := range []int{1, 2} {
			for _, base := range []int{12000, 32000} {
				for _, vbr := range []int{0, 1} {
					rate, ch, base, vbr := rate, ch, base, vbr
					name := fmt.Sprintf("dtx-toggle/%dk/ch%d/%dk/vbr%d", rate/1000, ch, base/1000, vbr)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						fs := rate / 50
						frames := 600
						c := aCfg{name: name, rate: rate, ch: ch, app: "voip", profile: true}
						c.pcm = gapsFor(rate, ch, frames*fs)
						steps := []aStep{cs("bitrate", base), cs("complexity", 9), cs("vbr", vbr)}
						for f := 0; f < frames; f++ {
							if f%70 == 10 {
								steps = append(steps, cs("dtx", (f/70+1)%2))
							}
							steps = append(steps, aStep{op: 'e', val: fs})
						}
						c.steps = steps
						auditReport(t, c)
					})
				}
			}
		}
	}
	// Forced channels toggling (stereo input).
	for _, src := range []auditSrc{srcs[4], music} {
		for _, base := range []int{16000, 64000} {
			src, base := src, base
			name := fmt.Sprintf("fc-toggle/%s/%dk", src.name, base/1000)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fs := 960
				frames := min(480, len(src.pcm)/src.ch/fs)
				c := aCfg{name: name, rate: 48000, ch: 2, app: "audio", profile: true}
				c.pcm = auditInput(src, 48000, 2, frames, fs)
				steps := []aStep{cs("bitrate", base), cs("complexity", 9)}
				for f := 0; f < frames; f++ {
					if f%13 == 5 {
						steps = append(steps, cs("fc", []int{1, 2, -1000}[(f/13)%3]))
					}
					steps = append(steps, aStep{op: 'e', val: fs})
				}
				c.steps = steps
				auditReport(t, c)
			})
		}
	}
}

// TestAuditFrameSizes: variable frame size per call and expert durations.
func TestAuditFrameSizes(t *testing.T) {
	auditSkip(t)
	srcs := auditLoadSources(t)
	for _, src := range []auditSrc{srcs[0], srcs[6]} {
		for _, rate := range []int{48000, 16000, 8000} {
			for _, ch := range []int{1, 2} {
				for _, br := range []int{12000, 32000, 96000} {
					src, rate, ch, br := src, rate, ch, br
					name := fmt.Sprintf("varsize/%s/%dk/ch%d/%dk", src.name, rate/1000, ch, br/1000)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						sizesUs := []int{20000, 10000, 60000, 2500, 40000, 5000, 20000, 120000, 10000, 80000, 20000, 100000, 2500, 20000}
						c := aCfg{name: name, rate: rate, ch: ch, app: "voip", profile: true}
						steps := []aStep{cs("bitrate", br), cs("complexity", 9)}
						total := 0
						for k := 0; k < 300; k++ {
							n := rate * sizesUs[(k*5+k/7)%len(sizesUs)] / 1000000
							steps = append(steps, aStep{op: 'e', val: n})
							total += n
						}
						c.pcm = auditInput(src, rate, ch, 1, total)
						c.steps = steps
						auditReport(t, c)
					})
					name2 := fmt.Sprintf("expert/%s/%dk/ch%d/%dk", src.name, rate/1000, ch, br/1000)
					t.Run(name2, func(t *testing.T) {
						t.Parallel()
						c := aCfg{name: name2, rate: rate, ch: ch, app: "audio", profile: true}
						steps := []aStep{cs("bitrate", br), cs("complexity", 9)}
						exp := []int{5004, 5001, 5003, 5006, 5002, 5009, 5005, 5000, 5007, 5008, 5010}
						total := 0
						for k := 0; k < 300; k++ {
							if k%20 == 0 {
								steps = append(steps, cs("expert", exp[(k/20)%len(exp)]))
							}
							n := rate * 120 / 1000 // always pass 120 ms; expert selects
							if exp[(k/20)%len(exp)] == 5000 {
								n = rate / 50
							}
							steps = append(steps, aStep{op: 'e', val: n})
							total += n
						}
						c.pcm = auditInput(src, rate, ch, 1, total)
						c.steps = steps
						auditReport(t, c)
					})
				}
			}
		}
	}
}
