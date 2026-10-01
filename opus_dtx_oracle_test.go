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

// refSpeechGapsSample is the oracle's ref-speech-gaps fixture: a 2.4 s cycle
// of ref-speech (0-0.4 s), digital silence (0.4-0.9 s), noise at about
// -60 dBFS (0.9-1.4 s), noise of a few LSBs (1.4-2.3 s) and ref-speech again
// (2.3-2.4 s). The noise is a hash of the sample index, so any frame size
// sees the same signal.
func refSpeechGapsSample(rate int, idx int, ch int) float64 {
	period := rate * 12 / 5
	pos := idx % period
	seg := 0
	switch {
	case pos < rate*2/5:
	case pos < rate*9/10:
		seg = 1
	case pos < rate*7/5:
		seg = 2
	case pos < rate*23/10:
		seg = 3
	}
	switch seg {
	case 1:
		return 0
	case 2, 3:
		h := uint32(idx)*2654435761 + uint32(ch)*40503 + 12345
		h ^= h >> 15
		h *= 2246822519
		h ^= h >> 13
		amp := 0.0002
		if seg == 2 {
			amp = 0.002
		}
		return (float64(h&0xffff)/65536.0 - 0.5) * amp
	}
	t := float64(idx) / float64(rate)
	env := 0.55 + 0.35*math.Sin(2*math.Pi*3*t)
	if ch == 0 {
		s := 0.32*math.Sin(2*math.Pi*180*t) +
			0.12*math.Sin(2*math.Pi*360*t+0.4) +
			0.06*math.Sin(2*math.Pi*720*t+0.9) +
			0.025*math.Sin(2*math.Pi*1100*t+1.7)
		return env * s
	}
	r := 0.30*math.Sin(2*math.Pi*185*t+0.2) +
		0.10*math.Sin(2*math.Pi*370*t+0.7) +
		0.05*math.Sin(2*math.Pi*740*t+1.1)
	return env * r
}

func refSpeechGapsFrame(rate, channels, start, n int) []float64 {
	pcm := make([]float64, n*channels)
	for i := 0; i < n; i++ {
		for c := 0; c < channels; c++ {
			v := refSpeechGapsSample(rate, start+i, c)
			pcm[i*channels+c] = math.Floor(float64(float32(v))*32768+0.5) / 32768
		}
	}
	return pcm
}

// TestDTXOracle compares the libopus policy with DTX enabled against the
// instrumented libopus encoder on a signal with speech, digital silence and
// low-level noise: the Opus-layer DTX (decide_dtx_mode, TOC-only packets
// after 200 ms without activity, a refresh every 400 ms), SILK's own DTX
// (useDTX / inDTX, zero-byte SILK packets) when the tonality analysis is
// off, the peak-energy activity test when it is on, and multi-frame packets
// made of DTX frames.
func TestDTXOracle(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skipf("encoder oracle not built (%s): run pwsh scripts/oracle/build_encoder.ps1", encOraclePath())
	}
	type dtxCase struct {
		rate, channels, bitrate, complexity, frameMs int
		signal, app                                  string
		vbr                                          bool
	}
	var cases []dtxCase
	for _, rate := range []int{16000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, bitrate := range []int{12000, 24000, 64000} {
				for _, complexity := range []int{5, 9} {
					cases = append(cases, dtxCase{rate, channels, bitrate, complexity, 20, "voice", "voip", true})
				}
			}
			cases = append(cases, dtxCase{rate, channels, 64000, 5, 20, "music", "audio", true})
			cases = append(cases, dtxCase{rate, channels, 24000, 5, 20, "voice", "voip", false})
			for _, frameMs := range []int{40, 60} {
				cases = append(cases, dtxCase{rate, channels, 24000, 5, frameMs, "voice", "voip", true})
				cases = append(cases, dtxCase{rate, channels, 64000, 9, frameMs, "music", "audio", true})
			}
		}
	}
	for _, tc := range cases {
		name := fmt.Sprintf("in%dk/ch%d/%dk/c%d/%dms/%s-%s/vbr=%v", tc.rate/1000, tc.channels, tc.bitrate/1000, tc.complexity, tc.frameMs, tc.app, tc.signal, tc.vbr)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			frames := 2400 / tc.frameMs
			frameSize := tc.rate * tc.frameMs / 1000
			vbrArg := "0"
			if tc.vbr {
				vbrArg = "1"
			}
			ref := runCELTOracleCmd(t, "ref-speech-gaps", "--auto-enc", strconv.Itoa(tc.rate), "ref-speech-gaps", strconv.Itoa(frames),
				strconv.Itoa(tc.bitrate), vbrArg, strconv.Itoa(tc.channels), strconv.Itoa(tc.complexity), tc.signal, tc.app,
				strconv.Itoa(tc.frameMs), "0", "1")
			app := ApplicationVOIP
			if tc.app == "audio" {
				app = ApplicationAudio
			}
			enc, err := NewEncoder(tc.rate, tc.channels, app)
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
			enc.SetDTX(true)
			identical, dtxC, dtxGo := 0, 0, 0
			firstDiff := -1
			for f := 0; f < frames; f++ {
				pcm := refSpeechGapsFrame(tc.rate, tc.channels, f*frameSize, frameSize)
				pkt, err := enc.EncodeFloat(pcm, frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", f, err)
				}
				r := ref[f]
				if !r.havePacket {
					t.Fatalf("frame %d: incomplete oracle trace", f)
				}
				if len(r.packet) <= 2 {
					dtxC++
				}
				if len(pkt) <= 2 {
					dtxGo++
				}
				if bytes.Equal(pkt, r.packet) {
					identical++
				} else if firstDiff < 0 {
					firstDiff = f
					t.Logf("frame %d: Go %d B TOC %#x / C %d B TOC %#x", f, len(pkt), pkt[0], len(r.packet), r.packet[0])
				}
			}
			t.Logf("%s: packets %d/%d byte-identical (first difference at frame %d), DTX packets Go %d / C %d", name, identical, frames, firstDiff, dtxGo, dtxC)
			if identical != frames {
				t.Errorf("%d/%d packets byte-identical, want all", identical, frames)
			}
		})
	}
}
