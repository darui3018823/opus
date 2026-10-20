//go:build opusref

package opus

// Encoder audit harness (not for commit): drives the --script-enc /
// --script-ms modes of the audit oracle build and the Go encoder with the
// same input and control script, then compares packets, final ranges, DTX
// state, CTL return codes and lookahead.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func auditOraclePath() string {
	return filepath.Join(os.TempDir(), "opusoracle_audit", "enc_oracle.exe")
}

type aStep struct {
	op  byte // 'e' float, 'i' int16, 'c' ctl, 'r' reset, 'q' lookahead, 'm' maxbytes
	ctl string
	val int
}

type aCfg struct {
	name     string
	rate     int
	ch       int
	app      string
	ms       bool
	family   int
	pcm      []float32 // interleaved
	steps    []aStep
	noFloat  bool
	profile  bool // the *WithProfile constructors with EncoderProfileLibopus
	onPacket func(pkt int, g auditGoEncoder, goPkt, cPkt []byte)
	oracle   string // executable to run; auditOraclePath() when empty
}

type aRes struct {
	packets, identical, firstDiff, rangeFirst, dtxFirst, errMismatch, ctlMismatch, laMismatch int
	sizeOnly, cdecRange, goDecRange                                                           int
	info                                                                                      string
	goBytes, cBytes                                                                           int
}

func (r aRes) ok() bool {
	return r.identical == r.packets && r.rangeFirst < 0 && r.errMismatch == 0 && r.ctlMismatch == 0 && r.laMismatch == 0 && r.dtxFirst < 0
}

func (r aRes) String() string {
	return fmt.Sprintf("ident %d/%d firstDiff=%d rangeFirst=%d dtxFirst=%d errMis=%d ctlMis=%d laMis=%d bytes go=%d c=%d cdecRangeMis=%d goDecRangeMis=%d %s",
		r.identical, r.packets, r.firstDiff, r.rangeFirst, r.dtxFirst, r.errMismatch, r.ctlMismatch, r.laMismatch, r.goBytes, r.cBytes, r.cdecRange, r.goDecRange, r.info)
}

type cEvent struct {
	kind byte
	pkt  []byte
	rng  uint32
	err  int
	val  int
	name string
}

func runAuditOracle(t *testing.T, c aCfg) []cEvent {
	t.Helper()
	dir := t.TempDir()
	pcmPath := filepath.Join(dir, "in.f32")
	scrPath := filepath.Join(dir, "script.txt")
	buf := make([]byte, 4*len(c.pcm))
	for i, v := range c.pcm {
		binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(v))
	}
	if err := os.WriteFile(pcmPath, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if ob := os.Getenv("AUDIT_OUTBYTES"); ob != "" {
		fmt.Fprintf(&sb, "m %s\n", ob)
	}
	for _, s := range c.steps {
		switch s.op {
		case 'e', 'i', 'm':
			fmt.Fprintf(&sb, "%c %d\n", s.op, s.val)
		case 'c':
			fmt.Fprintf(&sb, "c %s %d\n", s.ctl, s.val)
		case 'r', 'q':
			fmt.Fprintf(&sb, "%c\n", s.op)
		case 't':
			fmt.Fprintf(&sb, "t %d\n", s.val)
		}
	}
	if err := os.WriteFile(scrPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var args []string
	if c.ms {
		args = []string{"--script-ms", strconv.Itoa(c.rate), strconv.Itoa(c.ch), strconv.Itoa(c.family), c.app, pcmPath, scrPath}
	} else {
		args = []string{"--script-enc", strconv.Itoa(c.rate), strconv.Itoa(c.ch), c.app, pcmPath, scrPath}
	}
	oracle := c.oracle
	if oracle == "" {
		oracle = auditOraclePath()
	}
	cmd := exec.Command(oracle, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle: %v\n%s", err, stderr.String())
	}
	if p := os.Getenv("AUDIT_STDERR"); p != "" {
		_ = os.WriteFile(p, stderr.Bytes(), 0o644)
	}
	var ev []cEvent
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		f := strings.Fields(strings.TrimSpace(sc.Text()))
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "P":
			var p []byte
			if len(f) > 2 {
				p, _ = hex.DecodeString(f[2])
			}
			ev = append(ev, cEvent{kind: 'P', pkt: p})
		case "R":
			v, _ := strconv.ParseUint(f[1], 16, 32)
			ev[len(ev)-1].rng = uint32(v)
		case "X":
			v, _ := strconv.Atoi(f[1])
			ev[len(ev)-1].val = v
		case "E":
			v, _ := strconv.Atoi(f[1])
			ev = append(ev, cEvent{kind: 'E', err: v})
		case "L":
			v, _ := strconv.Atoi(f[1])
			ev = append(ev, cEvent{kind: 'L', val: v})
		case "K":
			v, _ := strconv.Atoi(f[2])
			ev = append(ev, cEvent{kind: 'K', name: f[1], err: v})
		case "C":
			v, _ := strconv.Atoi(f[1])
			ev = append(ev, cEvent{kind: 'C', err: v})
		}
	}
	return ev
}

