package audit

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	opus "github.com/darui3018823/opus"
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

// speechSrc is the full-level speech seed of the real corpus (the derived
// clips are nearly silent).
const speechSrc = "source/cc_speech_test01_20s.wav"

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

// TestHuntSILKStereoFECSide: in the LBRR frames of a stereo SILK packet the
// side channel is decoded or concealed only when the previous frame coded
// it or the side LBRR flag is set (has_side), and the side prediction
// memory is reset on the first frame without the mid-only flag after a
// mid-only frame. Alternating mono and wide stereo input produces mid-only
// frames next to coded side frames.
func TestHuntSILKStereoFECSide(t *testing.T) {
	for _, rate := range []int{16000, 48000} {
		for _, br := range []int{24000, 32000, 48000} {
			for _, us := range []int{20000, 40000, 60000} {
				t.Run(fmt.Sprintf("%d/%dk/%dms", rate, br/1000, us/1000), func(t *testing.T) {
					t.Parallel()
					src := audio(t, speechSrc, rate, 1, 12, 1)
					n := rate * 8
					pcm := make([]float32, 2*n)
					seg := rate * 2 / 3
					for i := range n {
						pcm[2*i] = src[i]
						if (i/seg)%2 == 0 {
							pcm[2*i+1] = src[i]
						} else {
							pcm[2*i+1] = src[i+rate*3]
						}
					}
					fs := rate * us / 1000000
					enc := oracleEncode(t, encCfg{rate: rate, ch: 2, app: appVOIP, bitrate: br, complexity: 9,
						fec: 1, loss: 30, vbr: 1, forcech: 2, signal: sigVoice, frameSize: fs}, pcm)
					for _, outCh := range []int{1, 2} {
						ops := fecOps(enc.packets, rate, fmtF32, 0.3, int64(rate+br+us), 1, true)
						cfg, g := newSingle(t, rate, outCh)
						requireExact(t, cfg, ops, g)
					}
				})
			}
		}
	}
}

// TestHuntCELTSilenceBackground: a CELT silence frame sets the energy of
// every band, including those outside start..end, to -28 before the
// background energy update. testvector08 (CELT with silence frames and
// bandwidth changes) with 30% random loss exposes it in the noise PLC.
func TestHuntCELTSilenceBackground(t *testing.T) {
	pk := loadBit(t, 8)
	for _, o := range []struct{ rate, ch int }{{48000, 1}, {48000, 2}, {24000, 2}} {
		t.Run(fmt.Sprintf("tv08/%d/%dch", o.rate, o.ch), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(8001))
			var ops []op
			last := o.rate / 50
			for _, p := range pk {
				if len(p.data) == 0 || rng.Float64() < 0.3 {
					ops = append(ops, op{code: 1, fmt: fmtF32, frameSize: last})
					continue
				}
				last, _ = opus.PacketGetNumSamples(p.data, o.rate)
				ops = append(ops, op{code: 1, fmt: fmtF32, frameSize: 5760 * o.rate / 48000, data: p.data})
			}
			cfg, g := newSingle(t, o.rate, o.ch)
			requireExact(t, cfg, ops, g)
		})
	}
}

// fecStream is one oracle-encoded stream of a splice scenario.
type fecStream struct {
	bw, br, forceCh int
}

type fecSegment struct {
	stream, packets int
	keep            bool // no loss at the end of the segment
}

// spliceFECOps decodes the scenario's segments, each from its own stream at
// the same position in time, losing the last packet of every segment and
// recovering it with FEC from the first packet of the next one.
func spliceFECOps(t *testing.T, rate, ch, fs int, streams []fecStream, segs []fecSegment) []op {
	t.Helper()
	pcm := audio(t, speechSrc, rate, ch, 6, 1)
	var pk [][][]byte
	for _, s := range streams {
		pk = append(pk, oracleEncode(t, encCfg{rate: rate, ch: ch, app: appVOIP, bitrate: s.br, complexity: 9,
			fec: 1, loss: 30, vbr: 1, bandwidth: s.bw, forcech: s.forceCh, signal: sigVoice, frameSize: fs}, pcm).packets)
	}
	var ops []op
	pos := 0
	for si, sg := range segs {
		for k := 0; k < sg.packets; k++ {
			p := pk[sg.stream][pos]
			switch {
			case k == sg.packets-1 && si+1 < len(segs) && !sg.keep:
				next := pk[segs[si+1].stream][pos+1]
				n, _ := opus.PacketGetNumSamples(p, rate)
				ops = append(ops, op{code: 1, fmt: fmtF32, fec: true, frameSize: n, data: next})
			default:
				ops = append(ops, op{code: 1, fmt: fmtF32, frameSize: 5760 * rate / 48000, data: p})
			}
			pos++
		}
	}
	return ops
}

