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

// TestAutoModeOracle compares the Go encoder with the libopus mode policy
// (SetLibopusModePolicy) against the instrumented libopus encoder in its
// automatic mode: no forced mode or bandwidth, VOIP/AUDIO, voice/music
// hints, 48 kHz mono and stereo over a bitrate sweep. It checks the TOC
// (mode, bandwidth, channels) of every packet and the packet bytes.
func TestAutoModeOracle(t *testing.T) {
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skipf("encoder oracle not built (%s): run pwsh scripts/oracle/build_encoder.ps1", encOraclePath())
	}
	const (
		rate   = 48000
		frames = 12
	)
	type autoCase struct {
		name       string
		bitrate    int
		vbr        bool
		channels   int
		complexity int
		signal     string
		app        string
		exact      bool
		frameMs    int // 0 = 20
	}
	var cases []autoCase
	for _, app := range []string{"voip", "audio"} {
		for _, signal := range []string{"voice", "music", "auto"} {
			for _, channels := range []int{1, 2} {
				for _, bitrate := range []int{12000, 24000, 48000, 80000, 128000} {
					cases = append(cases, autoCase{
						name:       fmt.Sprintf("%s/%s/ch%d/%dk", app, signal, channels, bitrate/1000),
						bitrate:    bitrate,
						vbr:        true,
						channels:   channels,
						complexity: 5,
						signal:     signal,
						app:        app,
						exact:      true,
					})
				}
				// 40 / 60 ms packets: native SILK multi-frame packets and
				// the repacketized 20 ms frames of hybrid / CELT-only.
				for _, frameMs := range []int{40, 60} {
					for _, bitrate := range []int{12000, 24000, 64000} {
						if signal == "auto" {
							continue
						}
						cases = append(cases, autoCase{
							name:       fmt.Sprintf("%s/%s/ch%d/%dk/%dms", app, signal, channels, bitrate/1000, frameMs),
							bitrate:    bitrate,
							vbr:        true,
							channels:   channels,
							complexity: 5,
							signal:     signal,
							app:        app,
							exact:      true,
							frameMs:    frameMs,
						})
					}
				}
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frameMs := tc.frameMs
			if frameMs == 0 {
				frameMs = 20
			}
			frameSize := rate * frameMs / 1000
			vbrArg := "0"
			if tc.vbr {
				vbrArg = "1"
			}
			ref := runCELTOracleCmd(t, "ref-speech", "--auto-enc", "48000", "ref-speech", strconv.Itoa(frames), strconv.Itoa(tc.bitrate), vbrArg, strconv.Itoa(tc.channels), strconv.Itoa(tc.complexity), tc.signal, tc.app, strconv.Itoa(frameMs))
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
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(true)
			if err := enc.SetComplexity(tc.complexity); err != nil {
				t.Fatal(err)
			}
			identical := 0
			tocMatch := 0
			firstDiff := -1
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
					t.Fatalf("frame %d: %v", f, err)
				}
				r := ref[f]
				if !r.havePacket {
					t.Fatalf("frame %d: incomplete oracle trace", f)
				}
				if pkt[0] == r.packet[0] {
					tocMatch++
				}
				same := bytes.Equal(pkt, r.packet)
				if same {
					identical++
				} else if firstDiff < 0 {
					firstDiff = f
					prefix := 0
					for prefix < len(pkt) && prefix < len(r.packet) && pkt[prefix] == r.packet[prefix] {
						prefix++
					}
					t.Logf("frame %d: Go %d B TOC %#x / C %d B TOC %#x, common prefix %d", f, len(pkt), pkt[0], len(r.packet), r.packet[0], prefix)
					tr := enc.celtEncoder.LastFrameTrace()
					if len(r.in) > 0 && len(tr.In) > 0 {
						var inAll []float64
						for c := range tr.In {
							inAll = append(inAll, tr.In[c]...)
						}
						inAbs, _, inEq, inN := float32Stats(inAll, r.in)
						beAbs, _, beEq, beN := float32Stats(tr.BandE, r.bandE)
						t.Logf("frame %d: celt in: eq %d/%d maxAbs %.3g | bandE: eq %d/%d maxAbs %.3g | Go{pf %v/%d/%.4g tellCoarse %d tellTF %d tellFinal %d} C{pf %v/%d/%.4g tell %d tellCoarse+tf %d tellFinal %d}",
							f, inEq, inN, inAbs, beEq, beN, beAbs, tr.PFOn, tr.PitchIndex, tr.PFGain, tr.TellCoarse, tr.TellTF, tr.TellFinal, r.pfOn, r.pitchIndex, r.gain1, r.tell, r.tellCoarse, r.tellFinal)
						shown := 0
						for i := 0; i < len(inAll) && i < len(r.in) && shown < 6; i++ {
							if float32(inAll[i]) != r.in[i] {
								t.Logf("frame %d: in[%d] Go %.9g C %.9g", f, i, float32(inAll[i]), r.in[i])
								shown++
							}
						}
						pbAbs, _, pbEq, pbN := float32Stats(func() []float64 {
							o := make([]float64, len(tr.PitchBuf))
							for i, v := range tr.PitchBuf {
								o[i] = float64(v)
							}
							return o
						}(), r.pitchBuf)
						t.Logf("frame %d: pitch Go{search %d raw %d gain %.9g} C{search %d raw %d gain %.9g} | pitch_buf eq %d/%d maxAbs %.3g",
							f, tr.PitchSearch, tr.PitchRaw, tr.PitchGain, r.pitchSearch, r.pitchRaw, r.pitchGain, pbEq, pbN, pbAbs)
					}
				}
			}
			t.Logf("%s: TOC %d/%d, packets %d/%d byte-identical (first difference at frame %d)", tc.name, tocMatch, frames, identical, frames, firstDiff)
			if tc.exact && identical != frames {
				t.Errorf("%s: %d/%d packets byte-identical, want all", tc.name, identical, frames)
			}
		})
	}
}