// auditGoEncoder abstracts the single and multistream Go encoders.
type auditGoEncoder struct {
	enc  *Encoder
	ms   *MultistreamEncoder
	sur  *SurroundEncoder
	proj *ProjectionEncoder
}

func (g auditGoEncoder) ctl(name string, v int) error {
	if g.enc != nil {
		e := g.enc
		switch name {
		case "bitrate":
			return e.SetBitrate(v)
		case "complexity":
			return e.SetComplexity(v)
		case "vbr":
			e.SetVBR(v != 0)
		case "vbrc":
			e.SetVBRConstraint(v != 0)
		case "bw":
			return e.SetBandwidth(v)
		case "maxbw":
			return e.SetMaxBandwidth(v)
		case "fc":
			return e.SetForceChannels(v)
		case "signal":
			e.SetSignalType(auditSignal(v))
		case "app":
			return e.SetApplication(Application(v))
		case "loss":
			if v < 0 || v > 100 {
				return ErrBadArg
			}
			e.SetPacketLossPerc(v)
		case "fec":
			return e.SetInbandFECMode(v)
		case "dtx":
			e.SetDTX(v != 0)
		case "lsb":
			return e.SetLSBDepth(v)
		case "pred":
			e.SetPredictionDisabled(v != 0)
		case "phaseinv":
			e.SetPhaseInversionDisabled(v != 0)
		case "expert":
			return e.SetExpertFrameDuration(ExpertFrameDuration(v))
		default:
			return fmt.Errorf("unknown ctl %s", name)
		}
		return nil
	}
	m := g.ms
	switch name {
	case "bitrate":
		switch {
		case g.sur != nil:
			return g.sur.SetBitrate(v)
		case g.proj != nil:
			return g.proj.SetBitrate(v)
		}
		return m.SetBitrate(v)
	case "complexity":
		return m.SetComplexity(v)
	case "vbr":
		m.SetVBR(v != 0)
	case "vbrc":
		m.SetVBRConstraint(v != 0)
	case "bw":
		if g.sur != nil {
			return g.sur.SetBandwidth(v)
		}
		return m.SetBandwidth(v)
	case "maxbw":
		return m.SetMaxBandwidth(v)
	case "signal":
		m.SetSignalType(auditSignal(v))
	case "app":
		return m.SetApplication(Application(v))
	case "loss":
		m.SetPacketLossPerc(v)
	case "fec":
		return m.SetInbandFECMode(v)
	case "dtx":
		m.SetDTX(v != 0)
	case "lsb":
		return m.SetLSBDepth(v)
	case "pred":
		m.SetPredictionDisabled(v != 0)
	case "phaseinv":
		m.SetPhaseInversionDisabled(v != 0)
	case "expert":
		if g.proj != nil {
			return g.proj.SetExpertFrameDuration(ExpertFrameDuration(v))
		}
		return m.SetExpertFrameDuration(ExpertFrameDuration(v))
	default:
		return fmt.Errorf("unknown ctl %s", name)
	}
	return nil
}

func auditSignal(v int) SignalType {
	switch v {
	case 3001:
		return SignalVoice
	case 3002:
		return SignalMusic
	}
	return SignalAuto
}

