//go:build opusref

package opus

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"testing"
)

// TestHybridEncoderOracle compares the Go hybrid (SILK + CELT) encoder with
// the instrumented libopus encoder: VOIP with a voice hint, 48 kHz input,
// forced hybrid mode in libopus and a bitrate range where the Go encoder
// chooses hybrid, fullband, CBR and CVBR, mono and stereo. Cells marked exact
// are gated; the others report the first divergent frame and the CELT-side
// stage differences.
func TestHybridEncoderOracle(t *testing.T) {
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skipf("encoder oracle not built (%s): run pwsh scripts/oracle/build_encoder.ps1", encOraclePath())
	}
	const (
		rate      = 48000
		frameSize = rate / 50
		frames    = 20
	)
	type hybridCase struct {
		name       string
		bitrate    int
		vbr        bool
		channels   int
		complexity int
		exact      bool
	}
	var cases []hybridCase
	for _, channels := range []int{1, 2} {
		bitrates := []int{48000, 64000, 96000}
		if channels == 2 {
			bitrates = []int{64000, 96000, 160000}
		}
		for _, bitrate := range bitrates {
			for _, vbr := range []bool{false, true} {
				for _, complexity := range []int{0, 5, 10} {
					mode := "cbr"
					if vbr {
						mode = "vbr"
					}
					cases = append(cases, hybridCase{
						name:       fmt.Sprintf("ch%d/%s/%dk/c%d", channels, mode, bitrate/1000, complexity),
						bitrate:    bitrate,
						vbr:        vbr,
						channels:   channels,
						complexity: complexity,
						exact:      true,
					})
				}
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := runHybridEncOracle(t, "ref-speech", frames, tc.bitrate, "fb", tc.vbr, tc.channels, tc.complexity)
			enc, err := NewEncoder(rate, tc.channels, ApplicationVOIP)
			if err != nil {
				t.Fatal(err)
			}
			enc.SetSignalType(SignalVoice)
			if err := enc.SetBitrate(tc.bitrate); err != nil {
				t.Fatal(err)
			}
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(true)
			if err := enc.SetComplexity(tc.complexity); err != nil {
				t.Fatal(err)
			}
			if err := enc.SetBandwidth(BandwidthFullband); err != nil {
				t.Fatal(err)
			}
			identical := 0
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
				same := bytes.Equal(pkt, r.packet)
				if same {
					identical++
				} else if firstDiff < 0 {
					firstDiff = f
				}
				if !same || testing.Verbose() {
					prefix := 0
					for prefix < len(pkt) && prefix < len(r.packet) && pkt[prefix] == r.packet[prefix] {
						prefix++
					}
					tr := enc.celtEncoder.LastFrameTrace()
					t.Logf("frame %d: identical=%v prefix=%d Go %d B (TOC %#x) / C %d B (TOC %#x)", f, same, prefix, len(pkt), pkt[0], len(r.packet), r.packet[0])
					if len(r.in) > 0 && len(tr.In) > 0 {
						var inAll, fqAll []float64
						for c := range tr.In {
							inAll = append(inAll, tr.In[c]...)
							fqAll = append(fqAll, tr.Freq[c]...)
						}
						inAbs, _, inEq, inN := float32Stats(inAll, r.in)
						fqAbs, _, fqEq, fqN := float32Stats(fqAll, r.freq)
						beAbs, _, beEq, beN := float32Stats(tr.BandE, r.bandE)
						t.Logf("frame %d: celt in: eq %d/%d maxAbs %.3g | freq: eq %d/%d maxAbs %.3g | bandE: eq %d/%d maxAbs %.3g | Go{transient %v tfEst %.6g tellCoarse %d tellTF %d} C{transient %v tfEst %.6g tell %d tellCoarse+tf %d}",
							f, inEq, inN, inAbs, fqEq, fqN, fqAbs, beEq, beN, beAbs, tr.IsTransient, tr.TFEstimate, tr.TellCoarse, tr.TellTF, r.isTransient, r.tfEstimate, r.tell, r.tellCoarse)
						t.Logf("frame %d: trim Go{tellFrac %d spread %d trim %d boost %d} C{tellFrac %d spread %d trim %d boost %d} | alloc Go{bits %d coded %d tellFine %d tellFinal %d bytes %d} C{bits %d coded %d tellFine %d tellFinal %d}",
							f, tr.TellFracTrim, tr.Spread, tr.AllocTrim, tr.TotalBoost, r.tellFracTrim, r.spread, r.allocTrim, r.totalBoost,
							tr.Bits, tr.CodedBands, tr.TellFine, tr.TellFinal, tr.PacketBytes, r.bits, r.codedBands, r.tellFine, r.tellFinal)
					}
				}
			}
			t.Logf("%s: %d/%d packets byte-identical (first difference at frame %d)", tc.name, identical, frames, firstDiff)
			if tc.exact && identical != frames {
				t.Errorf("%s: %d/%d packets byte-identical, want all", tc.name, identical, frames)
			}
		})
	}
}
