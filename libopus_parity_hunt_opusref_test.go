//go:build opusref

package opus

import (
	"fmt"
	"os"
	"testing"
)

// Regression cells for the libopus differences found by the 2026-10 parity
// hunt. Each cell runs the Go encoder and the plain-C audit oracle
// (scripts/oracle/build_encoder_audit.ps1) on the same input and controls and
// requires every packet and final range to match.

func huntSkip(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(auditOraclePath()); err != nil {
		t.Skip("audit oracle not built: run scripts/oracle/build_encoder_audit.ps1")
	}
}

type huntCell struct {
	src       string
	rate, ch  int
	app       string
	frames    int
	frameSize int
	ctl       []aStep
	at        map[int][]aStep
}

func (c huntCell) name() string {
	s := fmt.Sprintf("%s/%dk/ch%d/%s/fs%d", c.src, c.rate/1000, c.ch, c.app, c.frameSize)
	for _, st := range c.ctl {
		s += fmt.Sprintf("/%s=%d", st.ctl, st.val)
	}
	return s
}

func runHuntCells(t *testing.T, cells []huntCell) {
	t.Helper()
	huntSkip(t)
	srcs := map[string]auditSrc{}
	for _, s := range auditLoadSources(t) {
		srcs[s.name] = s
	}
	for _, hc := range cells {
		hc := hc
		t.Run(hc.name(), func(t *testing.T) {
			t.Parallel()
			src, ok := srcs[hc.src]
			if !ok {
				t.Fatalf("no source %q", hc.src)
			}
			c := aCfg{name: hc.name(), rate: hc.rate, ch: hc.ch, app: hc.app, profile: true}
			c.pcm = auditInput(src, hc.rate, hc.ch, hc.frames, hc.frameSize)
			frames := min(hc.frames, len(c.pcm)/hc.ch/hc.frameSize)
			c.steps = append(append([]aStep{}, hc.ctl...), framesSteps(frames, hc.frameSize, 'e', hc.at)...)
			auditReport(t, c)
		})
	}
}

// TestHuntProbe is a scratch matrix used to find triggers.
func TestHuntProbe(t *testing.T) {
	if os.Getenv("OPUS_HUNT_PROBE") == "" {
		t.Skip("OPUS_HUNT_PROBE not set")
	}
	huntSkip(t)
	srcs := map[string]auditSrc{}
	for _, s := range auditLoadSources(t) {
		srcs[s.name] = s
	}
	for _, pw := range []struct {
		l, r string
		w    float32
	}{{"tv02", "tv05", 0.02}, {"tv02", "tv05", 0.1}, {"tv08", "tv11", 0.05}, {"tv06", "tv12", 0.2}, {"speech24s", "tv02", 0.05}} {
		l, r := srcs[pw.l], srcs[pw.r]
		n := min(len(l.pcm)/l.ch, len(r.pcm)/r.ch)
		mixed := auditSrc{name: fmt.Sprintf("%s+%s*%g", pw.l, pw.r, pw.w), ch: 2, pcm: make([]float32, 2*n)}
		for i := 0; i < n; i++ {
			mixed.pcm[2*i] = l.pcm[i*l.ch]
			mixed.pcm[2*i+1] = (1-pw.w)*l.pcm[i*l.ch] + pw.w*r.pcm[i*r.ch]
		}
		for _, br := range []int{16000, 20000, 24000} {
			for _, sw := range []int{20, 60} {
				name := fmt.Sprintf("%s/%dk/sw%d", mixed.name, br/1000, sw)
				c := aCfg{name: name, rate: 48000, ch: 2, app: "audio", profile: true}
				c.pcm = auditInput(mixed, 48000, 2, (sw*960+1000*480)/480, 480)
				c.steps = []aStep{cs("bitrate", 64000), cs("fec", 1), cs("loss", 10), cs("maxbw", 1101), cs("signal", 3001)}
				for f := 0; f < sw; f++ {
					c.steps = append(c.steps, aStep{op: 'e', val: 960})
				}
				c.steps = append(c.steps, cs("maxbw", 1105), cs("bitrate", br))
				for f := 0; f < 1000 && (sw*960+(f+1)*480)*2 <= len(c.pcm); f++ {
					c.steps = append(c.steps, aStep{op: 'e', val: 480})
				}
				auditReport(t, c)
			}
		}
	}
	var cells []huntCell
	runHuntCells(t, cells)
}