func (g auditGoEncoder) encode(pcm []float32, n int, int16In bool) ([]byte, error) {
	if int16In {
		p := make([]int16, len(pcm))
		for i, v := range pcm {
			s := math.Floor(float64(v)*32768 + 0.5)
			p[i] = int16(math.Max(-32768, math.Min(32767, s)))
		}
		switch {
		case g.enc != nil:
			return g.enc.Encode(p, n)
		case g.sur != nil:
			return g.sur.Encode(p, n)
		case g.proj != nil:
			return g.proj.Encode(p, n)
		}
		return g.ms.Encode(p, n)
	}
	switch {
	case g.enc != nil:
		return g.enc.EncodeFloat32(pcm, n)
	case g.sur != nil:
		return g.sur.EncodeFloat32(pcm, n)
	case g.proj != nil:
		return g.proj.EncodeFloat32(pcm, n)
	}
	return g.ms.EncodeFloat32(pcm, n)
}

func (g auditGoEncoder) finalRange() uint32 {
	if g.enc != nil {
		return g.enc.FinalRange()
	}
	return g.ms.FinalRange()
}

func (g auditGoEncoder) reset() error {
	switch {
	case g.enc != nil:
		return g.enc.Reset()
	case g.proj != nil:
		return g.proj.Reset()
	}
	return g.ms.Reset()
}

func (g auditGoEncoder) lookahead() int {
	if g.enc != nil {
		return g.enc.Lookahead()
	}
	return g.ms.Lookahead()
}

func newAuditGoEncoder(t *testing.T, c aCfg) (auditGoEncoder, error) {
	app := map[string]Application{"voip": ApplicationVOIP, "audio": ApplicationAudio, "lowdelay": ApplicationRestrictedLowDelay,
		"rsilk": ApplicationRestrictedSILK, "rcelt": ApplicationRestrictedCELT}[c.app]
	if !c.ms {
		e, err := NewEncoder(c.rate, c.ch, app)
		if c.profile {
			e, err = NewEncoderWithProfile(c.rate, c.ch, app, EncoderProfileLibopus)
		}
		if err != nil {
			return auditGoEncoder{}, err
		}
		if err := e.SetModePolicy(ModePolicyLibopus); err != nil {
			t.Fatal(err)
		}
		// The script oracle's default out_data_bytes.
		if err := e.SetMaxPacketSize(7656); err != nil {
			t.Fatal(err)
		}
		return auditGoEncoder{enc: e}, nil
	}
	profile := EncoderProfileLegacy
	if c.profile {
		profile = EncoderProfileLibopus
	}
	switch c.family {
	case -1:
		layout, err := surroundLayoutFor(c.ch, MappingFamilyVorbis)
		if c.ch > 8 {
			layout, err = surroundLayoutFor(c.ch, MappingFamilyDiscrete)
		}
		if err != nil {
			return auditGoEncoder{}, err
		}
		enc, err := NewMultistreamEncoderWithProfile(c.rate, c.ch, layout.streams, layout.coupledStreams, layout.mapping, app, profile)
		if err != nil {
			return auditGoEncoder{}, err
		}
		_ = enc.SetModePolicy(ModePolicyLibopus)
		_ = enc.SetMaxPacketSize(7656)
		return auditGoEncoder{ms: enc}, nil
	case 2, 3:
		enc, err := NewProjectionEncoderWithProfile(c.rate, c.ch, c.family, app, profile)
		if err != nil {
			return auditGoEncoder{}, err
		}
		_ = enc.SetModePolicy(ModePolicyLibopus)
		_ = enc.SetMaxPacketSize(7656)
		return auditGoEncoder{ms: enc.multistream, proj: enc}, nil
	default:
		enc, err := NewSurroundEncoderWithProfile(c.rate, c.ch, c.family, app, profile)
		if err != nil {
			return auditGoEncoder{}, err
		}
		_ = enc.SetModePolicy(ModePolicyLibopus)
		_ = enc.SetMaxPacketSize(7656)
		return auditGoEncoder{ms: enc.MultistreamEncoder, sur: enc}, nil
	}
}

