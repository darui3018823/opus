//go:build opusref

package opus

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestChaosEncoderCTLOracle drives the libopus-profile encoder and the
// plain-C oracle through random CTL changes, resets, frame durations and
// int16/float input mid-stream, and compares every packet, final range and
// CTL result. CHAOS_BASE, CHAOS_SEEDS and CHAOS_FRAMES widen the sweep;
// CHAOS_DUMP logs every packet and CHAOS_HEX describes a mismatch.
func TestChaosEncoderCTLOracle(t *testing.T) {
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skip("encoder oracle not built")
	}
	seeds := 120
	if v, err := strconv.Atoi(os.Getenv("CHAOS_SEEDS")); err == nil {
		seeds = v
	}
	base := int64(1)
	if v, err := strconv.ParseInt(os.Getenv("CHAOS_BASE"), 10, 64); err == nil {
		base = v
	}
	frames := 60
	if v, err := strconv.Atoi(os.Getenv("CHAOS_FRAMES")); err == nil {
		frames = v
	}
	fails := 0
	for s := base; s < base+int64(seeds) && fails < 8; s++ {
		if msg := chaosRun(t, s, frames); msg != "" {
			fails++
			t.Errorf("seed %d: %s", s, msg)
		}
	}
}

type chaosCTL struct {
	name string // oracle script name
	vals []int
	set  func(e *Encoder, v int) error
}

var chaosCTLs = []chaosCTL{
	{"bitrate", []int{-1000, -1, 6000, 8000, 12000, 16000, 20000, 24000, 32000, 48000, 64000, 96000, 128000, 256000},
		func(e *Encoder, v int) error { return e.SetBitrate(v) }},
	{"complexity", []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		func(e *Encoder, v int) error { return e.SetComplexity(v) }},
	{"maxbw", []int{1101, 1102, 1103, 1104, 1105},
		func(e *Encoder, v int) error { return e.SetMaxBandwidth(v) }},
	{"bw", []int{-1000, 1101, 1102, 1103, 1104, 1105},
		func(e *Encoder, v int) error { return e.SetBandwidth(v) }},
	{"fc", []int{-1000, 1, 2},
		func(e *Encoder, v int) error { return e.SetForceChannels(v) }},
	{"lsb", []int{8, 16, 24},
		func(e *Encoder, v int) error { return e.SetLSBDepth(v) }},
	{"loss", []int{0, 5, 20, 50},
		func(e *Encoder, v int) error { e.SetPacketLossPerc(v); return nil }},
	{"signal", []int{-1000, 3001, 3002},
		func(e *Encoder, v int) error {
			m := map[int]SignalType{-1000: SignalAuto, 3001: SignalVoice, 3002: SignalMusic}
			e.SetSignalType(m[v])
			return nil
		}},
	{"vbr", []int{0, 1}, func(e *Encoder, v int) error { e.SetVBR(v == 1); return nil }},
	{"vbrc", []int{0, 1}, func(e *Encoder, v int) error { e.SetVBRConstraint(v == 1); return nil }},
	{"dtx", []int{0, 1}, func(e *Encoder, v int) error { e.SetDTX(v == 1); return nil }},
	{"fec", []int{0, 1, 2}, func(e *Encoder, v int) error { return e.SetInbandFECMode(v) }},
	{"pred", []int{0, 1}, func(e *Encoder, v int) error { e.SetPredictionDisabled(v == 1); return nil }},
	{"phaseinv", []int{0, 1}, func(e *Encoder, v int) error { e.SetPhaseInversionDisabled(v == 1); return nil }},
}

type chaosOp struct {
	kind byte // 'c' ctl, 'r' reset, 'e' float frame, 'i' int16 frame
	ctl  *chaosCTL
	val  int
	n    int
	pcm  []float32
}

type chaosApp struct {
	name string
	app  int
}

func chaosRun(t *testing.T, seed int64, frames int) string {
	fs, ch, ap, ops := chaosGen(seed, frames)
	return chaosEval(t, fs, ch, ap, ops)
}

