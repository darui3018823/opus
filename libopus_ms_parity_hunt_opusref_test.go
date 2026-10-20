//go:build opusref

package opus

import (
	"fmt"
	"testing"
)

// Multistream / surround / projection regression cells against the plain-C
// libopus 1.6.1 script oracle (scripts/oracle/build_encoder_audit.ps1),
// from the 2026-10-19 parity hunt.

// runMSHuntCells runs multistream cells (family -1: opus_multistream_encoder
// with the surround layout, 0/1/2/255: surround, 2/3: projection for
// projection=true) on music.
func runMSHuntCells(t *testing.T, cells []aCfg, frameSize, frames int) {
	t.Helper()
	huntSkip(t)
	srcs := auditLoadSources(t)
	music := srcs[6]
	for _, c := range cells {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.pcm = auditMultiInput(music, c.rate, c.ch, frames*frameSize)
			c.steps = append(c.steps, framesSteps(frames, frameSize, 'e', nil)...)
			auditReport(t, c)
		})
	}
}

// TestHuntMultistreamLibopusProfile: the *WithProfile constructors with
// EncoderProfileLibopus code the packets of libopus encoders that keep
// their create-time defaults (automatic bitrate, complexity 9, constrained
// VBR from opus_encoder_init); no CTL is sent to the oracle.
func TestHuntMultistreamLibopusProfile(t *testing.T) {
	var cells []aCfg
	for _, l := range []struct{ family, ch int }{{-1, 3}, {-1, 6}, {0, 2}, {1, 6}, {1, 8}, {255, 5}, {2, 9}, {3, 4}, {3, 9}} {
		for _, app := range []string{"voip", "audio"} {
			cells = append(cells, aCfg{
				name: fmt.Sprintf("fam%d/ch%d/%s", l.family, l.ch, app), rate: 48000, ch: l.ch, app: app,
				ms: true, family: l.family, profile: true,
			})
		}
	}
	runMSHuntCells(t, cells, 960, 150)
}

// TestHuntMultistreamExpertFrameDuration: the multistream
// OPUS_SET_EXPERT_FRAME_DURATION is stored by the multistream encoder
// (opus_multistream_encoder.c:1303) and applied by its own
// frame_size_select (opus_multistream_encoder.c:888); the streams code
// that frame size while their tonality analysis reads the caller's whole
// frame (analysis_pcm, analysis_frame_size).
func TestHuntMultistreamExpertFrameDuration(t *testing.T) {
	type dur struct {
		expert, frameSize int
	}
	var cells []aCfg
	for _, d := range []dur{{5003, 960}, {5004, 1920}, {5006, 2880}, {5002, 960}} {
		for _, l := range []struct{ family, ch int }{{-1, 6}, {1, 6}, {2, 9}, {3, 4}} {
			for _, app := range []string{"voip", "audio"} {
				cells = append(cells, aCfg{
					name: fmt.Sprintf("fam%d/ch%d/%s/expert%d/fs%d", l.family, l.ch, app, d.expert, d.frameSize),
					rate: 48000, ch: l.ch, app: app, ms: true, family: l.family, profile: true,
					steps: []aStep{cs("expert", d.expert)},
				})
			}
		}
		for i := len(cells) - 8; i < len(cells); i++ {
			cells[i].steps = append(cells[i].steps, framesSteps(60, d.frameSize, 'e', nil)...)
		}
	}
	huntSkip(t)
	music := auditLoadSources(t)[6]
	for _, c := range cells {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.pcm = auditMultiInput(music, c.rate, c.ch, 60*2880)
			auditReport(t, c)
		})
	}
}
