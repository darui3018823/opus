package audit

import (
	"fmt"
	"testing"

	opus "github.com/darui3018823/opus"
)

// TestGoEncoderPackets: packets from the Go encoder at many settings,
// decoded by both decoders (with 8% loss + FEC where enabled).
func TestGoEncoderPackets(t *testing.T) {
	type cell struct {
		rate, ch, app, br int
		dur               opus.ExpertFrameDuration
		fec, dtx          bool
		src               string
		gain              float32
	}
	durs := []opus.ExpertFrameDuration{opus.ExpertFrameDuration2_5ms, opus.ExpertFrameDuration5ms, opus.ExpertFrameDuration10ms,
		opus.ExpertFrameDuration20ms, opus.ExpertFrameDuration40ms, opus.ExpertFrameDuration60ms,
		opus.ExpertFrameDuration80ms, opus.ExpertFrameDuration100ms, opus.ExpertFrameDuration120ms}
	var cells []cell
	for _, rate := range []int{48000, 16000} {
		for _, ch := range []int{1, 2} {
			for _, d := range durs {
				cells = append(cells, cell{rate, ch, opus.ApplicationAudio, 64000, d, false, false, "music/synthetic_chords_48k_stereo.wav", 1})
				cells = append(cells, cell{rate, ch, opus.ApplicationVOIP, 16000, d, true, false, "clean_speech/cc_speech_48k_mono.wav", 1})
				cells = append(cells, cell{rate, ch, opus.ApplicationVOIP, 32000, d, false, true, "noisy_speech/cc_speech_noise_48k_mono.wav", 1})
			}
			// loud input to provoke decoder overshoot (soft-clip path)
			cells = append(cells, cell{rate, ch, opus.ApplicationAudio, 96000, opus.ExpertFrameDuration20ms, false, false, "music/synthetic_chords_48k_stereo.wav", 4})
			cells = append(cells, cell{rate, ch, opus.ApplicationRestrictedLowDelay, 128000, opus.ExpertFrameDuration10ms, false, false, "mixed/cc_speech_tone_mix_48k_mono.wav", 3})
		}
	}
	for _, c := range cells {
		for _, f := range []int{fmtI16, fmtF32} {
			name := fmt.Sprintf("f%d/%d/%dch/app%d/%dk/dur%d/fec%v/dtx%v/g%g", f, c.rate, c.ch, c.app, c.br/1000, int(c.dur)-5000, c.fec, c.dtx, c.gain)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				enc, err := opus.NewEncoder(c.rate, c.ch, c.app)
				if err != nil {
					t.Fatal(err)
				}
				enc.SetBitrate(c.br)
				if err := enc.SetExpertFrameDuration(c.dur); err != nil {
					t.Fatal(err)
				}
				enc.SetInbandFEC(c.fec)
				if c.fec {
					enc.SetPacketLossPerc(15)
				}
				enc.SetDTX(c.dtx)
				pcm := audio(t, c.src, c.rate, c.ch, 5, c.gain)
				ms := map[opus.ExpertFrameDuration]float64{5001: 2.5, 5002: 5, 5003: 10, 5004: 20, 5005: 40, 5006: 60, 5007: 80, 5008: 100, 5009: 120}[c.dur]
				fs := int(float64(c.rate) * ms / 1000)
				var pk [][]byte
				for off := 0; off+fs*c.ch <= len(pcm); off += fs * c.ch {
					p, err := enc.EncodeFloat32(pcm[off:off+fs*c.ch], fs)
					if err != nil {
						t.Fatal(err)
					}
					pk = append(pk, append([]byte(nil), p...))
				}
				var ops []op
				if c.fec {
					ops = fecOps(pk, c.rate, f, 0.08, int64(c.rate+c.ch), 1, true)
				} else {
					for _, p := range pk {
						if len(p) <= 2 { // DTX: opus_demo style PLC of the packet duration via decode
							ops = append(ops, op{code: 1, fmt: f, frameSize: fs, data: p})
							continue
						}
						ops = append(ops, op{code: 1, fmt: f, frameSize: 5760 * c.rate / 48000, data: p})
					}
				}
				cfg, g := newSingle(t, c.rate, c.ch)
				st := compare(t, cfg, ops, g, cmpOpts{checkDur: true, checkBW: true})
				t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
				if !st.exact() || st.durMis+st.bwMis > 0 {
					t.Errorf("DIFF")
				}
			})
		}
	}
}
