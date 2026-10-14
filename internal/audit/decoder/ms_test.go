package audit

import (
	"fmt"
	"math/rand"
	"testing"

	opus "github.com/darui3018823/opus"
)

// multichannel builds an n-channel signal from the stereo music and mono speech files.
func multichannel(t testing.TB, rate, n int, seconds float64) []float32 {
	sp := audio(t, "clean_speech/cc_speech_48k_mono.wav", rate, 1, seconds, 0.8)
	mu := audio(t, "music/synthetic_chords_48k_stereo.wav", rate, 2, seconds, 0.8)
	frames := len(sp)
	out := make([]float32, frames*n)
	for i := 0; i < frames; i++ {
		for c := 0; c < n; c++ {
			var v float32
			switch c % 3 {
			case 0:
				v = sp[i]
			case 1:
				v = mu[2*i]
			default:
				v = 0.5*mu[2*i+1] + 0.3*sp[(i+37*c)%frames]
			}
			out[i*n+c] = v
		}
	}
	return out
}

func msOps(pk [][]byte, rate, streams, f int, loss float64, seed int64, fec bool, extra []op) []op {
	rng := rand.New(rand.NewSource(seed))
	ops := append([]op(nil), extra...)
	lost := make([]bool, len(pk))
	for i := range lost {
		lost[i] = i > 0 && rng.Float64() < loss
	}
	last := rate / 50
	for i, p := range pk {
		if lost[i] {
			if fec && i+1 < len(pk) && !lost[i+1] {
				ops = append(ops, op{code: 1, fmt: f, fec: true, frameSize: last, data: pk[i+1]})
			} else {
				ops = append(ops, op{code: 1, fmt: f, frameSize: last})
			}
			continue
		}
		// duration of the first stream's packet
		n, err := opus.PacketGetNumSamples(firstStreamPacket(p, streams), rate)
		if err == nil {
			last = n
		}
		ops = append(ops, op{code: 1, fmt: f, frameSize: 5760 * rate / 48000, data: p})
	}
	return ops
}

// firstStreamPacket returns the self-delimited first stream's TOC-led bytes
// (sufficient for duration).
func firstStreamPacket(p []byte, streams int) []byte {
	if streams == 1 || len(p) < 2 {
		return p
	}
	// a self-delimited packet's TOC byte suffices for the per-frame
	// duration; PacketGetNumSamples needs a parseable packet, so build a
	// code-0 one-frame packet with the same TOC config.
	toc := p[0]
	if toc&3 == 0 {
		return []byte{toc, 0}
	}
	return p
}

