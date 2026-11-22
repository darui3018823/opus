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

// chaosMSEncoder is the control and coding surface shared by the surround
// and projection encoders.
type chaosMSEncoder interface {
	SetBitrate(int) error
	SetComplexity(int) error
	SetVBR(bool)
	SetVBRConstraint(bool)
	SetSignalType(SignalType)
	SetDTX(bool)
	SetInbandFECMode(int) error
	SetPacketLossPerc(int)
	SetLSBDepth(int) error
	SetPredictionDisabled(bool)
	SetPhaseInversionDisabled(bool)
	SetMaxBandwidth(int) error
	SetBandwidth(int) error
	SetMaxPacketSize(int) error
	Encode([]int16, int) ([]byte, error)
	EncodeFloat32([]float32, int) ([]byte, error)
	Reset() error
	FinalRange() uint32
}

var chaosMSCTLs = []struct {
	name string
	vals []int
	set  func(e chaosMSEncoder, v int) error
}{
	{"bitrate", []int{-1000, -1, 24000, 48000, 64000, 96000, 128000, 192000, 256000, 384000},
		func(e chaosMSEncoder, v int) error { return e.SetBitrate(v) }},
	{"complexity", []int{0, 1, 3, 5, 7, 9, 10}, func(e chaosMSEncoder, v int) error { return e.SetComplexity(v) }},
	{"maxbw", []int{1101, 1102, 1103, 1104, 1105}, func(e chaosMSEncoder, v int) error { return e.SetMaxBandwidth(v) }},
	{"bw", []int{-1000, 1101, 1103, 1104, 1105}, func(e chaosMSEncoder, v int) error { return e.SetBandwidth(v) }},
	{"lsb", []int{8, 16, 24}, func(e chaosMSEncoder, v int) error { return e.SetLSBDepth(v) }},
	{"loss", []int{0, 5, 20, 50}, func(e chaosMSEncoder, v int) error { e.SetPacketLossPerc(v); return nil }},
	{"signal", []int{-1000, 3001, 3002}, func(e chaosMSEncoder, v int) error {
		e.SetSignalType(map[int]SignalType{-1000: SignalAuto, 3001: SignalVoice, 3002: SignalMusic}[v])
		return nil
	}},
	{"vbr", []int{0, 1}, func(e chaosMSEncoder, v int) error { e.SetVBR(v == 1); return nil }},
	{"vbrc", []int{0, 1}, func(e chaosMSEncoder, v int) error { e.SetVBRConstraint(v == 1); return nil }},
	{"dtx", []int{0, 1}, func(e chaosMSEncoder, v int) error { e.SetDTX(v == 1); return nil }},
	{"fec", []int{0, 1, 2}, func(e chaosMSEncoder, v int) error { return e.SetInbandFECMode(v) }},
	{"pred", []int{0, 1}, func(e chaosMSEncoder, v int) error { e.SetPredictionDisabled(v == 1); return nil }},
	{"phaseinv", []int{0, 1}, func(e chaosMSEncoder, v int) error { e.SetPhaseInversionDisabled(v == 1); return nil }},
}

// TestChaosMultistreamCTLOracle is TestChaosEncoderCTLOracle for the
// surround (families 1, 2 and 255) and projection (family 3) encoders
// against enc_oracle --script-ms. CHAOS_MS_BASE and CHAOS_MS_SEEDS widen it.
func TestChaosMultistreamCTLOracle(t *testing.T) {
	if _, err := os.Stat(encOraclePath()); err != nil {
		t.Skip("encoder oracle not built")
	}
	seeds, base := 40, int64(1)
	if v, err := strconv.Atoi(os.Getenv("CHAOS_MS_SEEDS")); err == nil {
		seeds = v
	}
	if v, err := strconv.ParseInt(os.Getenv("CHAOS_MS_BASE"), 10, 64); err == nil {
		base = v
	}
	fails := 0
	for s := base; s < base+int64(seeds) && fails < 8; s++ {
		if msg := chaosMSRun(t, s); msg != "" {
			fails++
			t.Errorf("seed %d: %s", s, msg)
		}
	}
}

