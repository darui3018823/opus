//go:build opusref && opus16

package opus

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

var auditRates = []int{8000, 12000, 16000, 24000, 48000}
var auditApps = []int{ApplicationVOIP, ApplicationAudio, ApplicationRestrictedLowDelay, ApplicationRestrictedSILK, ApplicationRestrictedCELT}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func goSignalToLib(s SignalType) int {
	switch s {
	case SignalVoice:
		return 3001
	case SignalMusic:
		return 3002
	case SignalAuto:
		return -1000
	}
	return int(s) // unmapped
}

type getter struct {
	name string
	req  int
	g    func(e *Encoder) int
}

var encGetters = []getter{
	{"APPLICATION", 4001, func(e *Encoder) int { return int(e.Application()) }},
	{"BITRATE(Bitrate)", 4003, func(e *Encoder) int { return e.Bitrate() }},
	{"BITRATE(EffectiveBitrate)", 4003, func(e *Encoder) int { return e.EffectiveBitrate() }},
	{"MAX_BANDWIDTH", 4005, func(e *Encoder) int { return e.MaxBandwidth() }},
	{"VBR", 4007, func(e *Encoder) int { return b2i(e.VBR()) }},
	{"BANDWIDTH", 4009, func(e *Encoder) int { return e.Bandwidth() }},
	{"COMPLEXITY", 4011, func(e *Encoder) int { return e.Complexity() }},
	{"INBAND_FEC", 4013, func(e *Encoder) int { return b2i(e.InbandFEC()) }},
	{"PACKET_LOSS_PERC", 4015, func(e *Encoder) int { return e.PacketLossPerc() }},
	{"DTX", 4017, func(e *Encoder) int { return b2i(e.DTX()) }},
	{"VBR_CONSTRAINT", 4021, func(e *Encoder) int { return b2i(e.VBRConstraint()) }},
	{"FORCE_CHANNELS", 4023, func(e *Encoder) int { return e.ForceChannels() }},
	{"SIGNAL", 4025, func(e *Encoder) int { return goSignalToLib(e.SignalType()) }},
	{"LOOKAHEAD", 4027, func(e *Encoder) int { return e.Lookahead() }},
	{"SAMPLE_RATE", 4029, func(e *Encoder) int { return e.SampleRate() }},
	{"FINAL_RANGE", 4031, func(e *Encoder) int { return int(int32(e.FinalRange())) }},
	{"LSB_DEPTH", 4037, func(e *Encoder) int { return e.LSBDepth() }},
	{"EXPERT_FRAME_DURATION", 4041, func(e *Encoder) int { return int(e.ExpertFrameDuration()) }},
	{"PREDICTION_DISABLED", 4043, func(e *Encoder) int { return b2i(e.PredictionDisabled()) }},
	{"PHASE_INVERSION_DISABLED", 4047, func(e *Encoder) int { return b2i(e.PhaseInversionDisabled()) }},
	{"IN_DTX", 4049, func(e *Encoder) int { return b2i(e.InDTX()) }},
}

type cmpLog struct {
	counts map[string][2]int // [agree, total]
	first  map[string]string
}

func newCmpLog() *cmpLog { return &cmpLog{counts: map[string][2]int{}, first: map[string]string{}} }
func (c *cmpLog) add(key string, ok bool, detail string) {
	v := c.counts[key]
	v[1]++
	if ok {
		v[0]++
	} else if c.first[key] == "" {
		c.first[key] = detail
	}
	c.counts[key] = v
}
func (c *cmpLog) dump(t *testing.T, title string) {
	keys := make([]string, 0, len(c.counts))
	for k := range c.counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := c.counts[k]
		line := fmt.Sprintf("[%s] %-34s %d/%d agree", title, k, v[0], v[1])
		if v[0] != v[1] {
			line += "  first: " + c.first[k]
		}
		t.Log(line)
	}
}

func sineF32(n, ch, fs int, freq float64, amp float32, phase int) []float32 {
	out := make([]float32, n*ch)
	for i := 0; i < n; i++ {
		v := amp * float32(math.Sin(2*math.Pi*freq*float64(i+phase)/float64(fs)))
		for c := 0; c < ch; c++ {
			out[i*ch+c] = v
		}
	}
	return out
}

