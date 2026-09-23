//go:build opusref

package opus

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
)

// TestAutoModeOracleSweep is an exploratory sweep over more bitrates, CBR
// and complexities than TestAutoModeOracle. It only reports.
func TestAutoModeOracleSweep(t *testing.T) {
	if os.Getenv("OPUS_ORACLE_SWEEP") == "" {
		t.Skip("set OPUS_ORACLE_SWEEP=1")
	}
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skip("encoder oracle not built")
	}
	const (
		rate      = 48000
		frameSize = rate / 50
		frames    = 12
	)
	type cell struct {
		bitrate, channels, complexity int
		vbr                           bool
		signal, app                   string
	}
	var cells []cell
	for _, app := range []string{"voip", "audio"} {
		for _, signal := range []string{"voice", "music", "auto"} {
			for _, channels := range []int{1, 2} {
				for _, bitrate := range []int{8000, 16000, 20000, 32000, 40000, 64000, 96000, 160000, 256000} {
					for _, vbr := range []bool{true, false} {
						for _, cpx := range []int{0, 10} {
							cells = append(cells, cell{bitrate, channels, cpx, vbr, signal, app})
						}
					}
				}
			}
		}
	}
	bad := 0
	for _, tc := range cells {
		name := fmt.Sprintf("%s/%s/ch%d/%dk/vbr%v/c%d", tc.app, tc.signal, tc.channels, tc.bitrate/1000, tc.vbr, tc.complexity)
		vbrArg := "0"
		if tc.vbr {
			vbrArg = "1"
		}
		ref := runCELTOracleCmd(t, "ref-speech", "--auto-enc", "48000", "ref-speech", strconv.Itoa(frames), strconv.Itoa(tc.bitrate), vbrArg, strconv.Itoa(tc.channels), strconv.Itoa(tc.complexity), tc.signal, tc.app)
		app := ApplicationVOIP
		if tc.app == "audio" {
			app = ApplicationAudio
		}
		enc, err := NewEncoder(rate, tc.channels, app)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetModePolicy(ModePolicyLibopus); err != nil {
			t.Fatal(err)
		}
		switch tc.signal {
		case "voice":
			enc.SetSignalType(SignalVoice)
		case "music":
			enc.SetSignalType(SignalMusic)
		}
		if err := enc.SetBitrate(tc.bitrate); err != nil {
			t.Fatal(err)
		}
		enc.SetVBR(tc.vbr)
		enc.SetVBRConstraint(true)
		if err := enc.SetComplexity(tc.complexity); err != nil {
			t.Fatal(err)
		}
		identical := 0
		firstDiff := -1
		var firstToc [2]byte
		var firstLen [2]int
		for f := 0; f < frames; f++ {
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
				t.Fatalf("%s frame %d: %v", name, f, err)
			}
			r := ref[f]
			if bytes.Equal(pkt, r.packet) {
				identical++
			} else if firstDiff < 0 {
				firstDiff = f
				firstToc = [2]byte{pkt[0], r.packet[0]}
				firstLen = [2]int{len(pkt), len(r.packet)}
			}
		}
		if identical != frames {
			bad++
			t.Logf("%s: %d/%d identical, first diff frame %d Go TOC %#x %dB / C TOC %#x %dB", name, identical, frames, firstDiff, firstToc[0], firstLen[0], firstToc[1], firstLen[1])
		}
	}
	t.Logf("%d/%d cells differ", bad, len(cells))
}