// runAudit runs one configuration and returns the comparison.
func runAudit(t *testing.T, c aCfg) aRes {
	t.Helper()
	ev := runAuditOracle(t, c)
	res := aRes{firstDiff: -1, rangeFirst: -1, dtxFirst: -1}
	g, err := newAuditGoEncoder(t, c)
	if len(ev) > 0 && ev[0].kind == 'C' {
		if err == nil {
			res.info = fmt.Sprintf("create: libopus err %d, Go ok", ev[0].err)
			res.errMismatch++
		}
		return res
	}
	if err != nil {
		res.info = fmt.Sprintf("create: Go %v, libopus ok", err)
		res.errMismatch++
		return res
	}
	var dec *Decoder
	var cdec *cgoref.Decoder
	if !c.ms {
		dec, _ = NewDecoder(c.rate, c.ch)
		cdec, _ = cgoref.NewDecoder(c.rate, c.ch)
		if cdec != nil {
			defer cdec.Close()
		}
	}
	pos, ei, pkt := 0, 0, 0
	next := func() *cEvent {
		if ei >= len(ev) {
			return nil
		}
		ei++
		return &ev[ei-1]
	}
	note := func(s string) {
		if len(res.info) < 2000 {
			res.info += "\n  " + s
		}
	}
	for _, s := range c.steps {
		switch s.op {
		case 'm':
			if g.enc != nil {
				if err := g.enc.SetMaxPacketSize(s.val); err != nil {
					t.Fatal(err)
				}
			} else if g.ms != nil {
				if err := g.ms.SetMaxPacketSize(s.val); err != nil {
					t.Fatal(err)
				}
			}
		case 'r':
			if err := g.reset(); err != nil {
				t.Fatal(err)
			}
		case 'q':
			e := next()
			if la := g.lookahead(); e == nil || e.kind != 'L' || e.val != la {
				res.laMismatch++
				note(fmt.Sprintf("lookahead Go %d C %+v", la, e))
			}
		case 'c':
			gerr := g.ctl(s.ctl, s.val)
			e := next()
			if e == nil || e.kind != 'K' {
				t.Fatalf("oracle event desync at ctl %s", s.ctl)
			}
			if (gerr == nil) != (e.err == 0) {
				res.ctlMismatch++
				note(fmt.Sprintf("ctl %s=%d: Go err=%v, libopus ret=%d", s.ctl, s.val, gerr, e.err))
			}
		case 'e', 'i':
			n := s.val
			in := make([]float32, n*c.ch)
			if pos < len(c.pcm) {
				copy(in, c.pcm[pos:])
			}
			pos += n * c.ch
			gp, gerr := g.encode(in, n, s.op == 'i')
			e := next()
			if e == nil {
				t.Fatalf("oracle event desync at packet %d", pkt)
			}
			if e.kind == 'E' || gerr != nil {
				if !(e.kind == 'E' && gerr != nil) {
					res.errMismatch++
					note(fmt.Sprintf("packet %d: Go err=%v (len %d), libopus %+v", pkt, gerr, len(gp), e.kind))
				}
				pkt++
				continue
			}
			res.packets++
			res.goBytes += len(gp)
			res.cBytes += len(e.pkt)
			if bytes.Equal(gp, e.pkt) {
				res.identical++
			} else if res.firstDiff < 0 {
				res.firstDiff = pkt
				at := -1
				for i := 0; i < min(len(gp), len(e.pkt)); i++ {
					if gp[i] != e.pkt[i] {
						at = i
						break
					}
				}
				gt, ct := byte(0), byte(0)
				if len(gp) > 0 {
					gt = gp[0]
				}
				if len(e.pkt) > 0 {
					ct = e.pkt[0]
				}
				note(fmt.Sprintf("first diff packet %d: Go %d B TOC %#02x / C %d B TOC %#02x, byte %d", pkt, len(gp), gt, len(e.pkt), ct, at))
				if os.Getenv("AUDIT_HEX") != "" {
					note(fmt.Sprintf("go=%x", gp))
					note(fmt.Sprintf("c =%x", e.pkt))
				}
			}
			if res.rangeFirst < 0 && g.finalRange() != e.rng {
				res.rangeFirst = pkt
				note(fmt.Sprintf("range packet %d: Go %08x C %08x", pkt, g.finalRange(), e.rng))
			}
			if cdec != nil && len(e.pkt) > 0 {
				if _, derr := cdec.DecodeFloat(e.pkt, 5760); derr == nil {
					if r, _ := cdec.FinalRange(); r != e.rng {
						res.cdecRange++
						if res.cdecRange == 1 {
							note(fmt.Sprintf("packet %d: libopus(system) dec range %08x != C enc %08x", pkt, r, e.rng))
						}
					}
				}
			}
			if dec != nil && len(gp) > 0 {
				_, derr := dec.DecodeFloat(gp)
				if derr != nil {
					note(fmt.Sprintf("packet %d: Go decoder error %v", pkt, derr))
				} else if dec.FinalRange() != g.finalRange() && g.finalRange() == e.rng {
					res.goDecRange++
				}
				if derr == nil && dec.FinalRange() != g.finalRange() && res.rangeFirst < 0 {
					res.rangeFirst = pkt
					note(fmt.Sprintf("packet %d (%d B TOC %#02x): Go dec range %08x != Go enc %08x", pkt, len(gp), gp[0], dec.FinalRange(), g.finalRange()))
					if os.Getenv("AUDIT_DUMP") != "" {
						note(fmt.Sprintf("hex %x", gp))
					}
				}
			}
			if c.onPacket != nil {
				c.onPacket(pkt, g, gp, e.pkt)
			}
			if g.enc != nil && res.dtxFirst < 0 && g.enc.InDTX() != (e.val != 0) {
				res.dtxFirst = pkt
				note(fmt.Sprintf("packet %d: InDTX Go %v C %d", pkt, g.enc.InDTX(), e.val))
			}
			pkt++
		}
	}
	return res
}