// TestHuntThetaRDOLastBand: with theta RDO (stereo, complexity >= 8) the
// last band keeps lowband_scratch (bands.c: `if (last && !theta_rdo)`), so
// the round-down pass does not fold from a modified norm.
func TestHuntThetaRDOLastBand(t *testing.T) {
	var cells []huntCell
	for _, src := range []string{"tv06", "tv08", "stspeech"} {
		cells = append(cells, huntCell{src: src, rate: 48000, ch: 2, app: "audio", frames: 600, frameSize: 960,
			ctl: []aStep{cs("bitrate", 160000), cs("complexity", 10)}})
	}
	runHuntCells(t, cells)
}

// TestHuntNegativeEquivRate: 2.5 ms stereo frames at 20 kb/s give a negative
// equiv_rate, which libopus still uses (stereo width 0) instead of
// recomputing it.
func TestHuntNegativeEquivRate(t *testing.T) {
	var cells []huntCell
	for _, app := range []string{"lowdelay", "rcelt", "audio"} {
		cells = append(cells, huntCell{src: "tv08", rate: 48000, ch: 2, app: app, frames: 4000, frameSize: 120,
			ctl: []aStep{cs("bitrate", 20000)}})
	}
	runHuntCells(t, cells)
}

// TestHuntToCELTPrevMode: a hybrid packet that defers a switch to CELT
// (to_celt) makes CELT-only the next packet's prev_mode even when the
// redundant frame does not fit the low CBR budget.
func TestHuntToCELTPrevMode(t *testing.T) {
	var cells []huntCell
	for _, ch := range []int{1, 2} {
		for _, fs := range []int{960, 1920, 2880} {
			cells = append(cells, huntCell{src: "tv11", rate: 48000, ch: ch, app: "audio", frames: 600 * 960 / fs, frameSize: fs,
				ctl: []aStep{cs("bitrate", 16000), cs("vbr", 0)}})
		}
	}
	runHuntCells(t, cells)
}

// TestHuntFECMultiFrame: the SILK prefill runs silk_setup_LBRR, and the LBRR
// bit usage is reset for every later frame of a 40/60 ms packet.
func TestHuntFECMultiFrame(t *testing.T) {
	cells := []huntCell{
		{src: "tv05", rate: 48000, ch: 1, app: "voip", frames: 300, frameSize: 1920},
		{src: "tv02", rate: 48000, ch: 2, app: "audio", frames: 200, frameSize: 2880},
		{src: "speech24s", rate: 48000, ch: 1, app: "audio", frames: 300, frameSize: 1920},
		{src: "noisy", rate: 48000, ch: 1, app: "voip", frames: 300, frameSize: 1920},
	}
	for i := range cells {
		cells[i].ctl = []aStep{cs("bitrate", 24000), cs("fec", 1), cs("loss", 10)}
	}
	runHuntCells(t, cells)
}

// TestHuntLBRRQuantOffset: silk_LBRR_encode_FLP copies the indices before the
// rate loop, so the redundant frame keeps process_gains' quantOffsetType
// when the loop lowers it for the regular frame.
func TestHuntLBRRQuantOffset(t *testing.T) {
	var cells []huntCell
	for _, fs := range []int{1920, 2880} {
		cells = append(cells, huntCell{src: "tv11", rate: 48000, ch: 2, app: "audio", frames: 600 * 960 / fs, frameSize: fs,
			ctl: []aStep{cs("bitrate", 32000), cs("fec", 1), cs("loss", 10)}})
	}
	cells = append(cells, huntCell{src: "speech24s", rate: 48000, ch: 2, app: "voip", frames: 300, frameSize: 1920,
		ctl: []aStep{cs("bitrate", 32000), cs("fec", 1), cs("loss", 10)}})
	runHuntCells(t, cells)
}

// TestHuntStereoToMonoPrefill: a stereo -> mono switch that lands on a SILK
// bandwidth-switch prefill averages the new-rate mono frame with the side
// channel's old-rate resampler and inputBuf, and the prefill itself ends
// with nPrevChannelsInternal = 1.
func TestHuntStereoToMonoPrefill(t *testing.T) {
	var cells []huntCell
	for _, src := range []string{"tv08", "tv11", "mix", "chords"} {
		cells = append(cells, huntCell{src: src, rate: 48000, ch: 2, app: "audio", frames: 1200, frameSize: 480,
			ctl: []aStep{cs("bitrate", 24000), cs("fec", 1), cs("loss", 10)}})
	}
	runHuntCells(t, cells)
}
