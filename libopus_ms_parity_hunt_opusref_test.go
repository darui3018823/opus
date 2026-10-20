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