func TestAuditEncoderCTL(t *testing.T) {
	auditEnabled(t)
	create := newCmpLog()
	defaults := newCmpLog()
	sets := newCmpLog()
	after := newCmpLog()
	// constructor acceptance
	for _, fs := range []int{0, 7999, 8000, 11025, 12000, 16000, 22050, 24000, 44100, 48000, 96000} {
		for _, ch := range []int{0, 1, 2, 3} {
			for _, app := range append([]int{0, 2050, 2054}, auditApps...) {
				le, lerr := cgoref.NewAudEncoder(fs, ch, app)
				ge, gerr := NewEncoder(fs, ch, Application(app))
				create.add("opus_encoder_create", (le != nil) == (gerr == nil), fmt.Sprintf("fs=%d ch=%d app=%d lib=%d go=%v", fs, ch, app, lerr, gerr))
				if le != nil {
					le.Close()
				}
				_ = ge
			}
		}
	}
	create.dump(t, "create")

	type setCase struct {
		name string
		req  int
		vals []int
		set  func(e *Encoder, v int) error // nil error = accepted
	}
	setCases := []setCase{
		{"SET_BITRATE", 4002, []int{-1000, -1, -2, 0, 1, 499, 500, 501, 6000, 64000, 510000, 512000, 750000, 1500000, 1500001, 1 << 30},
			func(e *Encoder, v int) error { return e.SetBitrate(v) }},
		{"SET_COMPLEXITY", 4010, []int{-1, 0, 5, 10, 11},
			func(e *Encoder, v int) error { return e.SetComplexity(v) }},
		{"SET_MAX_BANDWIDTH", 4004, []int{-1000, 1100, 1101, 1102, 1103, 1104, 1105, 1106},
			func(e *Encoder, v int) error { return e.SetMaxBandwidth(v) }},
		{"SET_BANDWIDTH", 4008, []int{-1000, 1100, 1101, 1102, 1103, 1104, 1105, 1106},
			func(e *Encoder, v int) error { return e.SetBandwidth(v) }},
		{"SET_FORCE_CHANNELS", 4022, []int{-1000, -1, 0, 1, 2, 3},
			func(e *Encoder, v int) error { return e.SetForceChannels(v) }},
		{"SET_LSB_DEPTH", 4036, []int{7, 8, 16, 24, 25},
			func(e *Encoder, v int) error { return e.SetLSBDepth(v) }},
		{"SET_EXPERT_FRAME_DURATION", 4040, []int{4999, 5000, 5001, 5002, 5003, 5004, 5005, 5006, 5007, 5008, 5009, 5010},
			func(e *Encoder, v int) error { return e.SetExpertFrameDuration(ExpertFrameDuration(v)) }},
		{"SET_APPLICATION(before encode)", 4000, []int{0, 2048, 2049, 2051, 2052, 2053},
			func(e *Encoder, v int) error { return e.SetApplication(Application(v)) }},
		{"SET_PACKET_LOSS_PERC", 4014, []int{-1, 0, 50, 100, 101},
			func(e *Encoder, v int) error { e.SetPacketLossPerc(v); return nil }},
		{"SET_SIGNAL", 4024, []int{-1000, 3001, 3002},
			func(e *Encoder, v int) error {
				m := map[int]SignalType{-1000: SignalAuto, 3001: SignalVoice, 3002: SignalMusic}
				e.SetSignalType(m[v])
				return nil
			}},
		{"SET_VBR", 4006, []int{0, 1}, func(e *Encoder, v int) error { e.SetVBR(v == 1); return nil }},
		{"SET_VBR_CONSTRAINT", 4020, []int{0, 1}, func(e *Encoder, v int) error { e.SetVBRConstraint(v == 1); return nil }},
		{"SET_DTX", 4016, []int{0, 1}, func(e *Encoder, v int) error { e.SetDTX(v == 1); return nil }},
		{"SET_INBAND_FEC", 4012, []int{0, 1}, func(e *Encoder, v int) error { e.SetInbandFEC(v == 1); return nil }},
		{"SET_PREDICTION_DISABLED", 4042, []int{0, 1}, func(e *Encoder, v int) error { e.SetPredictionDisabled(v == 1); return nil }},
		{"SET_PHASE_INVERSION_DISABLED", 4046, []int{0, 1}, func(e *Encoder, v int) error { e.SetPhaseInversionDisabled(v == 1); return nil }},
	}
	getReq := map[int]int{4002: 4003, 4010: 4011, 4004: 4005, 4008: 4009, 4022: 4023, 4036: 4037, 4040: 4041, 4000: 4001, 4014: 4015, 4024: 4025, 4006: 4007, 4020: 4021, 4016: 4017, 4012: 4013, 4042: 4043, 4046: 4047}
	gByReq := map[int]getter{}
	for _, g := range encGetters {
		if _, ok := gByReq[g.req]; !ok || g.name == "BITRATE(EffectiveBitrate)" {
			gByReq[g.req] = g
		}
	}
	for _, fs := range auditRates {
		for _, ch := range []int{1, 2} {
			for _, app := range auditApps {
				tag := fmt.Sprintf("fs=%d ch=%d app=%d", fs, ch, app)
				le, _ := cgoref.NewAudEncoder(fs, ch, app)
				ge, gerr := newAuditEnc(fs, ch, app)
				if le == nil || gerr != nil {
					t.Fatalf("create %s: %v", tag, gerr)
				}
				for _, g := range encGetters {
					lv, lr := le.Get(g.req)
					gv := g.g(ge)
					defaults.add("GET_"+g.name, lr == 0 && lv == gv, fmt.Sprintf("%s lib=%d(ret %d) go=%d", tag, lv, lr, gv))
				}
				le.Close()
				// setters, each on a fresh pair
				for _, sc := range setCases {
					for _, v := range sc.vals {
						le, _ := cgoref.NewAudEncoder(fs, ch, app)
						ge, _ := newAuditEnc(fs, ch, app)
						lr := le.Set(sc.req, v)
						gerr := sc.set(ge, v)
						sets.add(sc.name+" accept", (lr == 0) == (gerr == nil), fmt.Sprintf("%s v=%d lib=%d go=%v", tag, v, lr, gerr))
						g := gByReq[getReq[sc.req]]
						lv, _ := le.Get(g.req)
						gv := g.g(ge)
						sets.add(sc.name+" readback", lv == gv, fmt.Sprintf("%s v=%d lib=%d go=%d", tag, v, lv, gv))
						if sc.req == 4002 {
							sets.add(fmt.Sprintf("%s v=%d readback(Eff)", sc.name, v), lv == gv, fmt.Sprintf("%s lib=%d go=%d", tag, lv, gv))
							sets.add(sc.name+" readback(Bitrate())", lv == ge.Bitrate(), fmt.Sprintf("%s v=%d lib=%d go=%d", tag, v, lv, ge.Bitrate()))
						}
						le.Close()
					}
				}
				// VBR constraint sequencing
				{
					le, _ := cgoref.NewAudEncoder(fs, ch, app)
					ge, _ := newAuditEnc(fs, ch, app)
					le.Set(4006, 0)
					ge.SetVBR(false)
					lv, _ := le.Get(4021)
					sets.add("GET_VBR_CONSTRAINT after SET_VBR(0)", lv == b2i(ge.VBRConstraint()), fmt.Sprintf("%s lib=%d go=%v", tag, lv, ge.VBRConstraint()))
					le.Set(4020, 0)
					ge.SetVBRConstraint(false)
					le.Set(4006, 1)
					ge.SetVBR(true)
					lv, _ = le.Get(4021)
					sets.add("VBR(0);CONSTRAINT(0);VBR(1) => CONSTRAINT", lv == b2i(ge.VBRConstraint()), fmt.Sprintf("%s lib=%d go=%v", tag, lv, ge.VBRConstraint()))
					le.Close()
				}
				// state after encoding a few frames at 10/20/60 ms
				for _, ms := range []int{10, 20, 60} {
					n := fs * ms / 1000
					le, _ := cgoref.NewAudEncoder(fs, ch, app)
					ge, _ := newAuditEnc(fs, ch, app)
					var lastG []byte
					same := true
					for f := 0; f < 5; f++ {
						pcm := sineF32(n, ch, fs, 440, 0.3, f*n)
						lp, lr := le.EncodeFloat(pcm, n, 4000)
						gp, gerr := ge.EncodeFloat32(pcm, n)
						after.add(fmt.Sprintf("encode %dms accept", ms), (lr > 0) == (gerr == nil), fmt.Sprintf("%s lib=%d go=%v", tag, lr, gerr))
						lastG = gp
						same = same && bytes.Equal(lp, gp)
					}
					after.add(fmt.Sprintf("packets byte-identical %dms", ms), same, tag)
					if lastG != nil {
						pbw, _ := PacketGetBandwidth(lastG)
						lv, _ := le.Get(4009)
						after.add(fmt.Sprintf("lib GET_BANDWIDTH == Go last packet bw %dms", ms), lv == pbw, fmt.Sprintf("%s lib=%d gopkt=%d goGetter=%d", tag, lv, pbw, ge.Bandwidth()))
					}
					for _, g := range encGetters {
						lv, _ := le.Get(g.req)
						gv := g.g(ge)
						after.add(fmt.Sprintf("GET_%s after %dms", g.name, ms), lv == gv, fmt.Sprintf("%s lib=%d go=%d", tag, lv, gv))
					}
					// application change after first encode
					for _, na := range []int{2048, 2049, 2051} {
						lr := le.Set(4000, na)
						gerr := ge.SetApplication(Application(na))
						after.add("SET_APPLICATION(after encode) accept", (lr == 0) == (gerr == nil), fmt.Sprintf("%s from=%d to=%d lib=%d go=%v", tag, app, na, lr, gerr))
					}
					// reset
					le.Reset()
					_ = ge.Reset()
					for _, g := range encGetters {
						lv, _ := le.Get(g.req)
						gv := g.g(ge)
						after.add(fmt.Sprintf("GET_%s after RESET", g.name), lv == gv, fmt.Sprintf("%s lib=%d go=%d", tag, lv, gv))
					}
					le.Close()
				}
			}
		}
	}
	defaults.dump(t, "default")
	sets.dump(t, "set")
	after.dump(t, "state")
	// unsupported requests on libopus build
	le, _ := cgoref.NewAudEncoder(48000, 2, ApplicationAudio)
	for _, r := range []int{4050, 4056, 4054, 4058, 4052} {
		t.Logf("libopus encoder ctl %d => %d", r, le.Set(r, 0))
	}
	le.Close()
}