func TestMultistreamSurround(t *testing.T) {
	type cell struct {
		kind, family, ch, rate, br, frameUs, app int
		loss                                     float64
		fec                                      bool
	}
	var cells []cell
	for _, ch := range []int{1, 2, 3, 4, 5, 6, 7, 8} {
		for _, rate := range []int{48000, 16000} {
			cells = append(cells, cell{1, 1, ch, rate, 64000 * ch, 20000, appAudio, 0, false})
			cells = append(cells, cell{1, 1, ch, rate, 64000 * ch, 20000, appAudio, 0.1, false})
			cells = append(cells, cell{1, 1, ch, rate, 16000 * ch, 20000, appVOIP, 0.15, true})
		}
	}
	for _, ch := range []int{3, 6} {
		cells = append(cells, cell{1, 255, ch, 48000, 48000 * ch, 10000, appAudio, 0.1, false})
	}
	// Ambisonics family 2 (multistream) and family 3 (projection).
	for _, ch := range []int{4, 9, 11, 16} {
		cells = append(cells, cell{3, 2, ch, 48000, 32000 * ch, 20000, appAudio, 0, false})
		cells = append(cells, cell{3, 2, ch, 48000, 32000 * ch, 20000, appAudio, 0.1, false})
		if ch != 11 {
			cells = append(cells, cell{2, 3, ch, 48000, 32000 * ch, 20000, appAudio, 0, false})
			cells = append(cells, cell{2, 3, ch, 48000, 32000 * ch, 20000, appAudio, 0.1, false})
		}
	}
	for _, c := range cells {
		for _, f := range []int{fmtI16, fmtF32, fmtI24} {
			name := fmt.Sprintf("k%d/fam%d/%dch/%d/%dk/app%d/loss%g/fec%v/f%d", c.kind, c.family, c.ch, c.rate, c.br/1000, c.app, c.loss, c.fec, f)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fs := c.rate * c.frameUs / 1000000
				pcm := multichannel(t, c.rate, c.ch, 4)
				fecOn := 0
				if c.fec {
					fecOn = 1
				}
				enc := oracleEncode(t, encCfg{kind: c.kind, rate: c.rate, ch: c.ch, app: c.app, family: c.family, bitrate: c.br, complexity: 9,
					fec: fecOn, loss: 15 * fecOn, vbr: 1, frameSize: fs}, pcm)
				var cfg decCfg
				var g goDecoder
				if c.kind == 2 {
					d, err := opus.NewProjectionDecoder(c.rate, c.ch, enc.streams, enc.coupled, enc.mapping)
					if err != nil {
						t.Fatal(err)
					}
					cfg = decCfg{kind: 2, rate: c.rate, ch: c.ch, streams: enc.streams, coupl: enc.coupled, mapping: enc.mapping}
					g = projDec{d}
				} else {
					d, err := opus.NewMultistreamDecoder(c.rate, c.ch, enc.streams, enc.coupled, enc.mapping)
					if err != nil {
						t.Fatal(err)
					}
					cfg = decCfg{kind: 1, rate: c.rate, ch: c.ch, streams: enc.streams, coupl: enc.coupled, mapping: enc.mapping}
					g = msDec{d}
				}
				ops := msOps(enc.packets, c.rate, enc.streams, f, c.loss, int64(c.ch*7+c.rate), c.fec, nil)
				st := compare(t, cfg, ops, g, cmpOpts{checkDur: c.kind != 2, checkBW: c.kind != 2})
				t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
				if !st.exact() || st.durMis+st.bwMis > 0 {
					t.Errorf("DIFF")
				}
			})
		}
	}
}

// TestMultistreamCTLs: gain / phase inversion / reset on multistream.
func TestMultistreamCTLs(t *testing.T) {
	for _, ch := range []int{2, 6} {
		for _, variant := range []string{"gain+3dB", "gain-6dB", "phaseinv", "reset"} {
			for _, f := range []int{fmtI16, fmtF32} {
				name := fmt.Sprintf("%dch/%s/f%d", ch, variant, f)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					rate := 48000
					pcm := multichannel(t, rate, ch, 4)
					enc := oracleEncode(t, encCfg{kind: 1, rate: rate, ch: ch, app: appAudio, family: 1, bitrate: 48000 * ch, complexity: 9, vbr: 1, frameSize: 960}, pcm)
					d, err := opus.NewMultistreamDecoder(rate, ch, enc.streams, enc.coupled, enc.mapping)
					if err != nil {
						t.Fatal(err)
					}
					cfg := decCfg{kind: 1, rate: rate, ch: ch, streams: enc.streams, coupl: enc.coupled, mapping: enc.mapping}
					var extra []op
					switch variant {
					case "gain+3dB":
						extra = []op{{code: 3, val: 768}}
					case "gain-6dB":
						extra = []op{{code: 3, val: -1536}}
					case "phaseinv":
						extra = []op{{code: 4, val: 1}}
					}
					ops := msOps(enc.packets, rate, enc.streams, f, 0.05, 3, false, extra)
					if variant == "reset" {
						var o2 []op
						for i, o := range ops {
							if i%25 == 24 {
								o2 = append(o2, op{code: 2})
							}
							o2 = append(o2, o)
						}
						ops = o2
					}
					st := compare(t, cfg, ops, msDec{d}, cmpOpts{checkDur: true, checkBW: true})
					t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
					if !st.exact() {
						t.Errorf("DIFF")
					}
				})
			}
		}
	}
}