func chaosMSRun(t *testing.T, seed int64) string {
	r := rand.New(rand.NewSource(seed))
	type layout struct{ family, channels int }
	layouts := []layout{{1, 3}, {1, 4}, {1, 6}, {1, 8}, {2, 4}, {2, 9}, {255, 3}, {255, 5}, {3, 4}, {3, 9}}
	ly := layouts[r.Intn(len(layouts))]
	rates := []int{16000, 24000, 48000}
	fs := rates[r.Intn(len(rates))]
	apps := []chaosApp{{"voip", ApplicationVOIP}, {"audio", ApplicationAudio}, {"lowdelay", ApplicationRestrictedLowDelay}}
	ap := apps[r.Intn(len(apps))]
	ch := ly.channels
	cfg := fmt.Sprintf("family=%d ch=%d fs=%d app=%s", ly.family, ch, fs, ap.name)

	var ge chaosMSEncoder
	var err error
	if ly.family == 3 {
		ge, err = NewProjectionEncoderWithProfile(fs, ch, 3, Application(ap.app), EncoderProfileLibopus)
	} else {
		ge, err = NewSurroundEncoderWithProfile(fs, ch, ly.family, Application(ap.app), EncoderProfileLibopus)
	}
	if err != nil {
		return fmt.Sprintf("%s go create: %v", cfg, err)
	}
	// The oracle's output buffer (max_data_bytes) is 7656 bytes.
	ge.SetMaxPacketSize(7656)

	type op struct {
		kind byte
		ctl  int
		val  int
		n    int
		pcm  []float32
	}
	var ops []op
	durs := []int{25, 50, 100, 200, 400, 600}
	phase, kind := 0, r.Intn(3)
	for f := 0; f < 30; f++ {
		if f == 0 || r.Intn(5) == 0 {
			c := r.Intn(len(chaosMSCTLs))
			ops = append(ops, op{kind: 'c', ctl: c, val: chaosMSCTLs[c].vals[r.Intn(len(chaosMSCTLs[c].vals))]})
		}
		if f > 0 && r.Intn(30) == 0 {
			ops = append(ops, op{kind: 'r'})
		}
		if r.Intn(6) == 0 {
			kind = r.Intn(3)
		}
		n := fs * durs[r.Intn(len(durs))] / 10000
		pcm := make([]float32, n*ch)
		for i := 0; i < n; i++ {
			for c := 0; c < ch; c++ {
				var v float64
				switch kind {
				case 0:
					v = 0.25 * math.Sin(2*math.Pi*(150+70*float64(c)+float64(f)*11)*float64(phase+i)/float64(fs))
				case 1:
					v = (r.Float64()*2 - 1) * 0.15
				}
				pcm[i*ch+c] = float32(v)
			}
		}
		phase += n
		k := byte('e')
		if r.Intn(3) == 0 {
			k = 'i'
		}
		ops = append(ops, op{kind: k, n: n, pcm: pcm})
	}

	dir := t.TempDir()
	var pf, sf bytes.Buffer
	for _, o := range ops {
		switch o.kind {
		case 'c':
			fmt.Fprintf(&sf, "c %s %d\n", chaosMSCTLs[o.ctl].name, o.val)
		case 'r':
			sf.WriteString("r\n")
		default:
			fmt.Fprintf(&sf, "%c %d\n", o.kind, o.n)
			for _, v := range o.pcm {
				binary.Write(&pf, binary.LittleEndian, v)
			}
		}
	}
	pcmPath, scriptPath := filepath.Join(dir, "in.f32"), filepath.Join(dir, "s.txt")
	os.WriteFile(pcmPath, pf.Bytes(), 0o644)
	os.WriteFile(scriptPath, sf.Bytes(), 0o644)
	cmd := exec.Command(encOraclePath(), "--script-ms", strconv.Itoa(fs), strconv.Itoa(ch), strconv.Itoa(ly.family), ap.name, pcmPath, scriptPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("%s oracle: %v %s", cfg, err, stderr.String())
	}
	var lines [][]string
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) > 0 && f[0] != "X" {
			lines = append(lines, f)
		}
	}
	if len(lines) > 0 && lines[0][0] == "C" {
		return fmt.Sprintf("%s oracle create failed %v", cfg, lines[0])
	}
	li := 0
	next := func() []string {
		if li < len(lines) {
			li++
			return lines[li-1]
		}
		return nil
	}
	var hist []string
	frame := 0
	for _, o := range ops {
		switch o.kind {
		case 'c':
			c := chaosMSCTLs[o.ctl]
			got := c.set(ge, o.val)
			hist = append(hist, fmt.Sprintf("f%d:%s=%d", frame, c.name, o.val))
			f := next()
			if len(f) != 3 || f[0] != "K" {
				return fmt.Sprintf("%s oracle desync at ctl: %v", cfg, f)
			}
			if lr, _ := strconv.Atoi(f[2]); (lr == 0) != (got == nil) {
				return fmt.Sprintf("%s ctl %s=%d lib=%d go=%v hist=%v", cfg, c.name, o.val, lr, got, hist)
			}
		case 'r':
			ge.Reset()
			hist = append(hist, fmt.Sprintf("f%d:reset", frame))
		default:
			var gp []byte
			var gerr error
			if o.kind == 'i' {
				p16 := make([]int16, len(o.pcm))
				for i, v := range o.pcm {
					p16[i] = int16(max(-32768, min(32767, math.Floor(float64(v)*32768+0.5))))
				}
				gp, gerr = ge.Encode(p16, o.n)
			} else {
				gp, gerr = ge.EncodeFloat32(o.pcm, o.n)
			}
			f := next()
			if f == nil {
				return fmt.Sprintf("%s oracle output ended at frame %d", cfg, frame)
			}
			if f[0] == "E" {
				if gerr == nil {
					return fmt.Sprintf("%s frame %d n=%d: lib error %s, go ok hist=%v", cfg, frame, o.n, f[1], hist)
				}
				frame++
				continue
			}
			if gerr != nil {
				return fmt.Sprintf("%s frame %d n=%d: go %v, lib ok hist=%v", cfg, frame, o.n, gerr, hist)
			}
			lp, _ := hex.DecodeString(f[2])
			rf := next()
			if !bytes.Equal(lp, gp) {
				streams := 0
				if sd, ok := ge.(interface{ Streams() int }); ok {
					streams = sd.Streams()
				}
				ls, _, _ := splitMultistreamPackets(lp, streams, fs)
				gs, _, _ := splitMultistreamPackets(gp, streams, fs)
				var detail []string
				for i := range ls {
					if i < len(gs) {
						detail = append(detail, fmt.Sprintf("s%d lib=%d/%02x go=%d/%02x eq=%v", i, len(ls[i]), firstByte(ls[i]), len(gs[i]), firstByte(gs[i]), bytes.Equal(ls[i], gs[i])))
					}
				}
				return fmt.Sprintf("%s frame %d n=%d %c: packets differ (lib %d bytes, go %d bytes) streams=%v hist=%v", cfg, frame, o.n, o.kind, len(lp), len(gp), detail, hist)
			}
			if len(rf) == 2 && rf[1] != fmt.Sprintf("%08x", ge.FinalRange()) {
				return fmt.Sprintf("%s frame %d: final range lib=%s go=%08x hist=%v", cfg, frame, rf[1], ge.FinalRange(), hist)
			}
			frame++
		}
	}
	return ""
}