func TestAuditEncoderFrameSizes(t *testing.T) {
	auditEnabled(t)
	c := newCmpLog()
	for _, fs := range auditRates {
		for _, ch := range []int{1, 2} {
			for _, app := range []int{ApplicationVOIP, ApplicationAudio, ApplicationRestrictedLowDelay, ApplicationRestrictedSILK, ApplicationRestrictedCELT} {
				set := map[int]bool{}
				for k := 0; k <= 60; k++ {
					set[k*fs/400] = true
					set[k*fs/400+1] = true
					set[k*fs/400-1] = true
				}
				sizes := []int{}
				for s := range set {
					sizes = append(sizes, s)
				}
				sort.Ints(sizes)
				for _, efd := range []int{5000, 5001, 5003, 5004, 5006, 5009} {
					le, _ := cgoref.NewAudEncoder(fs, ch, app)
					ge, _ := newAuditEnc(fs, ch, app)
					le.Set(4040, efd)
					gefdErr := ge.SetExpertFrameDuration(ExpertFrameDuration(efd))
					if gefdErr != nil {
						c.add("SET_EXPERT_FRAME_DURATION", false, fmt.Sprintf("fs=%d efd=%d go=%v", fs, efd, gefdErr))
					}
					for _, n := range sizes {
						if n < 0 {
							continue
						}
						pcm := sineF32(max(n, 1), ch, fs, 300, 0.2, 0)
						lp, lr := le.EncodeFloat(pcm, n, 4000)
						gp, gerr := ge.EncodeFloat32(pcm[:n*ch], n)
						ok := (lr > 0) == (gerr == nil)
						durOK := true
						if ok && lr > 0 {
							ld, _ := PacketGetNumSamples(lp, fs)
							gd, _ := PacketGetNumSamples(gp, fs)
							durOK = ld == gd
						}
						c.add(fmt.Sprintf("efd=%d accept", efd), ok, fmt.Sprintf("fs=%d ch=%d app=%d n=%d lib=%d go=%v", fs, ch, app, n, lr, gerr))
						c.add(fmt.Sprintf("efd=%d packet duration", efd), durOK, fmt.Sprintf("fs=%d ch=%d app=%d n=%d", fs, ch, app, n))
					}
					le.Close()
				}
			}
		}
	}
	c.dump(t, "enc-frame")
	// max_data_bytes small buffers: libopus returns BUFFER_TOO_SMALL; Go has no max_data_bytes
	le, _ := cgoref.NewAudEncoder(48000, 1, ApplicationAudio)
	for _, mb := range []int{0, 1, 2, 3} {
		_, r := le.EncodeFloat(sineF32(960, 1, 48000, 440, .3, 0), 960, mb)
		t.Logf("libopus encode max_data_bytes=%d => %d", mb, r)
	}
	le.Close()
}

