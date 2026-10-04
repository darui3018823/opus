package audit

import (
	"fmt"
	"math/rand"
	"testing"
)

// mutate returns a malformed / unusual variant of p.
func mutate(rng *rand.Rand, p []byte) []byte {
	q := append([]byte(nil), p...)
	switch rng.Intn(10) {
	case 0: // random bytes
		n := rng.Intn(64)
		q = make([]byte, n)
		rng.Read(q)
	case 1: // truncate
		if len(q) > 1 {
			q = q[:rng.Intn(len(q))]
		}
	case 2: // flip TOC count code
		if len(q) > 0 {
			q[0] = q[0]&^3 | byte(rng.Intn(4))
		}
	case 3: // random TOC
		if len(q) > 0 {
			q[0] = byte(rng.Intn(256))
		}
	case 4: // code 3 header with random frame count / flags
		if len(q) > 1 {
			q[0] |= 3
			q[1] = byte(rng.Intn(256))
		}
	case 5: // bit flips in payload
		for k := 0; k < 1+rng.Intn(8) && len(q) > 1; k++ {
			q[1+rng.Intn(len(q)-1)] ^= 1 << uint(rng.Intn(8))
		}
	case 6: // TOC only
		if len(q) > 0 {
			q = q[:1]
		}
	case 7: // append garbage
		extra := make([]byte, rng.Intn(40))
		rng.Read(extra)
		q = append(q, extra...)
	case 8: // code 2 with random first length
		if len(q) > 2 {
			q[0] = q[0]&^3 | 2
			q[1] = byte(rng.Intn(256))
		}
	case 9: // code 3 padding
		if len(q) > 2 {
			q[0] |= 3
			q[1] = byte(rng.Intn(64)) | 0x40
			q[2] = byte(rng.Intn(256))
		}
	}
	return q
}

func TestMalformedPackets(t *testing.T) {
	var valid [][]byte
	for v := 1; v <= 12; v++ {
		for _, p := range loadBit(t, v) {
			if len(p.data) > 0 {
				valid = append(valid, p.data)
			}
		}
	}
	for _, o := range []struct{ rate, ch int }{{48000, 2}, {48000, 1}, {16000, 1}, {8000, 2}} {
		for seed := int64(1); seed <= 6; seed++ {
			for _, f := range []int{fmtI16, fmtF32} {
				t.Run(fmt.Sprintf("%d/%d/s%d/f%d", o.rate, o.ch, seed, f), func(t *testing.T) {
					t.Parallel()
					rng := rand.New(rand.NewSource(seed))
					cfg, g := newSingle(t, o.rate, o.ch)
					var ops []op
					start := rng.Intn(len(valid) - 3000)
					for i := 0; i < 3000; i++ {
						p := valid[start+i]
						if rng.Float64() < 0.3 {
							p = mutate(rng, p)
						}
						if len(p) == 0 {
							p = []byte{} // len 0 non-NULL: PLC in libopus
						}
						ops = append(ops, op{code: 1, fmt: f, frameSize: 5760 * o.rate / 48000, data: p})
					}
					st := compare(t, cfg, ops, g, cmpOpts{})
					t.Logf("RESULT %s %s %v", t.Name(), st, st.notes)
					if !st.exact() {
						t.Errorf("DIFF")
					}
				})
			}
		}
	}
}

// TestFrameSizeArgs: PLC / FEC / decode frame-size arguments and returned
// counts/errors.
func TestFrameSizeArgs(t *testing.T) {
	pk := loadBit(t, 5) // hybrid
	pk3 := loadBit(t, 3)
	rate, ch := 48000, 1
	sizes := []int{0, -5, 1, 119, 120, 121, 240, 360, 480, 600, 960, 1440, 1920, 2880, 4800, 5760, 5880, 11520}
	var rows []string
	for _, src := range [][]bitPacket{pk, pk3} {
		for _, fs := range sizes {
			for _, kind := range []string{"decode", "plc", "fec"} {
				cfg, g := newSingle(t, rate, ch)
				ops := []op{{code: 1, frameSize: 5760, data: src[10].data}}
				switch kind {
				case "decode":
					ops = append(ops, op{code: 1, frameSize: fs, data: src[11].data})
				case "plc":
					ops = append(ops, op{code: 1, frameSize: fs})
				case "fec":
					ops = append(ops, op{code: 1, fec: true, frameSize: fs, data: src[11].data})
				}
				ops = append(ops, op{code: 1, frameSize: 5760, data: src[12].data})
				ref := runOracle(t, cfg, ops)
				var got []result
				var errs []error
				for _, o := range ops {
					r, err := g.run(o, ch)
					got = append(got, r)
					errs = append(errs, err)
				}
				same := got[1].ret == ref[1].ret
				// compare follow-up packet
				follow := len(got[2].i16) == len(ref[2].i16)
				for k := 0; follow && k < len(ref[2].i16); k++ {
					if got[2].i16[k] != ref[2].i16[k] {
						follow = false
					}
				}
				if !same || !follow {
					rows = append(rows, fmt.Sprintf("%s fs=%d go=%d(%v) lib=%d followupExact=%v", kind, fs, got[1].ret, errs[1], ref[1].ret, follow))
				}
			}
		}
	}
	for _, r := range rows {
		t.Logf("MISMATCH %s", r)
	}
}