// TestHuntFECModeSwitch covers in-band FEC across SILK/hybrid mode,
// bandwidth and channel switches: a hybrid FEC frame after a SILK frame
// resets the CELT decoder and conceals its high band (start band 17) up to
// the FEC packet's end band; a SILK FEC frame after a hybrid frame fades the
// CELT MDCT out with a silence frame; and the SILK resamplers follow the
// stream channel count as for a normal packet.
func TestHuntFECModeSwitch(t *testing.T) {
	silkWB := fecStream{bw: bwWB, br: 20000}
	hybSWB := fecStream{bw: 1104, br: 32000}
	hybFB := fecStream{bw: bwFB, br: 40000}
	silkMono := fecStream{bw: bwWB, br: 32000, forceCh: 1}
	silkStereo := fecStream{bw: bwWB, br: 32000, forceCh: 2}
	scenarios := []struct {
		name    string
		ch      int
		streams []fecStream
		segs    []fecSegment
	}{
		{"hybrid-fec-to-silk", 1, []fecStream{hybFB, silkWB, hybSWB}, []fecSegment{{0, 20, false}, {1, 20, true}, {2, 20, false}, {1, 20, true}, {0, 20, false}, {1, 20, false}}},
		{"channel-switch", 2, []fecStream{silkMono, silkStereo}, []fecSegment{{0, 20, false}, {1, 20, false}, {0, 20, false}, {1, 20, false}, {0, 20, false}}},
	}
	for _, sc := range scenarios {
		for _, rate := range []int{48000, 16000} {
			for _, ms := range []int{10, 20} {
				for _, outCh := range []int{1, 2} {
					t.Run(fmt.Sprintf("%s/%d/%dms/out%d", sc.name, rate, ms, outCh), func(t *testing.T) {
						t.Parallel()
						ops := spliceFECOps(t, rate, sc.ch, rate*ms/1000, sc.streams, sc.segs)
						cfg, g := newSingle(t, rate, outCh)
						requireExact(t, cfg, ops, g)
					})
				}
			}
		}
	}
}

// TestHuntFECShortFirstFrame: FEC from a packet whose first frame has at
// most one byte is a loss concealed in the previous mode (opus_decode_frame
// with len <= 1).
func TestHuntFECShortFirstFrame(t *testing.T) {
	for _, rate := range []int{48000, 16000} {
		for _, ch := range []int{1, 2} {
			t.Run(fmt.Sprintf("%d/%dch", rate, ch), func(t *testing.T) {
				t.Parallel()
				fs := rate / 50
				pcm := audio(t, speechSrc, rate, ch, 4, 1)
				enc := func(bw, br int) [][]byte {
					return oracleEncode(t, encCfg{rate: rate, ch: ch, app: appVOIP, bitrate: br, complexity: 9,
						fec: 1, loss: 30, vbr: 1, bandwidth: bw, signal: sigVoice, frameSize: fs}, pcm).packets
				}
				// SILK and hybrid packets alternate every 10 packets; the
				// FEC carriers of the losses are cut to their TOC or to a
				// one-byte frame, from the same or the other mode.
				streams := [2][][]byte{enc(bwWB, 20000), enc(bwFB, 40000)}
				var ops []op
				for i := range streams[0] {
					p := streams[(i/10)%2][i]
					if i%5 == 4 && i+1 < len(streams[0]) {
						next := streams[((i+1)/10)%2][i+1]
						short := []byte{next[0]}
						if i%10 == 4 {
							short = []byte{next[0], next[1]}
						}
						ops = append(ops, op{code: 1, fmt: fmtF32, fec: true, frameSize: fs, data: short})
						continue
					}
					ops = append(ops, op{code: 1, fmt: fmtF32, frameSize: 5760 * rate / 48000, data: p})
				}
				cfg, g := newSingle(t, rate, ch)
				requireExact(t, cfg, ops, g)
			})
		}
	}
}
