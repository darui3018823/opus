package audit

import (
	"fmt"
	"math/rand"
	"testing"

	opus "github.com/darui3018823/opus"
)

const (
	appVOIP     = 2048
	appAudio    = 2049
	appLowDelay = 2051
	sigVoice    = 3001
	sigMusic    = 3002
)

// fecOps builds an opus_demo-style decode sequence: a lost packet is
// recovered with FEC from the next packet when that one arrived, otherwise
// concealed. fecDiv splits the FEC request (1 = full duration).
func fecOps(pk [][]byte, rate, f int, lossP float64, seed int64, fecDiv int, useFEC bool) []op {
	rng := rand.New(rand.NewSource(seed))
	lost := make([]bool, len(pk))
	for i := range lost {
		lost[i] = i > 0 && rng.Float64() < lossP
	}
	var ops []op
	for i, p := range pk {
		dur, _ := opus.PacketGetNumSamples(p, rate)
		if !lost[i] {
			ops = append(ops, op{code: 1, fmt: f, frameSize: 5760 * rate / 48000, data: p})
			continue
		}
		if useFEC && i+1 < len(pk) && !lost[i+1] {
			if fecDiv > 1 {
				// PLC the first part, FEC the last part (libopus FEC
				// always fills the tail with LBRR).
				ops = append(ops, op{code: 1, fmt: f, frameSize: dur - dur/fecDiv})
				ops = append(ops, op{code: 1, fmt: f, fec: true, frameSize: dur / fecDiv, data: pk[i+1]})
			} else {
				ops = append(ops, op{code: 1, fmt: f, fec: true, frameSize: dur, data: pk[i+1]})
			}
			continue
		}
		ops = append(ops, op{code: 1, fmt: f, frameSize: dur})
	}
	return ops
}

func TestFECLibopusPackets(t *testing.T) {
	type cell struct {
		rate, ch, br, frameUs, app int
	}
	var cells []cell
	for _, rate := range []int{8000, 16000, 24000, 48000} {
		for _, ch := range []int{1, 2} {
			for _, br := range []int{12000, 24000, 40000} {
				for _, us := range []int{10000, 20000, 40000, 60000} {
					cells = append(cells, cell{rate, ch, br, us, appVOIP})
				}
			}
		}
	}
	// CELT-only carriers: FEC on a CELT packet must fall back to PLC.
	for _, ch := range []int{1, 2} {
		cells = append(cells, cell{48000, ch, 96000, 20000, appLowDelay})
		cells = append(cells, cell{48000, ch, 64000, 10000, appAudio})
	}
	for _, c := range cells {
		for _, f := range []int{fmtI16, fmtF32, fmtI24} {
			for _, div := range []int{1, 2} {
				if div == 2 && c.frameUs < 20000 {
					continue
				}
				name := fmt.Sprintf("f%d/%d/%dch/%dk/%dms/app%d/div%d", f, c.rate, c.ch, c.br/1000, c.frameUs/1000, c.app, div)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					fs := c.rate * c.frameUs / 1000000
					pcm := audio(t, "clean_speech/cc_speech_48k_mono.wav", c.rate, c.ch, 6, 1)
					enc := oracleEncode(t, encCfg{kind: 0, rate: c.rate, ch: c.ch, app: c.app, bitrate: c.br, complexity: 9,
						fec: 1, loss: 20, vbr: 1, cvbr: 0, signal: sigVoice, frameSize: fs}, pcm)
					ops := fecOps(enc.packets, c.rate, f, 0.2, int64(c.rate+c.ch+c.br+c.frameUs), div, true)
					cfg, g := newSingle(t, c.rate, c.ch)
					st := compare(t, cfg, ops, g, cmpOpts{checkDur: true})
					t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
					if !st.exact() || st.durMis > 0 {
						t.Errorf("DIFF")
					}
				})
			}
		}
	}
}
