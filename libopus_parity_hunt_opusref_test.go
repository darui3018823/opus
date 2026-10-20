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
	var cells []huntCell
	for _, src := range []string{"speech24s", "stspeech", "tv02", "tv05", "tv08", "tv11", "tv12"} {
		for _, step := range []int{500, 1000} {
			for _, fs := range []int{1920, 2880, 3840} {
				for _, vbr := range []int{0, 1} {
					c := huntSweepCell(src, "rsilk", vbr, step)
					c.frames, c.frameSize = 1500*960/fs, fs
					c.ctl = append(c.ctl, cs("fec", 1), cs("loss", 10))
					cells = append(cells, c)
				}
			}
		}
	}
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

// TestHuntCBRMultiFramePadding: a CBR packet of several frames is padded to
// cbr_bytes with the repacketizer's padding-count run, also when the
// padding amount is 255k+1.
func TestHuntCBRMultiFramePadding(t *testing.T) {
	cells := []huntCell{
		{src: "tv08", rate: 8000, ch: 1, app: "rsilk", frames: 250, frameSize: 960},
		{src: "tv11", rate: 16000, ch: 2, app: "rsilk", frames: 250, frameSize: 1920},
		{src: "tv11", rate: 48000, ch: 1, app: "rsilk", frames: 375, frameSize: 3840},
	}
	for i := range cells {
		cells[i].ctl = []aStep{cs("bitrate", 256000), cs("vbr", 0)}
	}
	runHuntCells(t, cells)
}

// huntSweepCell is a stereo 20 ms stream whose bitrate bounces between 6 and
// 48 kb/s by step every frame (opus_demo -sweep), coded at complexity
// step/250.
func huntSweepCell(src, app string, vbr, step int) huntCell {
	at := map[int][]aStep{}
	br, dir := 6000, 1
	for f := 0; f < 1500; f++ {
		at[f] = []aStep{cs("bitrate", br)}
		br += dir * step
		if br > 48000 || br < 6000 {
			dir = -dir
			br += 2 * dir * step
		}
	}
	return huntCell{src: src, rate: 48000, ch: 2, app: app, frames: 1500, frameSize: 960,
		ctl: []aStep{cs("vbr", vbr), cs("complexity", step/250)}, at: at}
}

// TestHuntMonoToStereoSideInit: on a mono -> stereo switch silk_Encode
// re-initialises the side channel before the control pass, so the side
// takes the "just initialized" branch of silk_control_audio_bandwidth and
// cannot report switchReady or cut maxBits.
func TestHuntMonoToStereoSideInit(t *testing.T) {
	runHuntCells(t, []huntCell{
		huntSweepCell("tv02", "audio", 1, 500),
		huntSweepCell("tv06", "audio", 1, 250),
		huntSweepCell("speech24s", "audio", 1, 1000),
		huntSweepCell("speech24s", "voip", 1, 250),
		huntSweepCell("stspeech", "audio", 1, 1000),
	})
}

// TestHuntSILKRateSwitchWithoutPrefill: opusCanSwitch persists to the next
// SILK call, so when the redundant frame of a prepared bandwidth switch did
// not fit (no prefill) silk_control_encoder switches the internal rate
// itself, resampling x_buf through the new input resampler
// (silk_setup_resamplers) and resetting part of the state (silk_setup_fs);
// a small packet budget also caps the rate (maxInternalSampleRate).
func TestHuntSILKRateSwitchWithoutPrefill(t *testing.T) {
	runHuntCells(t, []huntCell{
		huntSweepCell("tv02", "audio", 0, 500),
		huntSweepCell("tv12", "voip", 0, 250),
		huntSweepCell("tv08", "audio", 0, 1000),
		huntSweepCell("mix", "voip", 0, 1000),
		huntSweepCell("tv11", "voip", 0, 500),
	})
}

// TestHuntHybridPrefillEndBand: opus_encode_frame_native sets
// CELT_SET_END_BAND before the mode-switch reset and prefill, so the
// 2.5 ms hybrid prefill codes the packet's bands, not the previous ones.
func TestHuntHybridPrefillEndBand(t *testing.T) {
	runHuntCells(t, []huntCell{
		huntSweepCell("tv12", "voip", 1, 500),
		huntSweepCell("tv08", "voip", 1, 250),
		huntSweepCell("tv06", "voip", 1, 1000),
		huntSweepCell("tv05", "voip", 1, 250),
	})
}

// TestHuntStereoPrefillLPState: a mono -> stereo switch on a bandwidth
// switch prefill first initialises the side channel, then the prefill
// reset restores the saved sLP into both channels, so the side continues
// the mid's transition.
func TestHuntStereoPrefillLPState(t *testing.T) {
	runHuntCells(t, []huntCell{
		huntSweepCell("tv05", "voip", 0, 1000),
		huntSweepCell("tv05", "audio", 0, 1000),
		huntSweepCell("noisy", "voip", 1, 1000),
	})
}

