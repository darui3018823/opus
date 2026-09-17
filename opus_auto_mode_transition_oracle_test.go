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
// Every cell is gated on byte identity; OPUS_TRANSITION_VBR_TRACE=1 logs
// the CELT VBR state of every frame.
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
	if sched := os.Getenv("OPUS_TRANSITION_SCHEDULE"); sched != "" {
		// Probe: one comma-separated schedule for every app/signal/channel
		// combination, reporting only.
		var schedule []int
		for _, part := range strings.Split(sched, ",") {
			b, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				t.Fatalf("OPUS_TRANSITION_SCHEDULE: %v", err)
			}
			schedule = append(schedule, b)
		}
		for _, app := range []string{"voip", "audio"} {
			for _, signal := range []string{"voice", "music", "auto"} {
				for _, channels := range []int{1, 2} {
					cases = append(cases, transCase{name: fmt.Sprintf("%s/%s/ch%d/probe", app, signal, channels), schedule: schedule, channels: channels, signal: signal, app: app})
				}
			}
		}
	}
	for _, app := range []string{"voip", "audio"} {
		if len(cases) > 0 {
			break
		}
		for _, signal := range []string{"voice", "music", "auto"} {
			for _, channels := range []int{1, 2} {
				for _, sw := range [][2]int{{12000, 64000}, {64000, 12000}, {24000, 128000}, {128000, 24000}, {12000, 128000}, {128000, 12000}, {32000, 48000}, {48000, 32000}} {
					cases = append(cases, transCase{
						name:     fmt.Sprintf("%s/%s/ch%d/%dk-%dk", app, signal, channels, sw[0]/1000, sw[1]/1000),
						schedule: sched(sw[0], sw[1]),
						channels: channels,
						signal:   signal,
						app:      app,
						exact:    true,
					})
				}
				cases = append(cases, transCase{
					name:     fmt.Sprintf("%s/%s/ch%d/12k-128k-12k", app, signal, channels),
					schedule: sched3(12000, 128000, 12000),
					channels: channels,
					signal:   signal,
					app:      app,
					exact:    true,
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
				if os.Getenv("OPUS_TRANSITION_VBR_TRACE") != "" {
					tr := enc.celtEncoder.LastFrameTrace()
					t.Logf("frame %d: celt bitrate %d encoder bitrate %d rateMode %v", f, enc.celtEncoder.Bitrate(), enc.bitrate, enc.celtEncoder.GetRateMode())
					t.Logf("frame %d: vbr Go{target %d base %d lastCoded %d int %d stereoSaving %.9g totBoost %d tfEst %.9g pitchChange %v maxDepth %.9g temporalVBR %.9g equiv %d | after: reservoir %d drift %d offset %d bytes %d}",
						f, tr.VBRTarget, tr.VBRBaseTarget, tr.LastCodedBandsIn, tr.Intensity, tr.StereoSaving, tr.TotBoost, tr.TFEstimate, tr.PitchChange, tr.MaxDepth, tr.TemporalVBR, tr.EquivRate, tr.VBRReservoir, tr.VBRDrift, tr.VBROffset, tr.PacketBytes)
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
					lo := prefix - 4
					if lo < 0 {
						lo = 0
					}
					hiG, hiC := lo+28, lo+28
					if hiG > len(pkt) {
						hiG = len(pkt)
					}
					if hiC > len(r.packet) {
						hiC = len(r.packet)
					}
					t.Logf("frame %d: bytes[%d:] Go % x / C % x", f, lo, pkt[lo:hiG], r.packet[lo:hiC])
					tr := enc.celtEncoder.LastFrameTrace()
					for _, sub := range enc.celtEncoders[:2] {
						if sub == nil {
							continue
						}
						st := sub.LastFrameTrace()
						if st.PacketBytes == 0 && len(st.In) == 0 {
							continue
						}
						t.Logf("frame %d: celt %d-sample Go{transient %v tfEst %.6g pf %v/%d intra %v tellCoarse %d tellFrac %d spread %d trim %d bits %d coded %d tellFinal %d bytes %d}",
							f, sub.FrameSize(), st.IsTransient, st.TFEstimate, st.PFOn, st.PitchIndex, st.Intra, st.TellCoarse, st.TellFracTrim, st.Spread, st.AllocTrim, st.Bits, st.CodedBands, st.TellFinal, st.PacketBytes)
						if sub.FrameSize() == 120 {
							t.Logf("frame %d: celt 120-sample Go bandE %v | bandLogE %v | temporalVBR %.6g", f, st.BandE, st.BandLogE, st.TemporalVBR)
						}
						if len(r.in) > 0 && len(st.In) > 0 && len(st.In[0]) == len(r.in)/tc.channels {
							var inAll []float64
							for c := range st.In {
								inAll = append(inAll, st.In[c]...)
							}
							inAbs, _, inEq, inN := float32Stats(inAll, r.in)
							beAbs, _, beEq, beN := float32Stats(st.BandE, r.bandE)
							t.Logf("frame %d: celt %d-sample in eq %d/%d maxAbs %.3g | bandE eq %d/%d maxAbs %.3g", f, sub.FrameSize(), inEq, inN, inAbs, beEq, beN, beAbs)
							ceAbs, _, ceEq, ceN := float32Stats(st.CoarseError, r.errorE)
							obAbs, _, obEq, obN := float32Stats(st.OldBandE, r.oldBandE)
							t.Logf("frame %d: celt %d-sample coarse error eq %d/%d maxAbs %.3g | oldBandE eq %d/%d maxAbs %.3g", f, sub.FrameSize(), ceEq, ceN, ceAbs, obEq, obN, obAbs)
							for i := 0; i < len(st.CoarseError) && i < len(r.errorE); i++ {
								if float32(st.CoarseError[i]) != r.errorE[i] {
									t.Logf("frame %d: error[%d] Go %.9g C %.9g | oldBandE Go %.9g C %.9g", f, i, float32(st.CoarseError[i]), r.errorE[i], float32(st.OldBandE[i]), r.oldBandE[i])
								}
							}
							t.Logf("frame %d: celt %d-sample band tell_frac Go %v C %v | pulses Go %v C %v | fine Go %v C %v | Go{int %d dual %v bal %d rsv %d final rng %#x} C{int %d dual %v bal %d rsv %d}",
								f, sub.FrameSize(), st.BandTellFrac, r.bandTellFrac, st.Pulses, r.pulses, st.FineQuant, r.fineQuant, st.Intensity, st.DualStereo, st.Balance, st.AntiCollapseRsv, sub.FinalRange(), r.intensity, r.dualStereo, r.balance, r.antiCollapseRsv)
						}
					}
					t.Logf("frame %d: celt Go{transient %v tfEst %.6g pf %v/%d intra %v tellCoarse %d tellFrac %d spread %d trim %d bits %d coded %d tellFinal %d bytes %d} C{transient %v tfEst %.6g pf %v/%d tell %d tellCoarse %d tellFrac %d spread %d trim %d bits %d coded %d tellFinal %d}",
						f, tr.IsTransient, tr.TFEstimate, tr.PFOn, tr.PitchIndex, tr.Intra, tr.TellCoarse, tr.TellFracTrim, tr.Spread, tr.AllocTrim, tr.Bits, tr.CodedBands, tr.TellFinal, tr.PacketBytes,
						r.isTransient, r.tfEstimate, r.pfOn, r.pitchIndex, r.tell, r.tellCoarse, r.tellFracTrim, r.spread, r.allocTrim, r.bits, r.codedBands, r.tellFinal)
					t.Logf("frame %d: celt Go{int %d dual %v reservoir %d drift %d offset %d temporalVBR %.6g pitchChange %v lastCoded %d} C{int %d dual %v}", f, tr.Intensity, tr.DualStereo, tr.VBRReservoir, tr.VBRDrift, tr.VBROffset, tr.TemporalVBR, tr.PitchChange, tr.CodedBands, r.intensity, r.dualStereo)
					if len(r.in) > 0 && len(tr.In) > 0 {
						var inAll []float64
						for c := range tr.In {
							inAll = append(inAll, tr.In[c]...)
						}
						inAbs, _, inEq, inN := float32Stats(inAll, r.in)
						beAbs, _, beEq, beN := float32Stats(tr.BandE, r.bandE)
						obAbs, _, obEq, obN := float32Stats(tr.OldBandE, r.oldBandE)
						t.Logf("frame %d: celt in eq %d/%d maxAbs %.3g | bandE eq %d/%d maxAbs %.3g | oldBandE eq %d/%d maxAbs %.3g", f, inEq, inN, inAbs, beEq, beN, beAbs, obEq, obN, obAbs)
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
