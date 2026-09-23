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
	rate := 48000
	if v := os.Getenv("OPUS_TRANSITION_RATE"); v != "" {
		// Probe: another input rate (8/12/16/24 kHz).
		rate, _ = strconv.Atoi(v)
	}
	frameMs := 20
	if v := os.Getenv("OPUS_TRANSITION_FRAME_MS"); v != "" {
		// Probe: 40 or 60 ms packets.
		frameMs, _ = strconv.Atoi(v)
	}
	frameSize := rate * frameMs / 1000
	type transCase struct {
		name     string
		schedule []int
		channels int
		signal   string
		app      string
		exact    bool
		frames   int // 0 = 16
		frameMs  int // 0 = 20
		rate     int // 0 = the sweep rate (48 kHz unless probed)
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
				// SILK internal rate transitions (NB <-> WB): the variable
				// low-pass, switchReady with its trailing redundancy and the
				// re-init + prefill of the switch; 8k->24k switches up within
				// 16 frames, 24k->8k needs the 128-frame down transition.
				cases = append(cases, transCase{
					name:     fmt.Sprintf("%s/%s/ch%d/8k-24k", app, signal, channels),
					schedule: sched(8000, 24000),
					channels: channels,
					signal:   signal,
					app:      app,
					exact:    true,
				})
				if signal == "voice" {
					cases = append(cases, transCase{
						name:     fmt.Sprintf("%s/%s/ch%d/24k-8k-long", app, signal, channels),
						schedule: sched(24000, 8000),
						channels: channels,
						signal:   signal,
						app:      app,
						exact:    true,
						frames:   160,
					})
				}
				// 40 / 60 ms packets: the repacketized 20 ms frames of hybrid /
				// CELT-only (to_celt on the last frame, redundancy on the first,
				// the prefill on every frame) and native SILK multi-frame packets.
				for _, mf := range []struct{ ms, a, b int }{{40, 12000, 128000}, {40, 128000, 12000}, {40, 8000, 24000}, {60, 128000, 12000}, {60, 8000, 24000}} {
					cases = append(cases, transCase{
						name:     fmt.Sprintf("%s/%s/ch%d/%dk-%dk-%dms", app, signal, channels, mf.a/1000, mf.b/1000, mf.ms),
						schedule: sched(mf.a, mf.b),
						channels: channels,
						signal:   signal,
						app:      app,
						exact:    true,
						frameMs:  mf.ms,
					})
				}
				// Input rates below 48 kHz: the CELT layer runs the 48 kHz
				// mode on the zero-stuffed input, so a mode transition also
				// prefills and codes its redundant frames from it.
				if signal != "auto" {
					for _, inputRate := range []int{8000, 12000, 16000, 24000} {
						for _, tr := range []struct{ a, b int }{{12000, 128000}, {128000, 12000}, {8000, 24000}} {
							cases = append(cases, transCase{
								name:     fmt.Sprintf("%s/%s/ch%d/%dk-%dk/in%dk", app, signal, channels, tr.a/1000, tr.b/1000, inputRate/1000),
								schedule: sched(tr.a, tr.b),
								channels: channels,
								signal:   signal,
								app:      app,
								exact:    true,
								rate:     inputRate,
							})
						}
					}
				}
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames := tc.frames
			if frames == 0 {
				frames = 16
			}
			rate, frameMs, frameSize := rate, frameMs, frameSize
			if tc.rate != 0 {
				rate = tc.rate
			}
			if tc.frameMs != 0 {
				frameMs = tc.frameMs
			}
			frameSize = rate * frameMs / 1000
			parts := make([]string, len(tc.schedule))
			for i, b := range tc.schedule {
				parts[i] = strconv.Itoa(b)
			}
			ref, oracleStderr := runCELTOracleCmdWithStderr(t, "ref-speech", "--auto-enc", strconv.Itoa(rate), "ref-speech", strconv.Itoa(frames), strings.Join(parts, ","), "1", strconv.Itoa(tc.channels), "5", tc.signal, tc.app, strconv.Itoa(frameMs))
			silkRef := parseEncOracleFrames(t, oracleStderr, frames)
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
				if os.Getenv("OPUS_TRANSITION_SILK_STAGES") != "" && enc.silkEncoder != nil && f < len(silkRef) && silkRef[f].stages.haveNSQ {
					g := enc.silkEncoder.LastFrameTrace()
					slg, _, _ := enc.silkEncoder.LTPInputsTrace()
					t.Logf("frame %d: silk signalType %d sum_log_gain_in %d", f, g.SignalType, slg)
					if d := stageDiffs(g, silkRef[f].stages); len(d) > 0 {
						t.Logf("frame %d: silk stage diffs %v", f, d)
					}
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
					if enc.silkEncoder != nil {
						t.Logf("frame %d: silk rate %d lp %+v allowSwitch %v bwSwitch %v decidedBW %d", f, enc.silkSampleRate, enc.silkEncoder.LPState(), enc.silkEncoder.AllowBandwidthSwitch(), enc.silkBwSwitch, enc.libopusBandwidth)
						if f < len(silkRef) && silkRef[f].stages.haveNSQ {
							g := enc.silkEncoder.LastFrameTrace()
							t.Logf("frame %d: silk signalType Go %d C %d pitchL Go %v C %v", f, g.SignalType, silkRef[f].stages.signalType, g.PitchL, silkRef[f].stages.pitchL)
							t.Logf("frame %d: silk LTPCoef_Q14 Go %v C %v | LTPScale Go %d C %d | invGains Go %v C %v", f, g.LTPCoefQ14, silkRef[f].stages.ltpCoefQ14, g.LTPScaleQ14, silkRef[f].stages.ltpScaleQ14, g.InvGains, silkRef[f].stages.invGains)
							for k, pt := range enc.silkEncoder.PacketFrameTraces() {
								t.Logf("frame %d: silk sub-frame %d Go{signalType %d invGains %v gains %v nBits %d target %d loop %+v}", f, k, pt.SignalType, pt.InvGains, pt.GainsQ16, pt.NBits, pt.TargetRateBps, pt.Loop)
							}
							slg, xx, xX := enc.silkEncoder.LTPInputsTrace()
							if len(xx) > 6 {
								xx = xx[:6]
							}
							t.Logf("frame %d: silk LTP inputs Go{sum_log_gain %d xX %v XX[0:6] %v}", f, slg, xX, xx)
							for _, d := range stageDiffs(g, silkRef[f].stages) {
								t.Logf("frame %d: silk %s", f, d)
							}
							if d := loopDiffs(g, silkRef[f]); d != "" {
								t.Logf("frame %d: silk loop %s", f, d)
							}
						}
					}
					if dump := os.Getenv("OPUS_TRANSITION_SILK_DUMP"); dump != "" && enc.silkEncoder != nil {
						var sb strings.Builder
						for _, v := range enc.silkEncoder.InputBufferFLP() {
							fmt.Fprintln(&sb, v)
						}
						_ = os.WriteFile(dump, []byte(sb.String()), 0o644)
						{
							var sb3 strings.Builder
							for _, v := range enc.silkEncoder.PitchResidualTrace() {
								fmt.Fprintln(&sb3, v)
							}
							_ = os.WriteFile(dump+".res", []byte(sb3.String()), 0o644)
						}
						if side := enc.silkEncoder.SideEncoder(); side != nil {
							var sb2 strings.Builder
							for _, v := range side.InputBufferFLP() {
								fmt.Fprintln(&sb2, v)
							}
							_ = os.WriteFile(dump+".side", []byte(sb2.String()), 0o644)
						}
						t.Logf("frame %d: SILK x_buf written to %s (speech_activity_Q8 %d) stereo %+v prefill %+v", f, dump, enc.silkEncoder.LastSpeechActivityQ8(), enc.silkEncoder.LastStereoTrace(), enc.silkEncoder.LastPrefillStereoTrace())
						st := enc.silkEncoder.LastFrameTrace()
						t.Logf("frame %d: SILK rate Go{nBits %d target %d exceeded %d usedLBRR %d tell %d loop %+v}", f, st.NBits, st.TargetRateBps, st.NBitsExceeded, st.NBitsUsedLBRR, st.Tell, st.Loop)
					}
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