func chaosGen(seed int64, frames int) (int, int, chaosApp, []chaosOp) {
	r := rand.New(rand.NewSource(seed))
	rates := []int{8000, 12000, 16000, 24000, 48000}
	apps := []chaosApp{{"voip", ApplicationVOIP}, {"audio", ApplicationAudio}, {"lowdelay", ApplicationRestrictedLowDelay}}
	fs := rates[r.Intn(len(rates))]
	ch := 1 + r.Intn(2)
	ap := apps[r.Intn(len(apps))]

	var ops []chaosOp
	durs := []int{25, 50, 100, 200, 400, 600} // tenths of ms
	phase, kind := 0, r.Intn(4)
	for f := 0; f < frames; f++ {
		if f == 0 || r.Intn(6) == 0 {
			for k := 0; k < 1+r.Intn(2); k++ {
				c := &chaosCTLs[r.Intn(len(chaosCTLs))]
				ops = append(ops, chaosOp{kind: 'c', ctl: c, val: c.vals[r.Intn(len(c.vals))]})
			}
		}
		if f > 0 && r.Intn(40) == 0 {
			ops = append(ops, chaosOp{kind: 'r'})
		}
		if r.Intn(8) == 0 {
			kind = r.Intn(4)
		}
		n := fs * durs[r.Intn(len(durs))] / 10000
		pcm := make([]float32, n*ch)
		for i := 0; i < n; i++ {
			var v float64
			switch kind {
			case 0:
				v = 0.3 * math.Sin(2*math.Pi*(220+float64(f)*37)*float64(phase+i)/float64(fs))
			case 1:
				v = (r.Float64()*2 - 1) * 0.2
			case 2:
				v = 0
			case 3:
				v = 1.3 * math.Sin(2*math.Pi*97*float64(phase+i)/float64(fs))
			}
			for c := 0; c < ch; c++ {
				x := v
				if c == 1 {
					x = 0.7*v + 0.05*(r.Float64()*2-1)
				}
				pcm[i*ch+c] = float32(x)
			}
		}
		phase += n
		op := chaosOp{kind: 'e', n: n, pcm: pcm}
		if r.Intn(3) == 0 {
			op.kind = 'i'
		}
		ops = append(ops, op)
	}
	return fs, ch, ap, ops
}

