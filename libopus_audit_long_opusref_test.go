//go:build opusref

package opus

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// auditEnabled skips the libopus audit unless OPUS_LIBOPUS_AUDIT is set: it
// reports known residual differences as failures, so it is opt-in.
func auditEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("OPUS_LIBOPUS_AUDIT") == "" {
		t.Skip("libopus audit: set OPUS_LIBOPUS_AUDIT=1")
	}
}

// auditSkip additionally needs the script oracle
// (scripts/oracle/build_encoder_audit.ps1).
func auditSkip(t *testing.T) {
	t.Helper()
	auditEnabled(t)
	if _, err := os.Stat(auditOraclePath()); err != nil {
		t.Skip("audit oracle not built: run scripts/oracle/build_encoder_audit.ps1")
	}
}

// auditSources returns named 48 kHz inputs (interleaved, with channel count).
type auditSrc struct {
	name string
	ch   int
	pcm  []float32
}

func auditLoadSources(t *testing.T) []auditSrc {
	var out []auditSrc
	_, ch, sp := auditReadWAV(t, filepath.Join("testdata", "real_corpus", "source", "cc_speech_test01_20s.wav"))
	out = append(out, auditSrc{"speech24s", ch, sp})
	for _, p := range []struct{ name, path string }{
		{"noisy", "noisy_speech/cc_speech_noise_48k_mono.wav"},
		{"mix", "mixed/cc_speech_tone_mix_48k_mono.wav"},
		{"chords", "music/synthetic_chords_48k_stereo.wav"},
		{"stspeech", "stereo_speech/cc_speech_48k_stereo.wav"},
	} {
		_, ch, pcm := auditReadWAV(t, filepath.Join("testdata", "real_corpus", p.path))
		out = append(out, auditSrc{p.name, ch, pcm})
	}
	for _, tv := range []string{"02", "05", "06", "08", "11", "12"} {
		out = append(out, auditSrc{"tv" + tv, 2, auditReadDec(t, "testvector"+tv+".dec")})
	}
	return out
}

func auditInput(src auditSrc, rate, ch, frames, frameSize int) []float32 {
	p := auditToChannels(src.pcm, src.ch, ch)
	p = auditResample(p, ch, 48000, rate)
	p = auditSnap16(p)
	need := frames * frameSize * ch
	if len(p) > need {
		p = p[:need]
	}
	return p
}

func TestAuditSanity(t *testing.T) {
	auditSkip(t)
	srcs := auditLoadSources(t)
	c := aCfg{name: "sanity", rate: 48000, ch: 1, app: "voip", profile: true}
	c.pcm = auditInput(srcs[0], 48000, 1, 50, 960)
	c.steps = append([]aStep{{op: 'q'}, cs("bitrate", 24000), cs("complexity", 5)}, framesSteps(50, 960, 'e', nil)...)
	auditReport(t, c)
	c.name = "sanity-voice"
	c.steps = append([]aStep{{op: 'q'}, cs("bitrate", 24000), cs("complexity", 5), cs("signal", 3001)}, framesSteps(50, 960, 'e', nil)...)
	auditReport(t, c)
	c.name = "sanity-refspeech"
	c.pcm = nil
	for f := 0; f < 50; f++ {
		for _, v := range encOracleRefSpeechFrame(48000, f*960, 960) {
			c.pcm = append(c.pcm, float32(v))
		}
	}
	c.pcm = auditSnap16(c.pcm)
	auditReport(t, c)
}

// TestAuditLongReal: long runs on real audio, broad config.
func TestAuditLongReal(t *testing.T) {
	auditSkip(t)
	srcs := auditLoadSources(t)
	type rm struct {
		name      string
		vbr, vbrc int
	}
	for _, src := range srcs {
		for _, rate := range []int{48000, 16000} {
			for _, ch := range []int{1, 2} {
				for _, br := range []int{6000, 12000, 20000, 32000, 64000, 128000} {
					for _, mode := range []rm{{"cvbr", 1, 1}, {"vbr", 1, 0}, {"cbr", 0, 1}} {
						for _, cpx := range []int{10, 4} {
							for _, app := range []string{"voip", "audio"} {
								if (cpx == 4) != (app == "audio") {
									continue
								}
								src, rate, ch, br, mode, cpx, app := src, rate, ch, br, mode, cpx, app
								name := fmt.Sprintf("%s/%dk/ch%d/%dk/%s/c%d/%s", src.name, rate/1000, ch, br/1000, mode.name, cpx, app)
								t.Run(name, func(t *testing.T) {
									t.Parallel()
									fs := rate / 50
									frames := min(1200, len(src.pcm)/src.ch/960)
									c := aCfg{name: name, rate: rate, ch: ch, app: app, profile: true}
									c.pcm = auditInput(src, rate, ch, frames, fs)
									c.steps = append([]aStep{cs("bitrate", br), cs("complexity", cpx), cs("vbr", mode.vbr), cs("vbrc", mode.vbrc)},
										framesSteps(frames, fs, 'e', nil)...)
									auditReport(t, c)
								})
							}
						}
					}
				}
			}
		}
	}
}
