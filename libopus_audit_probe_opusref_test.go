//go:build opusref

package opus

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envI(name string, def int) int {
	v, err := strconv.Atoi(envOr(name, strconv.Itoa(def)))
	if err != nil {
		return def
	}
	return v
}

// TestAuditProbe: one configurable cell. AP_SRC (index into
// auditLoadSources), AP_RATE, AP_CH, AP_FRAMEUS, AP_FRAMES, AP_APP and
// AP_CTLS ("bitrate=20000,complexity=10,...") applied before frame 0.
// AP_CPX is a comma list of complexities to sweep.
func TestAuditProbe(t *testing.T) {
	auditSkip(t)
	srcs := auditLoadSources(t)
	src := srcs[envI("AP_SRC", 0)]
	rate, ch := envI("AP_RATE", 48000), envI("AP_CH", 1)
	us := envI("AP_FRAMEUS", 20000)
	fs := rate * us / 1000000
	frames := envI("AP_FRAMES", 300)
	for _, cpx := range strings.Split(envOr("AP_CPX", "10"), ",") {
		cv, _ := strconv.Atoi(cpx)
		c := aCfg{name: fmt.Sprintf("probe/%s/c%d", src.name, cv), rate: rate, ch: ch, app: envOr("AP_APP", "voip"), profile: true}
		c.pcm = auditInput(src, rate, ch, frames, fs)
		var steps []aStep
		for _, kv := range strings.Split(envOr("AP_CTLS", ""), ",") {
			if kv == "" {
				continue
			}
			p := strings.SplitN(kv, "=", 2)
			v, _ := strconv.Atoi(p[1])
			steps = append(steps, cs(p[0], v))
		}
		steps = append(steps, cs("complexity", cv))
		tf := envI("AP_TRACE", -1)
		at := map[int][]aStep{}
		if tf >= 0 {
			at[tf] = []aStep{{op: 't', val: 1}}
			at[tf+1] = []aStep{{op: 't', val: 0}}
			c.onPacket = func(p int, g auditGoEncoder, gp, cp []byte) {
				if p != tf || g.enc == nil {
					return
				}
				e := g.enc
				t.Logf("GO pkt %d: %d B TOC %#x | C %d B", p, len(gp), gp[0], len(cp))
				if _, gf, _, err1 := PacketParse(gp); err1 == nil {
					if _, cf, _, err2 := PacketParse(cp); err2 == nil {
						for k := 0; k < max(len(gf), len(cf)); k++ {
							gl, cl, same := -1, -1, false
							if k < len(gf) {
								gl = len(gf[k])
							}
							if k < len(cf) {
								cl = len(cf[k])
							}
							if k < len(gf) && k < len(cf) {
								same = string(gf[k]) == string(cf[k])
							}
							t.Logf("frame %d: Go %d B C %d B same=%v", k, gl, cl, same)
						}
					}
				}
				t.Logf("GO pkt hex %x", gp)
				t.Logf("C  pkt hex %x", cp)
				if e.silkEncoder != nil {
					st := e.silkEncoder.LastFrameTrace()
					t.Logf("GO SILK sig=%d qoff=%d gainsIdx=%v pitchL=%v ltpScale=%d seed=%d nlsf=%v interp=%d nbits=%d target=%d tell=%d loop=%d lambdaQ10=%d",
						st.SignalType, st.QuantOffset, st.GainsIndices, st.PitchL, st.LTPScaleQ14, st.Seed, st.NLSFQ15, st.InterpFactor, st.NBits, st.TargetRateBps, st.Tell, len(st.Loop), st.LambdaQ10)
					for _, it := range st.Loop {
						t.Logf("GO SILK loop %+v", it)
					}
				}
				ct := e.celtEncoder.LastFrameTrace()
				t.Logf("GO CELT tr=%v sb=%d tfEst=%.9g pf=%v/%d/%.6g tellCoarse=%d tellTF=%d tellTrim=%d spread=%d trim=%d boost=%d bits=%d coded=%d int=%d dual=%v bal=%d tellFine=%d tellFinal=%d vbrTarget=%d base=%d equiv=%d tf=%v offsets=%v",
					ct.IsTransient, ct.ShortBlocks, ct.TFEstimate, ct.PFOn, ct.PitchIndex, ct.PFGain, ct.TellCoarse, ct.TellTF, ct.TellFracTrim, ct.Spread, ct.AllocTrim, ct.TotalBoost, ct.Bits, ct.CodedBands, ct.Intensity, ct.DualStereo, ct.Balance, ct.TellFine, ct.TellFinal, ct.VBRTarget, ct.VBRBaseTarget, ct.EquivRate, ct.TFRes, ct.Offsets)
				t.Logf("GO CELT analysis %+v", ct.Analysis)
				if p := os.Getenv("AUDIT_STDERR"); p != "" {
					data, _ := os.ReadFile(p)
					cmp := func(tag string, goVals []float64) {
						for _, line := range strings.Split(string(data), "\n") {
							if !strings.HasPrefix(line, "["+tag+"]") {
								continue
							}
							ms := encOracleValuesRe.FindAllStringSubmatch(line, -1)
							eq, first, maxd := 0, -1, 0.0
							for i, m := range ms {
								v, _ := strconv.ParseFloat(m[1], 64)
								if i >= len(goVals) {
									break
								}
								if float32(v) == float32(goVals[i]) {
									eq++
								} else {
									if first < 0 {
										first = i
									}
									if d := v - goVals[i]; d*d > maxd*maxd {
										maxd = d
									}
								}
							}
							t.Logf("CMP %s: C n=%d Go n=%d eq=%d firstDiff=%d maxAbs=%.4g", tag, len(ms), len(goVals), eq, first, maxd)
							if first >= 0 {
								v, _ := strconv.ParseFloat(ms[first][1], 64)
								t.Logf("CMP %s[%d]: C %.9g Go %.9g", tag, first, v, goVals[first])
							}
						}
					}
					var in []float64
					for _, ch := range ct.In {
						in = append(in, ch...)
					}
					cmp("SILK_CELT_ENC_IN", in)
					cmp("SILK_CELT_ENC_BANDE", ct.BandE)
					cmp("SILK_CELT_ENC_BANDLOGE", ct.BandLogE)
					var fr []float64
					for _, ch := range ct.Freq {
						fr = append(fr, ch...)
					}
					cmp("SILK_CELT_ENC_FREQ", fr)
					cmp("SILK_CELT_ENC_OLDBANDE", ct.OldBandE)
					cmp("SILK_CELT_ENC_ERROR", ct.CoarseError)
					t.Logf("GO bandTellFrac=%v pulses=%v fineQuant=%v", ct.BandTellFrac, ct.Pulses, ct.FineQuant)
					t.Logf("GO intra=%v tfChan=%d tfSelect=%d tfEst=%v", ct.Intra, ct.TFChan, ct.TFSelect, ct.TFEstimate)
				}
			}
		}
		if sch := os.Getenv("AP_SCHED"); sch != "" {
			parts := strings.Split(sch, ",")
			for f := 0; f < frames; f++ {
				v, _ := strconv.Atoi(parts[min(f, len(parts)-1)])
				at[f] = append([]aStep{cs("bitrate", v)}, at[f]...)
			}
		}
		c.steps = append(steps, framesSteps(frames, fs, 'e', at)...)
		auditReport(t, c)
	}
}
