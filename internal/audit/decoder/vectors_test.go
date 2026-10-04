package audit

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	opus "github.com/darui3018823/opus"
)

var root = filepath.Join("..", "..", "..", "testdata")

type bitPacket struct {
	data []byte
	rng  uint32
}

func loadBit(t testing.TB, v int) []bitPacket {
	data, err := os.ReadFile(filepath.Join(root, "opus_newvectors", fmt.Sprintf("testvector%02d.bit", v)))
	if err != nil {
		t.Skip(err)
	}
	var out []bitPacket
	for pos := 0; pos+8 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[pos:]))
		rng := binary.BigEndian.Uint32(data[pos+4:])
		pos += 8
		out = append(out, bitPacket{data[pos : pos+n], rng})
		pos += n
	}
	return out
}

func newSingle(t testing.TB, rate, ch int) (decCfg, goDecoder) {
	d, err := opus.NewDecoder(rate, ch)
	if err != nil {
		t.Fatal(err)
	}
	return decCfg{kind: 0, rate: rate, ch: ch}, singleDec{d}
}

var allOutputs = []struct{ rate, ch int }{{48000, 2}, {48000, 1}, {24000, 2}, {24000, 1}, {16000, 2}, {16000, 1}, {12000, 2}, {12000, 1}, {8000, 2}, {8000, 1}}

// TestVectorsAllOutputs: plain vector decode, int16, all 10 output configs.
func TestVectorsAllOutputs(t *testing.T) {
	for _, f := range []int{fmtI16, fmtF32, fmtI24} {
		for v := 1; v <= 12; v++ {
			pk := loadBit(t, v)
			for _, o := range allOutputs {
				if f != fmtI16 && o.rate != 48000 && o.rate != 16000 {
					continue
				}
				t.Run(fmt.Sprintf("f%d/tv%02d/%d/%d", f, v, o.rate, o.ch), func(t *testing.T) {
					t.Parallel()
					cfg, g := newSingle(t, o.rate, o.ch)
					var ops []op
					last := o.rate / 50
					for _, p := range pk {
						if len(p.data) == 0 {
							ops = append(ops, op{code: 1, fmt: f, frameSize: last})
							continue
						}
						n, _ := opus.PacketGetNumSamples(p.data, o.rate)
						last = n
						ops = append(ops, op{code: 1, fmt: f, frameSize: 5760 * o.rate / 48000, data: p.data})
					}
					st := compare(t, cfg, ops, g, cmpOpts{checkDur: true, checkBW: true, checkPitch: true})
					t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
					if !st.exact() || st.durMis+st.bwMis+st.pitchMis > 0 {
						t.Errorf("DIFF")
					}
				})
			}
		}
	}
}

// TestVectorsRandomLoss applies seeded random/burst loss to the vectors.
func TestVectorsRandomLoss(t *testing.T) {
	type lossCfg struct {
		name  string
		p     float64
		burst int
	}
	losses := []lossCfg{{"rand10", 0.10, 1}, {"rand30", 0.30, 1}, {"burst8", 0.04, 8}, {"burst25", 0.01, 25}}
	for _, f := range []int{fmtI16, fmtF32} {
		for v := 1; v <= 12; v++ {
			pk := loadBit(t, v)
			for _, o := range allOutputs {
				if f == fmtF32 && o.ch == 1 && o.rate != 48000 {
					continue
				}
				for li, l := range losses {
					t.Run(fmt.Sprintf("f%d/tv%02d/%d/%d/%s", f, v, o.rate, o.ch, l.name), func(t *testing.T) {
						t.Parallel()
						rng := rand.New(rand.NewSource(int64(v*1000 + li)))
						cfg, g := newSingle(t, o.rate, o.ch)
						var ops []op
						last := o.rate / 50
						lostRun := 0
						for _, p := range pk {
							if lostRun == 0 && rng.Float64() < l.p {
								lostRun = l.burst
							}
							if len(p.data) == 0 || lostRun > 0 {
								if lostRun > 0 {
									lostRun--
								}
								ops = append(ops, op{code: 1, fmt: f, frameSize: last})
								continue
							}
							n, _ := opus.PacketGetNumSamples(p.data, o.rate)
							last = n
							ops = append(ops, op{code: 1, fmt: f, frameSize: 5760 * o.rate / 48000, data: p.data})
						}
						st := compare(t, cfg, ops, g, cmpOpts{checkDur: true})
						t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
						if !st.exact() || st.durMis > 0 {
							t.Errorf("DIFF")
						}
					})
				}
			}
		}
	}
}

