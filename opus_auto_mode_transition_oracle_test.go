//go:build opusref

package opus

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestAutoModeTransitionOracle compares the Go encoder with the libopus
// mode policy against the instrumented libopus encoder over a bitrate
// schedule, so that the automatic decisions switch mode, bandwidth or
// channel count mid-stream (SILK<->hybrid<->CELT redundancy, the to_celt
// deferral, the SILK re-init and prefill, the stereo<->mono transitions).
func TestAutoModeTransitionOracle(t *testing.T) {
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skipf("encoder oracle not built (%s): run pwsh scripts/oracle/build_encoder.ps1", encOraclePath())
	}
	const (
		rate      = 48000
		frameSize = rate / 50
		frames    = 16
	)
	type transCase struct {
		name     string
		schedule []int
		channels int
		signal   string
		app      string
		exact    bool
	}
	// Each schedule switches at frame 6 (and some back at frame 11).
	sched := func(a, b int) []int {
		s := make([]int, 0, 7)
		for i := 0; i < 6; i++ {
			s = append(s, a)
		}
		return append(s, b)
	}
	sched3 := func(a, b, c int) []int {
		s := sched(a, b)
		for len(s) < 11 {
			s = append(s, b)
		}
		return append(s, c)
	}
	var cases []transCase
	for _, app := range []string{"voip", "audio"} {
		for _, signal := range []string{"voice", "music", "auto"} {
			for _, channels := range []int{1, 2} {
				for _, sw := range [][2]int{{12000, 64000}, {64000, 12000}, {24000, 128000}, {128000, 24000}, {12000, 128000}, {128000, 12000}, {32000, 48000}, {48000, 32000}} {
					cases = append(cases, transCase{
						name:     fmt.Sprintf("%s/%s/ch%d/%dk-%dk", app, signal, channels, sw[0]/1000, sw[1]/1000),
						schedule: sched(sw[0], sw[1]),
						channels: channels,
						signal:   signal,
						app:      app,
					})
				}
				cases = append(cases, transCase{
					name:     fmt.Sprintf("%s/%s/ch%d/12k-128k-12k", app, signal, channels),
					schedule: sched3(12000, 128000, 12000),
					channels: channels,
					signal:   signal,
					app:      app,
				})
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parts := make([]string, len(tc.schedule))
			for i, b := range tc.schedule {
				parts[i] = strconv.Itoa(b)
			}
			ref := runCELTOracleCmd(t, "ref-speech", "--auto-enc", "48000", "ref-speech", strconv.Itoa(frames), strings.Join(parts, ","), "1", strconv.Itoa(tc.channels), "5", tc.signal, tc.app)
			app := ApplicationVOIP
			if tc.app == "audio" {
				app = ApplicationAudio
			}
			enc, err := NewEncoder(rate, tc.channels, app)
			if err != nil {
				t.Fatal(err)
			}
			enc.SetLibopusModePolicy(true)
			switch tc.signal {
			case "voice":
				enc.SetSignalType(SignalVoice)
			case "music":
				enc.SetSignalType(SignalMusic)
			}
			enc.SetVBR(true)
			enc.SetVBRConstraint(true)
			if err := enc.SetComplexity(5); err != nil {
				t.Fatal(err)
			}
			identical := 0
			tocMatch := 0
			firstDiff := -1
			bitrate := -1
			for f := 0; f < frames; f++ {
				idx := f
				if idx >= len(tc.schedule) {
					idx = len(tc.schedule) - 1
				}
				if tc.schedule[idx] != bitrate {
					bitrate = tc.schedule[idx]
					if err := enc.SetBitrate(bitrate); err != nil {
						t.Fatal(err)
					}
				}
				var pcm []float64
				if tc.channels == 2 {
					pcm = silkRefSpeechFrameStereo(rate, f*frameSize, frameSize)
				} else {
					pcm = encOracleRefSpeechFrame(rate, f*frameSize, frameSize)
				}
				for i, v := range pcm {
					pcm[i] = math.Floor(float64(float32(v))*32768+0.5) / 32768
				}
				pkt, err := enc.EncodeFloat(pcm, frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", f, err)
				}
				r := ref[f]
				if !r.havePacket {
					t.Fatalf("frame %d: incomplete oracle trace", f)
				}
				if pkt[0] == r.packet[0] {
					tocMatch++
				}
				if bytes.Equal(pkt, r.packet) {
					identical++
				} else if firstDiff < 0 {
					firstDiff = f
					prefix := 0
					for prefix < len(pkt) && prefix < len(r.packet) && pkt[prefix] == r.packet[prefix] {
						prefix++
					}
					t.Logf("frame %d (%d bps): Go %d B TOC %#x / C %d B TOC %#x, common prefix %d", f, bitrate, len(pkt), pkt[0], len(r.packet), r.packet[0], prefix)
				}
			}
			t.Logf("%s: TOC %d/%d, packets %d/%d byte-identical (first difference at frame %d)", tc.name, tocMatch, frames, identical, frames, firstDiff)
			if tc.exact && identical != frames {
				t.Errorf("%s: %d/%d packets byte-identical, want all", tc.name, identical, frames)
			}
		})
	}
}
