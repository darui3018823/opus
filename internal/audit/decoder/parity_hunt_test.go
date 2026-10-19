package audit

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// Regression cells for the decoder differences found by the 2026-10 libopus
// parity hunt. Each cell decodes the same operations with the plain-C
// libopus 1.6.1 oracle and requires every output sample and final range to
// match.

const (
	bwNB = 1101
	bwWB = 1103
	bwFB = 1105
)

func requireExact(t *testing.T, cfg decCfg, ops []op, g goDecoder) {
	t.Helper()
	st := compare(t, cfg, ops, g, cmpOpts{checkDur: true})
	t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
	if !st.exact() || st.durMis > 0 {
		t.Errorf("DIFF")
	}
}

// TestHuntCELTBackgroundAcrossBandwidth: the CELT noise PLC floor
// (backgroundLogE) of the bands above a narrower packet's end band is kept
// and keeps rising while narrower packets are decoded, as libopus keeps one
// CELT decoder whatever the bandwidth.
func TestHuntCELTBackgroundAcrossBandwidth(t *testing.T) {
	for _, rate := range []int{48000, 16000} {
		for _, ch := range []int{1, 2} {
			for _, narrow := range []int{bwNB, bwWB} {
				t.Run(fmt.Sprintf("%d/%dch/bw%d", rate, ch, narrow), func(t *testing.T) {
					t.Parallel()
					fs := rate / 50
					// Speech (whose pauses pull the high-band background
					// energy down), then loud noise (high-band energy far
					// above it).
					pcm := audio(t, "clean_speech/cc_speech_48k_mono.wav", rate, ch, 3, 1)
					rng := rand.New(rand.NewSource(1))
					for range 3 * rate * ch {
						pcm = append(pcm, float32(math.Round((rng.Float64()-0.5)*0.6*32768)/32768))
					}
					enc := func(bw int) [][]byte {
						return oracleEncode(t, encCfg{rate: rate, ch: ch, app: appLowDelay, bitrate: 48000, complexity: 9,
							vbr: 1, bandwidth: bw, signal: sigMusic, frameSize: fs}, pcm).packets
					}
					wide, narrowPk := enc(bwFB), enc(narrow)
					var ops []op
					for i := range wide {
						switch {
						case i < 100, i >= 150 && i < 170, i >= 220:
							ops = append(ops, op{code: 1, fmt: fmtF32, frameSize: 5760 * rate / 48000, data: wide[i]})
						case i < 150:
							ops = append(ops, op{code: 1, fmt: fmtF32, frameSize: 5760 * rate / 48000, data: narrowPk[i]})
						default:
							// A loss long enough for the noise PLC to decay
							// to the background floor.
							ops = append(ops, op{code: 1, fmt: fmtF32, frameSize: fs})
						}
					}
					cfg, g := newSingle(t, rate, ch)
					requireExact(t, cfg, ops, g)
				})
			}
		}
	}
}