// TestVectorsOddPLCDurations: PLC sizes not equal to the packet duration.
func TestVectorsOddPLCDurations(t *testing.T) {
	plcMs := []float64{2.5, 5, 10, 20, 40, 60, 120}
	for v := 1; v <= 12; v++ {
		pk := loadBit(t, v)
		for _, o := range []struct{ rate, ch int }{{48000, 2}, {48000, 1}, {16000, 1}, {8000, 2}} {
			for _, ms := range plcMs {
				t.Run(fmt.Sprintf("tv%02d/%d/%d/plc%gms", v, o.rate, o.ch, ms), func(t *testing.T) {
					t.Parallel()
					rng := rand.New(rand.NewSource(int64(v)))
					cfg, g := newSingle(t, o.rate, o.ch)
					fs := int(math.Round(float64(o.rate) * ms / 1000))
					var ops []op
					for _, p := range pk {
						if len(p.data) == 0 || rng.Float64() < 0.08 {
							ops = append(ops, op{code: 1, fmt: fmtI16, frameSize: fs})
							continue
						}
						ops = append(ops, op{code: 1, fmt: fmtI16, frameSize: 5760 * o.rate / 48000, data: p.data})
					}
					st := compare(t, cfg, ops, g, cmpOpts{checkDur: true})
					t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
					if !st.exact() || st.durMis > 0 {
						t.Errorf("DIFF")
					}
				})
			}
		}
	}
}

// TestVectorsResetGainPhase: mid-stream reset, decode gain, phase inversion disabled.
func TestVectorsResetGainPhase(t *testing.T) {
	type variant struct {
		name  string
		setup []op
		reset int
	}
	variants := []variant{
		{"reset", nil, 37},
		{"gain+6dB", []op{{code: 3, val: 1536}}, 0},
		{"gain-12dB", []op{{code: 3, val: -3072}}, 0},
		{"gain+0.4dB", []op{{code: 3, val: 100}}, 0},
		{"gainmax", []op{{code: 3, val: 32767}}, 0},
		{"phaseinv", []op{{code: 4, val: 1}}, 0},
	}
	for _, f := range []int{fmtI16, fmtF32, fmtI24} {
		for v := 1; v <= 12; v++ {
			pk := loadBit(t, v)
			for _, o := range []struct{ rate, ch int }{{48000, 2}, {48000, 1}, {16000, 2}} {
				for _, va := range variants {
					t.Run(fmt.Sprintf("f%d/tv%02d/%d/%d/%s", f, v, o.rate, o.ch, va.name), func(t *testing.T) {
						t.Parallel()
						cfg, g := newSingle(t, o.rate, o.ch)
						ops := append([]op(nil), va.setup...)
						last := o.rate / 50
						for i, p := range pk {
							if va.reset > 0 && i > 0 && i%va.reset == 0 {
								ops = append(ops, op{code: 2})
							}
							if len(p.data) == 0 {
								ops = append(ops, op{code: 1, fmt: f, frameSize: last})
								continue
							}
							n, _ := opus.PacketGetNumSamples(p.data, o.rate)
							last = n
							ops = append(ops, op{code: 1, fmt: f, frameSize: 5760 * o.rate / 48000, data: p.data})
						}
						st := compare(t, cfg, ops, g, cmpOpts{checkDur: true, checkBW: true})
						t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
						if !st.exact() || st.durMis+st.bwMis > 0 {
							t.Errorf("DIFF")
						}
					})
				}
			}
		}
	}
}
