package audit

import (
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestReproPacketSeq decodes the hex packets in PKTS (comma separated) on a
// fresh decoder (RATE, CH, int16) and compares each with libopus.
func TestReproPacketSeq(t *testing.T) {
	list := os.Getenv("PKTS")
	if list == "" {
		t.Skip()
	}
	rate, _ := strconv.Atoi(os.Getenv("RATE"))
	ch, _ := strconv.Atoi(os.Getenv("CH"))
	if rate == 0 {
		rate = 48000
	}
	if ch == 0 {
		ch = 1
	}
	var ops []op
	for _, h := range strings.Split(list, ",") {
		p, err := hex.DecodeString(h)
		if err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op{code: 1, fmt: fmtF32, frameSize: 5760 * rate / 48000, data: p})
	}
	cfg, g := newSingle(t, rate, ch)
	ref := runOracle(t, cfg, ops)
	for i, o := range ops {
		got, err := g.run(o, ch)
		eq, first := 0, -1
		for k := range ref[i].f32 {
			if k < len(got.f32) && got.f32[k] == ref[i].f32[k] {
				eq++
			} else if first < 0 {
				first = k
			}
		}
		t.Logf("pkt%d ret go=%d lib=%d err=%v rngEq=%v eq=%d/%d first=%d", i, got.ret, ref[i].ret, err, got.rng == ref[i].rng, eq, len(ref[i].f32), first)
		if first >= 0 {
			for k := first; k < first+4 && k < len(got.f32); k++ {
				t.Logf("  k=%d go=%.9g lib=%.9g d=%.3g", k, got.f32[k], ref[i].f32[k], got.f32[k]-ref[i].f32[k])
			}
		}
	}
}