func libPackets(t *testing.T, fs, ch, app, ms int, cfg func(*cgoref.AudEncoder), frames int) [][]byte {
	le, _ := cgoref.NewAudEncoder(fs, ch, app)
	defer le.Close()
	if cfg != nil {
		cfg(le)
	}
	n := fs * ms / 1000
	var out [][]byte
	rng := rand.New(rand.NewSource(int64(fs + ch + app + ms)))
	for f := 0; f < frames; f++ {
		pcm := sineF32(n, ch, fs, 200+float64(f%7)*150, 0.3, f*n)
		for i := range pcm {
			pcm[i] += float32(rng.NormFloat64() * 0.02)
		}
		if f%9 == 8 {
			for i := range pcm {
				pcm[i] = 0
			}
		}
		p, r := le.EncodeFloat(pcm, n, 1500)
		if r > 0 {
			out = append(out, p)
		}
	}
	return out
}

func TestAuditDecoderCTL(t *testing.T) {
	auditEnabled(t)
	c := newCmpLog()
	for _, fs := range []int{0, 8000, 11025, 12000, 16000, 24000, 44100, 48000, 96000} {
		for _, ch := range []int{0, 1, 2, 3} {
			ld, lerr := cgoref.NewAudDecoder(fs, ch)
			_, gerr := NewDecoder(fs, ch)
			c.add("opus_decoder_create", (ld != nil) == (gerr == nil), fmt.Sprintf("fs=%d ch=%d lib=%d go=%v", fs, ch, lerr, gerr))
			if ld != nil {
				ld.Close()
			}
		}
	}
	type dg struct {
		name string
		req  int
		g    func(d *Decoder) int
	}
	dgs := []dg{
		{"BANDWIDTH", 4009, func(d *Decoder) int { return d.Bandwidth() }},
		{"SAMPLE_RATE", 4029, func(d *Decoder) int { return d.SampleRate() }},
		{"FINAL_RANGE", 4031, func(d *Decoder) int { return int(int32(d.FinalRange())) }},
		{"PITCH", 4033, func(d *Decoder) int { return d.Pitch48kHz() }},
		{"GAIN", 4045, func(d *Decoder) int { return d.Gain() }},
		{"LAST_PACKET_DURATION", 4039, func(d *Decoder) int { return d.GetLastPacketDuration() }},
		{"PHASE_INVERSION_DISABLED", 4047, func(d *Decoder) int { return b2i(d.PhaseInversionDisabled()) }},
	}
	for _, fs := range auditRates {
		for _, ch := range []int{1, 2} {
			ld, _ := cgoref.NewAudDecoder(fs, ch)
			gd, _ := NewDecoder(fs, ch)
			for _, g := range dgs {
				lv, lr := ld.Get(g.req)
				c.add("default GET_"+g.name, lr == 0 && lv == g.g(gd), fmt.Sprintf("fs=%d ch=%d lib=%d go=%d", fs, ch, lv, g.g(gd)))
			}
			for _, v := range []int{-32769, -32768, 0, 100, 32767, 32768} {
				lr := ld.Set(4034, v)
				gerr := gd.SetGain(v)
				c.add("SET_GAIN accept", (lr == 0) == (gerr == nil), fmt.Sprintf("v=%d lib=%d go=%v", v, lr, gerr))
			}
			_ = ld.Set(4034, 0)
			_ = gd.SetGain(0)
			for _, v := range []int{-1, 0, 5, 10, 11} {
				t.Logf("libopus decoder SET_COMPLEXITY(%d) => %d (no Go equivalent)", v, ld.Set(4010, v))
				break
			}
			ld.Close()
		}
	}
	// decode sequences
	for _, fs := range auditRates {
		for _, ch := range []int{1, 2} {
			for _, cfg := range []struct {
				name string
				app  int
				ms   int
				f    func(*cgoref.AudEncoder)
			}{
				{"silk", ApplicationVOIP, 20, func(e *cgoref.AudEncoder) { e.Set(4002, 16000) }},
				{"silk60", ApplicationVOIP, 60, func(e *cgoref.AudEncoder) { e.Set(4002, 12000) }},
				{"hybrid", ApplicationVOIP, 20, func(e *cgoref.AudEncoder) { e.Set(4002, 32000) }},
				{"celt", ApplicationAudio, 10, func(e *cgoref.AudEncoder) { e.Set(4002, 96000) }},
				{"celt2.5", ApplicationRestrictedLowDelay, 2, nil},
			} {
				ms := cfg.ms
				encFs := 48000
				var pk [][]byte
				if ms == 2 {
					le, _ := cgoref.NewAudEncoder(encFs, ch, cfg.app)
					for f := 0; f < 30; f++ {
						p, r := le.EncodeFloat(sineF32(120, ch, encFs, 500, .3, f*120), 120, 1500)
						if r > 0 {
							pk = append(pk, p)
						}
					}
					le.Close()
				} else {
					pk = libPackets(t, encFs, ch, cfg.app, ms, cfg.f, 30)
				}
				ld, _ := cgoref.NewAudDecoder(fs, ch)
				gd, _ := NewDecoder(fs, ch)
				for i, p := range pk {
					_, lr := ld.Decode(p, fs*120/1000, ch, false)
					gpcm, gerr := gd.DecodeFloat32(p)
					c.add("decode "+cfg.name+" accept/len", lr >= 0 && gerr == nil && lr == len(gpcm)/ch, fmt.Sprintf("fs=%d ch=%d pkt=%d lib=%d go=%d,%v", fs, ch, i, lr, len(gpcm)/ch, gerr))
					for _, g := range dgs {
						lv, _ := ld.Get(g.req)
						gv := g.g(gd)
						c.add("GET_"+g.name+" after "+cfg.name, lv == gv, fmt.Sprintf("fs=%d ch=%d pkt=%d lib=%d go=%d", fs, ch, i, lv, gv))
						if g.name == "PITCH" {
							c.add(fmt.Sprintf("PITCH fs=%d %s", fs, cfg.name), lv == gv, fmt.Sprintf("ch=%d pkt=%d lib=%d go=%d", ch, i, lv, gv))
							c.add(fmt.Sprintf("PITCH fs=%d %s go*48k/fs==lib", fs, cfg.name), lv == gv*48000/fs, fmt.Sprintf("ch=%d pkt=%d lib=%d go=%d", ch, i, lv, gv))
						}
					}
					if i == 10 { // PLC
						_, lr := ld.Decode(nil, fs/50, ch, false)
						_, gerr := gd.DecodePLCFloat32(fs / 50)
						c.add("PLC accept", (lr >= 0) == (gerr == nil), "")
						for _, g := range dgs {
							lv, _ := ld.Get(g.req)
							gv := g.g(gd)
							c.add("GET_"+g.name+" after PLC("+cfg.name+")", lv == gv, fmt.Sprintf("fs=%d ch=%d lib=%d go=%d", fs, ch, lv, gv))
						}
					}
				}
				ld.Reset()
				_ = gd.Reset()
				for _, g := range dgs {
					lv, _ := ld.Get(g.req)
					c.add("GET_"+g.name+" after RESET", lv == g.g(gd), fmt.Sprintf("fs=%d ch=%d lib=%d go=%d", fs, ch, lv, g.g(gd)))
				}
				ld.Close()
			}
		}
	}
	c.dump(t, "dec")
}

