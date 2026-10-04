package audit

import (
	"math"
	"os"
	"strconv"
	"testing"

	opus "github.com/darui3018823/opus"
)

func TestDiagFloatOps(t *testing.T) {
	v, _ := strconv.Atoi(os.Getenv("TV"))
	if v == 0 {
		t.Skip()
	}
	rate, ch := 48000, 2
	if r, _ := strconv.Atoi(os.Getenv("RATE")); r != 0 {
		rate = r
	}
	if c, _ := strconv.Atoi(os.Getenv("CH")); c != 0 {
		ch = c
	}
	f := fmtF32
	if os.Getenv("F24") != "" {
		f = fmtI24
	}
	pk := loadBit(t, v)
	cfg, g := newSingle(t, rate, ch)
	var ops []op
	for _, p := range pk {
		ops = append(ops, op{code: 1, fmt: f, frameSize: 5760, data: p.data})
	}
	ref := runOracle(t, cfg, ops)
	prev := 0
	shown := 0
	for i, o := range ops {
		got, _ := g.run(o, ch)
		info, _ := opus.InspectPacket(o.data, rate)
		diff, maxAbs, maxRel, maxAmp := 0, 0.0, 0.0, 0.0
		first := -1
		var a, b []float64
		if f == fmtF32 {
			for k := range ref[i].f32 {
				a = append(a, float64(got.f32[k]))
				b = append(b, float64(ref[i].f32[k]))
			}
		} else {
			for k := range ref[i].i24 {
				a = append(a, float64(got.i24[k]))
				b = append(b, float64(ref[i].i24[k]))
			}
		}
		for k := range b {
			if a[k] != b[k] {
				diff++
				d := math.Abs(a[k] - b[k])
				maxAbs = math.Max(maxAbs, d)
				maxRel = math.Max(maxRel, d/math.Max(1e-30, math.Abs(b[k])))
				maxAmp = math.Max(maxAmp, math.Abs(b[k]))
				if first < 0 {
					first = k
					if shown < 8 {
						t.Logf("   first go=%.10g lib=%.10g", a[k], b[k])
					}
				}
			}
		}
		if diff > 0 && shown < 40 {
			shown++
			t.Logf("op%d mode=%d prev=%d cfg=%d spf=%d fc=%d len=%d diff=%d/%d first=%d maxAbs=%.3g maxRel=%.3g maxAmpAtDiff=%.3g", i, info.Mode, prev, info.Config, info.SamplesPerFrame, info.FrameCount, len(o.data), diff, len(b), first, maxAbs, maxRel, maxAmp)
		}
		prev = info.Mode
	}
}