// TestHuntRestrictedSILKBandwidthSwitch: RESTRICTED_SILK has no CELT
// redundancy, so a prepared SILK bandwidth switch never prefills and
// silk_control_encoder switches the rate on the next packet.
func TestHuntRestrictedSILKBandwidthSwitch(t *testing.T) {
	runHuntCells(t, []huntCell{
		huntSweepCell("tv02", "rsilk", 1, 500),
		huntSweepCell("tv05", "rsilk", 0, 1000),
		huntSweepCell("speech24s", "rsilk", 1, 250),
	})
}

// TestHuntPrefillDelayLine: the bandwidth-switch prefill re-initialises the
// SILK channel, so at an API rate equal to the new SILK rate the input
// resampler (the front end's delay line) restarts from zeros; a mono stream
// keeps only sStereo.sMid[1] in front of the frame.
func TestHuntPrefillDelayLine(t *testing.T) {
	var cells []huntCell
	for _, src := range []string{"tv02", "tv05", "tv08", "speech24s"} {
		cells = append(cells, huntCell{src: src, rate: 12000, ch: 2, app: "voip", frames: 1000, frameSize: 240,
			ctl: []aStep{cs("bitrate", 20000), cs("vbr", 0), cs("fec", 1), cs("loss", 10)}})
		cells = append(cells, huntCell{src: src, rate: 16000, ch: 1, app: "audio", frames: 300, frameSize: 1600,
			ctl: []aStep{cs("bitrate", 32000)}})
	}
	runHuntCells(t, cells)
}

// TestHuntEqualRateSwitchResampler: a mid-stream SILK rate switch from a rate
// equal to the API rate resamples x_buf through the decoder-direction
// resampler, which delays its input even at equal rates (delay_matrix_dec).
func TestHuntEqualRateSwitchResampler(t *testing.T) {
	var cells []huntCell
	for _, cfg := range []struct {
		src      string
		rate, ch int
	}{{"tv02", 12000, 1}, {"tv11", 12000, 2}, {"tv05", 16000, 2}, {"speech24s", 16000, 2}} {
		c := huntSweepCell(cfg.src, "voip", 0, 500)
		c.rate, c.ch, c.frameSize = cfg.rate, cfg.ch, cfg.rate/50
		cells = append(cells, c)
	}
	runHuntCells(t, cells)
}

// TestHuntRateSwitchDropsLBRR: a SILK rate switch sets
// first_frame_after_reset, after which silk_Encode clears the pending LBRR
// flags, so the switch packet carries no redundancy for the previous one.
func TestHuntRateSwitchDropsLBRR(t *testing.T) {
	var cells []huntCell
	for _, src := range []string{"speech24s", "tv02", "tv05", "tv11"} {
		c := huntSweepCell(src, "rsilk", 1, 500)
		c.ctl = append(c.ctl, cs("fec", 1), cs("loss", 25))
		cells = append(cells, c)
	}
	runHuntCells(t, cells)
}

// TestHuntMonoToStereoRateSwitch: when a mono -> stereo switch and a SILK
// rate switch share a call, the side channel starts fresh and copies the
// mid channel's already switched input resampler.
func TestHuntMonoToStereoRateSwitch(t *testing.T) {
	var cells []huntCell
	for _, cfg := range []struct {
		src           string
		vbr, step, fs int
	}{{"tv08", 0, 1000, 3840}, {"tv08", 1, 1000, 1920}, {"tv11", 0, 500, 1920}, {"speech24s", 0, 500, 2880}} {
		c := huntSweepCell(cfg.src, "rsilk", cfg.vbr, cfg.step)
		c.frames, c.frameSize = 1500*960/cfg.fs, cfg.fs
		c.ctl = append(c.ctl, cs("fec", 1), cs("loss", 10))
		cells = append(cells, c)
	}
	runHuntCells(t, cells)
}

// TestHuntStereoToMonoRateSwitch: a stereo -> mono switch on a mid-stream
// rate switch leaves the side channel at its old rate, and only the first
// SILK frame of the packet is averaged with it.
func TestHuntStereoToMonoRateSwitch(t *testing.T) {
	var cells []huntCell
	for _, cfg := range []struct {
		src           string
		vbr, step, fs int
	}{{"tv05", 1, 1000, 1920}, {"tv12", 0, 1000, 1920}, {"speech24s", 0, 500, 1920}, {"tv02", 0, 1000, 2880}} {
		c := huntSweepCell(cfg.src, "rsilk", cfg.vbr, cfg.step)
		c.frames, c.frameSize = 1500*960/cfg.fs, cfg.fs
		c.ctl = append(c.ctl, cs("fec", 1), cs("loss", 10))
		cells = append(cells, c)
	}
	runHuntCells(t, cells)
}
