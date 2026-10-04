package audit

import (
	"math"
	"math/rand"
	"os"
	"strconv"
	"testing"

	opus "github.com/darui3018823/opus"
)

// buildLossOps mirrors TestVectorsRandomLoss.
func buildLossOps(pk []bitPacket, rate, f int, p float64, burst int, seed int64) ([]op, []int) {
	rng := rand.New(rand.NewSource(seed))
	var ops []op
	var src []int
	last := rate / 50
	lostRun := 0
	for i, pp := range pk {
		if lostRun == 0 && rng.Float64() < p {
			lostRun = burst
		}
		if len(pp.data) == 0 || lostRun > 0 {
			if lostRun > 0 {
				lostRun--
			}
			ops = append(ops, op{code: 1, fmt: f, frameSize: last})
			src = append(src, i)
			continue
		}
		n, _ := opus.PacketGetNumSamples(pp.data, rate)
		last = n
		ops = append(ops, op{code: 1, fmt: f, frameSize: 5760 * rate / 48000, data: pp.data})
		src = append(src, i)
	}
	return ops, src
}

func TestDiagLossAll(t *testing.T) {
	if os.Getenv("DIAGLOSS") == "" {
		t.Skip()
	}
	for _, c := range [][4]int{{12, 48000, 2, 2}, {1, 48000, 2, 0}, {10, 48000, 2, 0}, {7, 48000, 2, 3}, {8, 48000, 2, 1}, {9, 48000, 2, 1}, {1, 48000, 2, 2}, {8, 48000, 2, 3}} {
		t.Logf("==== tv%d rate%d ch%d li%d", c[0], c[1], c[2], c[3])
		diagLoss(t, c[0], c[1], c[2], c[3])
	}
}

func TestDiagLoss(t *testing.T) {
	v, _ := strconv.Atoi(os.Getenv("TV"))
	if v == 0 {
		t.Skip()
	}
	rate, _ := strconv.Atoi(os.Getenv("RATE"))
	ch, _ := strconv.Atoi(os.Getenv("CH"))
	li, _ := strconv.Atoi(os.Getenv("LI"))
	diagLoss(t, v, rate, ch, li)
}

func diagLoss(t *testing.T, v, rate, ch, li int) {
	ps := []float64{0.10, 0.30, 0.04, 0.01}
	bs := []int{1, 1, 8, 25}
	pk := loadBit(t, v)
	ops, _ := buildLossOps(pk, rate, fmtI16, ps[li], bs[li], int64(v*1000+li))
	if os.Getenv("F32") != "" {
		for k := range ops {
			ops[k].fmt = fmtF32
		}
	}
	if ms, _ := strconv.ParseFloat(os.Getenv("PLCMS"), 64); ms > 0 {
		rng := rand.New(rand.NewSource(int64(v)))
		fs := int(math.Round(float64(rate) * ms / 1000))
		ops = ops[:0]
		for _, p := range pk {
			if len(p.data) == 0 || rng.Float64() < 0.08 {
				ops = append(ops, op{code: 1, fmt: fmtI16, frameSize: fs})
				continue
			}
			ops = append(ops, op{code: 1, fmt: fmtI16, frameSize: 5760 * rate / 48000, data: p.data})
		}
	}
	cfg, g := newSingle(t, rate, ch)
	ref := runOracle(t, cfg, ops)
	shown := 0
	lastMode := 0
	run := 0
	for i, o := range ops {
		if o.data == nil {
			run++
		} else {
			run = 0
		}
		got, err := g.run(o, ch)
		mode, cfgn, spf := 0, 0, 0
		if o.data != nil {
			info, _ := opus.InspectPacket(o.data, rate)
			mode, cfgn, spf = info.Mode, info.Config, info.SamplesPerFrame
		}
		diff, maxAbs, first := 0, 0.0, -1
		for k := range ref[i].i16 {
			if k < len(got.i16) && got.i16[k] != ref[i].i16[k] {
				diff++
				maxAbs = math.Max(maxAbs, math.Abs(float64(got.i16[k])-float64(ref[i].i16[k])))
				if first < 0 {
					first = k
				}
			}
		}
		for k := range ref[i].f32 {
			if k < len(got.f32) && got.f32[k] != ref[i].f32[k] {
				diff++
				maxAbs = math.Max(maxAbs, math.Abs(float64(got.f32[k])-float64(ref[i].f32[k])))
				if first < 0 {
					first = k
					t.Logf("   op%d first f32 go=%.9g lib=%.9g", i, got.f32[k], ref[i].f32[k])
				}
			}
		}
		lo, _ := strconv.Atoi(os.Getenv("LO"))
		hi, _ := strconv.Atoi(os.Getenv("HI"))
		if ((diff > 0 || got.ret != ref[i].ret) && shown < 12) || (i >= lo && i < hi) {
			shown++
			t.Logf("op%d run=%d lost=%v mode=%d cfg=%d spf=%d lastDecodedMode=%d fs=%d ret go=%d lib=%d err=%v diff=%d first=%d maxAbs=%g", i, run, o.data == nil, mode, cfgn, spf, lastMode, o.frameSize, got.ret, ref[i].ret, err, diff, first/ch, maxAbs)
		}
		if o.data != nil {
			lastMode = mode*100 + cfgn
		}
	}
}