// ---- input sources ----

func auditReadWAV(t *testing.T, path string) (int, int, []float32) {
	t.Helper()
	clip, err := readCorpusWAV(path)
	if err != nil {
		t.Skipf("%s: %v", path, err)
	}
	out := make([]float32, len(clip.pcm))
	for i, v := range clip.pcm {
		out[i] = float32(v)
	}
	return clip.rate, clip.channels, out
}

// auditReadDec reads a 48 kHz stereo int16 test-vector .dec file.
func auditReadDec(t *testing.T, name string) []float32 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "opus_newvectors", name))
	if err != nil {
		t.Skip(err)
	}
	out := make([]float32, len(data)/2)
	for i := range out {
		out[i] = float32(int16(binary.LittleEndian.Uint16(data[2*i:]))) / 32768
	}
	return out
}

// auditResample is a crude deterministic decimation (box filter); only
// determinism matters since both encoders see the same samples.
func auditResample(in []float32, ch, from, to int) []float32 {
	if from == to {
		return in
	}
	n := len(in) / ch
	m := int(int64(n) * int64(to) / int64(from))
	out := make([]float32, m*ch)
	ratio := float64(from) / float64(to)
	for i := 0; i < m; i++ {
		s0 := int(float64(i) * ratio)
		s1 := max(s0+1, int(float64(i+1)*ratio))
		for c := 0; c < ch; c++ {
			acc := 0.0
			for j := s0; j < s1 && j < n; j++ {
				acc += float64(in[j*ch+c])
			}
			out[i*ch+c] = float32(acc / float64(s1-s0))
		}
	}
	return out
}

func auditToChannels(in []float32, from, to int) []float32 {
	if from == to {
		return in
	}
	n := len(in) / from
	out := make([]float32, n*to)
	for i := 0; i < n; i++ {
		for c := 0; c < to; c++ {
			out[i*to+c] = in[i*from+c%from]
		}
	}
	return out
}

func auditSnap16(in []float32) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(math.Floor(float64(v)*32768+0.5) / 32768)
	}
	return out
}

// framesSteps builds k encode steps of n samples with ctl changes before
// given frame indices.
func framesSteps(k, n int, op byte, at map[int][]aStep) []aStep {
	var s []aStep
	for f := 0; f < k; f++ {
		s = append(s, at[f]...)
		s = append(s, aStep{op: op, val: n})
	}
	return s
}

func cs(name string, v int) aStep { return aStep{op: 'c', ctl: name, val: v} }

func auditReport(t *testing.T, c aCfg) aRes {
	t.Helper()
	r := runAudit(t, c)
	if r.ok() {
		t.Logf("OK   %s: %s", c.name, r)
	} else {
		t.Errorf("DIFF %s: %s", c.name, r)
	}
	return r
}