func chaosEval(t *testing.T, fs, ch int, ap chaosApp, ops []chaosOp) string {
	cfg := fmt.Sprintf("fs=%d ch=%d app=%s", fs, ch, ap.name)
	// Oracle run.
	dir := t.TempDir()
	var pf, sf bytes.Buffer
	for _, op := range ops {
		switch op.kind {
		case 'c':
			fmt.Fprintf(&sf, "c %s %d\n", op.ctl.name, op.val)
		case 'r':
			sf.WriteString("r\n")
		default:
			fmt.Fprintf(&sf, "%c %d\n", op.kind, op.n)
			for _, v := range op.pcm {
				binary.Write(&pf, binary.LittleEndian, v)
			}
		}
	}
	pcmPath, scriptPath := filepath.Join(dir, "in.f32"), filepath.Join(dir, "s.txt")
	os.WriteFile(pcmPath, pf.Bytes(), 0o644)
	os.WriteFile(scriptPath, sf.Bytes(), 0o644)
	cmd := exec.Command(encOraclePath(), "--script-enc", strconv.Itoa(fs), strconv.Itoa(ch), ap.name, pcmPath, scriptPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("oracle: %v %s", err, stderr.String())
	}
	var lines []string
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	li := 0
	next := func() []string {
		for li < len(lines) {
			f := strings.Fields(lines[li])
			li++
			if len(f) > 0 && f[0] != "X" {
				return f
			}
		}
		return nil
	}

	ge, err := NewEncoderWithProfile(fs, ch, Application(ap.app), EncoderProfileLibopus)
	if err != nil {
		return fmt.Sprintf("%s go create: %v", cfg, err)
	}
	// The oracle's output buffer (max_data_bytes) is 7656 bytes.
	ge.SetMaxPacketSize(7656)
	var hist []string
	frame := 0
	for _, op := range ops {
		switch op.kind {
		case 'c':
			got := op.ctl.set(ge, op.val)
			f := next()
			hist = append(hist, fmt.Sprintf("f%d:%s=%d", frame, op.ctl.name, op.val))
			if len(f) != 3 || f[0] != "K" {
				return fmt.Sprintf("%s oracle desync at ctl: %v", cfg, f)
			}
			lr, _ := strconv.Atoi(f[2])
			if (lr == 0) != (got == nil) {
				return fmt.Sprintf("%s ctl %s=%d lib=%d go=%v hist=%v", cfg, op.ctl.name, op.val, lr, got, hist)
			}
		case 'r':
			ge.Reset()
			hist = append(hist, fmt.Sprintf("f%d:reset", frame))
		default:
			var gp []byte
			var gerr error
			if op.kind == 'i' {
				p16 := make([]int16, len(op.pcm))
				for i, v := range op.pcm {
					x := math.Floor(float64(v)*32768 + 0.5)
					p16[i] = int16(max(-32768, min(32767, x)))
				}
				gp, gerr = ge.Encode(p16, op.n)
			} else {
				gp, gerr = ge.EncodeFloat32(op.pcm, op.n)
			}
			f := next()
			if f == nil {
				return fmt.Sprintf("%s oracle output ended at frame %d", cfg, frame)
			}
			if f[0] == "E" {
				if gerr == nil {
					return fmt.Sprintf("%s frame %d n=%d %c: lib error %s, go ok hist=%v", cfg, frame, op.n, op.kind, f[1], hist)
				}
				frame++
				continue
			}
			if gerr != nil {
				return fmt.Sprintf("%s frame %d n=%d %c: go %v, lib ok hist=%v", cfg, frame, op.n, op.kind, gerr, hist)
			}
			lp, _ := hex.DecodeString(f[2])
			rf := next()
			if os.Getenv("CHAOS_DUMP") != "" {
				t.Logf("frame %d %c n=%d lib len=%d toc=%02x go len=%d toc=%02x eq=%v", frame, op.kind, op.n, len(lp), lp[0], len(gp), firstByte(gp), bytes.Equal(lp, gp))
			}
			if !bytes.Equal(lp, gp) {
				if os.Getenv("CHAOS_HEX") != "" {
					d := 0
					for d < len(lp) && d < len(gp) && lp[d] == gp[d] {
						d++
					}
					t.Logf("first diff at byte %d; lib[1:3]=%x go[1:3]=%x", d, lp[1:min(3, len(lp))], gp[1:min(3, len(gp))])
					lh, _ := PacketHasLBRR(lp)
					gh, _ := PacketHasLBRR(gp)
					_, lf, _, _ := PacketParse(lp)
					_, gf, _, _ := PacketParse(gp)
					var ls, gs []int
					for _, f := range lf {
						ls = append(ls, len(f))
					}
					for _, f := range gf {
						gs = append(gs, len(f))
					}
					t.Logf("LBRR lib=%v go=%v frame sizes lib=%v go=%v", lh, gh, ls, gs)
				}
				return fmt.Sprintf("%s frame %d n=%d %c: packets differ (lib %d bytes toc %02x, go %d bytes toc %02x) hist=%v", cfg, frame, op.n, op.kind, len(lp), lp[0], len(gp), firstByte(gp), hist)
			}
			if len(rf) == 2 && rf[1] != fmt.Sprintf("%08x", ge.FinalRange()) {
				return fmt.Sprintf("%s frame %d: final range lib=%s go=%08x hist=%v", cfg, frame, rf[1], ge.FinalRange(), hist)
			}
			frame++
		}
	}
	return ""
}

func firstByte(b []byte) byte {
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

// TestChaosEncoderCTLMinimize drops CTL and reset ops of CHAOS_SEED while it
// still fails, then logs the remaining script (a debugging aid).
func TestChaosEncoderCTLMinimize(t *testing.T) {
	seed, err := strconv.ParseInt(os.Getenv("CHAOS_SEED"), 10, 64)
	if err != nil {
		t.Skip("CHAOS_SEED not set")
	}
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skip("encoder oracle not built")
	}
	fs, ch, ap, ops := chaosGen(seed, 60)
	msg := chaosEval(t, fs, ch, ap, ops)
	if msg == "" {
		t.Fatal("seed passes")
	}
	for i := 0; i < len(ops); {
		if ops[i].kind != 'c' && ops[i].kind != 'r' {
			i++
			continue
		}
		trial := append(append([]chaosOp(nil), ops[:i]...), ops[i+1:]...)
		if m := chaosEval(t, fs, ch, ap, trial); m != "" {
			ops, msg = trial, m
			continue
		}
		i++
	}
	// Try float input everywhere.
	allE := append([]chaosOp(nil), ops...)
	for i := range allE {
		if allE[i].kind == 'i' {
			allE[i].kind = 'e'
		}
	}
	t.Logf("float-only: %q", chaosEval(t, fs, ch, ap, allE))
	frame := 0
	for _, op := range ops {
		switch op.kind {
		case 'c':
			t.Logf("f%d c %s %d", frame, op.ctl.name, op.val)
		case 'r':
			t.Logf("f%d reset", frame)
		default:
			frame++
		}
	}
	t.Logf("minimal: %s", msg)
}