func TestAuditDecoderFrameSizes(t *testing.T) {
	auditEnabled(t)
	c := newCmpLog()
	for _, fs := range auditRates {
		for _, ch := range []int{1, 2} {
			pkts := libPackets(t, 48000, ch, ApplicationAudio, 20, func(e *cgoref.AudEncoder) { e.Set(4002, 64000) }, 3)
			silk := libPackets(t, 48000, ch, ApplicationVOIP, 20, func(e *cgoref.AudEncoder) {
				e.Set(4002, 16000)
				e.Set(4012, 1)
				e.Set(4014, 20)
			}, 4)
			need := fs / 50
			sizes := []int{0, 1, need - 1, need, need + 1, 2 * need, fs * 120 / 1000, fs*120/1000 + 1}
			for k := 1; k <= 48; k++ {
				sizes = append(sizes, k*fs/400, k*fs/400+1)
			}
			for _, n := range sizes {
				// normal decode with output buffer of n samples/channel
				ld, _ := cgoref.NewAudDecoder(fs, ch)
				gd, _ := NewDecoder(fs, ch)
				_, lr := ld.Decode(pkts[0], n, ch, false)
				buf := make([]int16, n*ch)
				_, gerr := gd.Decode(pkts[0], buf)
				c.add("decode buffer accept", (lr >= 0) == (gerr == nil), fmt.Sprintf("fs=%d ch=%d n=%d lib=%d go=%v", fs, ch, n, lr, gerr))
				if lr < 0 && gerr != nil {
					c.add("decode buffer errcode", lr == goErrCode(gerr), fmt.Sprintf("fs=%d n=%d lib=%d go=%v", fs, n, lr, gerr))
				}
				// PLC
				_, lr = ld.Decode(nil, n, ch, false)
				_, gerr = gd.DecodePLC(make([]int16, n*ch), n)
				c.add("PLC frame_size accept", (lr >= 0) == (gerr == nil), fmt.Sprintf("fs=%d ch=%d n=%d lib=%d go=%v", fs, ch, n, lr, gerr))
				ld.Close()
				// FEC on SILK packet
				ld, _ = cgoref.NewAudDecoder(fs, ch)
				gd, _ = NewDecoder(fs, ch)
				ld.Decode(silk[0], fs/50, ch, false)
				_, _ = gd.DecodeFloat32(silk[0])
				lf, lr := ld.Decode(silk[2], n, ch, true)
				gf, gerr := gd.DecodeFECFloat32(silk[2], n)
				c.add("FEC frame_size accept", (lr >= 0) == (gerr == nil), fmt.Sprintf("fs=%d ch=%d n=%d lib=%d go=%v", fs, ch, n, lr, gerr))
				if lr >= 0 && gerr == nil {
					c.add("FEC returned samples", len(lf) == len(gf), fmt.Sprintf("fs=%d n=%d lib=%d go=%d", fs, n, len(lf)/ch, len(gf)/ch))
				}
				ld.Close()
			}
		}
	}
	c.dump(t, "dec-frame")
}

