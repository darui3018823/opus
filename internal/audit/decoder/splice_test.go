package audit

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	opus "github.com/darui3018823/opus"
)

func spliceFmt() int {
	if os.Getenv("SPLICEF32") != "" {
		return fmtF32
	}
	return fmtI16
}

// TestSplicedVectors decodes random sequences of valid packets drawn from
// all official vectors (arbitrary mode/bandwidth/frame-size/channel
// transitions), optionally with losses.
func TestSplicedVectors(t *testing.T) {
	var all [][]byte
	byVec := map[int][][]byte{}
	for v := 1; v <= 12; v++ {
		for _, p := range loadBit(t, v) {
			if len(p.data) > 0 {
				all = append(all, p.data)
				byVec[v] = append(byVec[v], p.data)
			}
		}
	}
	type variant struct {
		name string
		loss float64
		runs int // packets drawn consecutively from one vector
		pool []int
	}
	variants := []variant{
		{"rand-any", 0, 1, nil},
		{"runs8-any", 0, 8, nil},
		{"runs8-any-loss10", 0.10, 8, nil},
		{"silk-only-vecs", 0, 3, []int{2, 3, 4}},
		{"silk-only-vecs-loss15", 0.15, 3, []int{2, 3, 4}},
		{"hybrid-vecs", 0, 4, []int{5, 6, 12}},
		{"hybrid-vecs-loss15", 0.15, 4, []int{5, 6, 12}},
		{"nosilkvec-rand", 0, 1, []int{1, 5, 6, 7, 8, 9, 10, 11, 12}},
		{"celtonly-rand", 0, 1, []int{1, 7, 11}},
		{"celt+hyb-rand", 0, 1, []int{1, 5, 6, 7, 11}},
	}
	if f := os.Getenv("SPLICE"); f != "" {
		var keep []variant
		for _, v := range variants {
			if strings.HasPrefix(v.name, f) {
				keep = append(keep, v)
			}
		}
		variants = keep
	}
	for _, va := range variants {
		for _, o := range []struct{ rate, ch int }{{48000, 2}, {48000, 1}, {16000, 1}, {24000, 2}} {
			for seed := int64(1); seed <= 4; seed++ {
				t.Run(fmt.Sprintf("%s/%d/%d/s%d", va.name, o.rate, o.ch, seed), func(t *testing.T) {
					t.Parallel()
					rng := rand.New(rand.NewSource(seed))
					cfg, g := newSingle(t, o.rate, o.ch)
					var ops []op
					last := o.rate / 50
					for len(ops) < 1500 {
						var src [][]byte
						if va.pool == nil {
							src = byVec[1+rng.Intn(12)]
						} else {
							src = byVec[va.pool[rng.Intn(len(va.pool))]]
						}
						start := rng.Intn(len(src))
						for k := 0; k < va.runs && start+k < len(src); k++ {
							p := src[start+k]
							if va.loss > 0 && rng.Float64() < va.loss {
								ops = append(ops, op{code: 1, fmt: spliceFmt(), frameSize: last})
								continue
							}
							n, _ := opus.PacketGetNumSamples(p, o.rate)
							last = n
							ops = append(ops, op{code: 1, fmt: spliceFmt(), frameSize: 5760 * o.rate / 48000, data: p})
						}
					}
					_ = all
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