// --- multistream / surround / projection ---

func TestAuditMultistreamConstructors(t *testing.T) {
	auditEnabled(t)
	c := newCmpLog()
	rng := rand.New(rand.NewSource(23))
	for trial := 0; trial < 20000; trial++ {
		ch := rng.Intn(10)
		if trial%10 == 0 {
			ch = []int{0, 254, 255, 256}[rng.Intn(4)]
		}
		streams := rng.Intn(8)
		coupled := rng.Intn(5) - 1
		if trial%13 == 0 {
			streams, coupled = 200, 56
		}
		mapping := make([]byte, max(ch, 0))
		for i := range mapping {
			switch rng.Intn(6) {
			case 0:
				mapping[i] = 255
			case 1:
				mapping[i] = byte(rng.Intn(256))
			default:
				mapping[i] = byte(rng.Intn(max(1, streams+coupled)))
			}
		}
		if trial%3 == 0 { // valid-ish identity mapping
			for i := range mapping {
				mapping[i] = byte(i % max(1, streams+coupled))
			}
		}
		fs := []int{48000, 16000, 44100}[rng.Intn(3)]
		le, lerr := cgoref.NewAudMSEncoder(fs, ch, streams, coupled, mapping, ApplicationAudio)
		_, gerr := NewMultistreamEncoder(fs, ch, streams, coupled, mapping, ApplicationAudio)
		c.add("ms_encoder_create accept", (le != nil) == (gerr == nil), fmt.Sprintf("fs=%d ch=%d s=%d c=%d map=%v lib=%d go=%v", fs, ch, streams, coupled, trunc(mapping), lerr, gerr))
		if le != nil {
			le.Close()
		}
		ld, lerr := cgoref.NewAudMSDecoder(fs, ch, streams, coupled, mapping)
		_, gerr = NewMultistreamDecoder(fs, ch, streams, coupled, mapping)
		c.add("ms_decoder_create accept", (ld != nil) == (gerr == nil), fmt.Sprintf("fs=%d ch=%d s=%d c=%d map=%v lib=%d go=%v", fs, ch, streams, coupled, trunc(mapping), lerr, gerr))
		if ld != nil {
			ld.Close()
		}
	}
	// surround
	for _, fs := range []int{8000, 48000} {
		for _, family := range []int{-1, 0, 1, 2, 3, 4, 254, 255} {
			for ch := 0; ch <= 256; ch++ {
				if family != 255 && family != 2 && ch > 12 && ch != 255 && ch%17 != 0 {
					continue
				}
				le, ls, lc, lm, lerr := cgoref.NewAudSurroundEncoder(fs, ch, family, ApplicationAudio)
				ge, gerr := NewSurroundEncoder(fs, ch, family, ApplicationAudio)
				fam := fmt.Sprintf("surround family=%d", family)
				c.add(fam+" accept", (le != nil) == (gerr == nil), fmt.Sprintf("fs=%d ch=%d lib=%d go=%v", fs, ch, lerr, gerr))
				if le != nil && gerr == nil {
					same := ls == ge.Streams() && lc == ge.CoupledStreams() && bytes.Equal(lm, ge.Mapping())
					c.add(fam+" streams/coupled/mapping", same, fmt.Sprintf("fs=%d ch=%d lib=%d/%d/%v go=%d/%d/%v", fs, ch, ls, lc, trunc(lm), ge.Streams(), ge.CoupledStreams(), trunc(ge.Mapping())))
					// bitrate getter after AUTO and numeric set
					for _, br := range []int{-1000, -1, 1, 64000, 100000000} {
						lr := le.Set(4002, br)
						gerr := ge.SetBitrate(br)
						lv, _ := le.Get(4003)
						c.add(fam+" SET_BITRATE accept", (lr == 0) == (gerr == nil), fmt.Sprintf("ch=%d br=%d lib=%d go=%v", ch, br, lr, gerr))
						c.add(fam+" GET_BITRATE readback", lv == ge.Bitrate(), fmt.Sprintf("fs=%d ch=%d br=%d lib=%d go=%d", fs, ch, br, lv, ge.Bitrate()))
					}
					// generic getters via the multistream encoder
					for _, g := range []struct {
						req int
						v   func() int
						n   string
					}{
						{4011, func() int { return ge.Complexity() }, "COMPLEXITY"},
						{4007, func() int { return b2i(ge.VBR()) }, "VBR"},
						{4027, func() int { return ge.Lookahead() }, "LOOKAHEAD"},
						{4029, func() int { return ge.SampleRate() }, "SAMPLE_RATE"},
						{4001, func() int { return int(ge.Application()) }, "APPLICATION"},
						{4009, func() int { return ge.Bandwidth() }, "BANDWIDTH"},
						{4037, func() int { return ge.LSBDepth() }, "LSB_DEPTH"},
						{4041, func() int { return int(ge.ExpertFrameDuration()) }, "EXPERT_FRAME_DURATION"},
					} {
						lv, lr := le.Get(g.req)
						c.add(fam+" GET_"+g.n, lr == 0 && lv == g.v(), fmt.Sprintf("fs=%d ch=%d lib=%d(%d) go=%d", fs, ch, lv, lr, g.v()))
					}
				}
				if le != nil {
					le.Close()
				}
				// surround decoder (Go-only convenience): accept parity with libopus encoder
				_, gderr := NewSurroundDecoder(fs, ch, family)
				c.add(fam+" decoder accept vs lib encoder", (le != nil) == (gderr == nil), fmt.Sprintf("fs=%d ch=%d lib=%d go=%v", fs, ch, lerr, gderr))
			}
		}
	}
	// projection / ambisonics
	for _, fs := range []int{16000, 48000} {
		for _, family := range []int{2, 3, 1, 255} {
			for ch := 0; ch <= 230; ch++ {
				ls, lc, lg, lm, lerr := cgoref.AudAmbisonics(fs, ch, family, ApplicationAudio)
				lok := lerr == 0 && (ls > 0 || lm != nil)
				ge, gerr := NewProjectionEncoder(fs, ch, family, ApplicationAudio)
				fam := fmt.Sprintf("projection family=%d", family)
				c.add(fam+" accept", lok == (gerr == nil), fmt.Sprintf("fs=%d ch=%d lib=%d go=%v", fs, ch, lerr, gerr))
				if lok && gerr == nil {
					c.add(fam+" streams/coupled", ls == ge.Streams() && lc == ge.CoupledStreams(), fmt.Sprintf("ch=%d lib=%d/%d go=%d/%d", ch, ls, lc, ge.Streams(), ge.CoupledStreams()))
					c.add(fam+" demixing gain", lg == ge.DemixingMatrixGain(), fmt.Sprintf("ch=%d lib=%d go=%d", ch, lg, ge.DemixingMatrixGain()))
					c.add(fam+" demixing matrix bytes", bytes.Equal(lm, ge.DemixingMatrixBytes()), fmt.Sprintf("ch=%d liblen=%d golen=%d", ch, len(lm), len(ge.DemixingMatrixBytes())))
					// projection decoder acceptance with the matrix and with a truncated matrix
					for _, mm := range [][]byte{lm, lm[:len(lm)-1], append(append([]byte{}, lm...), 0)} {
						lr := cgoref.AudProjectionDecoderCreate(fs, ch, ls, lc, mm)
						_, gerr := NewProjectionDecoder(fs, ch, ls, lc, mm)
						c.add("projection_decoder_create accept", (lr == 0) == (gerr == nil), fmt.Sprintf("ch=%d mlen=%d lib=%d go=%v", ch, len(mm), lr, gerr))
					}
				}
			}
		}
	}
	c.dump(t, "ms")
}

func TestAuditStrerror(t *testing.T) {
	auditEnabled(t)
	var parts []string
	for code := 0; code >= -8; code-- {
		parts = append(parts, fmt.Sprintf("%d=%q", code, cgoref.AudStrerror(code)))
	}
	t.Log("libopus strerror: " + strings.Join(parts, ", "))
}

var auditProfile = EncoderProfileLegacy

func newAuditEnc(fs, ch, app int) (*Encoder, error) {
	return NewEncoderWithProfile(fs, ch, Application(app), auditProfile)
}

func TestAuditEncoderCTLLibopusProfile(t *testing.T) {
	auditEnabled(t)
	auditProfile = EncoderProfileLibopus
	defer func() { auditProfile = EncoderProfileLegacy }()
	TestAuditEncoderCTL(t)
}

func TestAuditEncoderFrameSizesLibopusProfile(t *testing.T) {
	auditEnabled(t)
	auditProfile = EncoderProfileLibopus
	defer func() { auditProfile = EncoderProfileLegacy }()
	TestAuditEncoderFrameSizes(t)
}
