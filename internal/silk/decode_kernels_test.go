package silk

import (
	"math/rand"
	"slices"
	"testing"
)

// iirFIRInterpolReference is the per-sample append loop iirFIRInterpol
// replaced; the rewrite must produce the same samples.
func iirFIRInterpolReference(out []int16, buf []int16, maxIndexQ16, idxInc int32) []int16 {
	for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += idxInc {
		tableIndex := silkSMULWB(indexQ16&0xFFFF, 12)
		bp := buf[indexQ16>>16:]
		res := silkSMULBB(int32(bp[0]), int32(silkResamplerFracFIR12[tableIndex][0]))
		res = silkSMLABB(res, int32(bp[1]), int32(silkResamplerFracFIR12[tableIndex][1]))
		res = silkSMLABB(res, int32(bp[2]), int32(silkResamplerFracFIR12[tableIndex][2]))
		res = silkSMLABB(res, int32(bp[3]), int32(silkResamplerFracFIR12[tableIndex][3]))
		res = silkSMLABB(res, int32(bp[4]), int32(silkResamplerFracFIR12[11-tableIndex][3]))
		res = silkSMLABB(res, int32(bp[5]), int32(silkResamplerFracFIR12[11-tableIndex][2]))
		res = silkSMLABB(res, int32(bp[6]), int32(silkResamplerFracFIR12[11-tableIndex][1]))
		res = silkSMLABB(res, int32(bp[7]), int32(silkResamplerFracFIR12[11-tableIndex][0]))
		out = append(out, silkSAT16(rshiftRound(int64(res), 15)))
	}
	return out
}

func TestIIRFIRInterpolMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	// invRatioQ16 for the IIR_FIR up-sampling cases (8/12/16 kHz to
	// 24/48 kHz and 12->16 kHz) plus a few arbitrary steps.
	steps := []int32{10923, 16384, 21846, 32768, 43690, 49152, 65535, 7, 1 << 17}
	for _, idxInc := range steps {
		for _, nIn := range []int{0, 1, 2, 7, 80, 120, 160} {
			maxIndexQ16 := int32(nIn) << 17
			buf := make([]int16, 2*nIn+resamplerOrderFIR12+8)
			for i := range buf {
				buf[i] = int16(rng.Intn(65536) - 32768)
			}
			prefix := []int16{1, -2, 3}
			want := iirFIRInterpolReference(slices.Clone(prefix), buf, maxIndexQ16, idxInc)
			got := iirFIRInterpol(slices.Clone(prefix), buf, maxIndexQ16, idxInc)
			if !slices.Equal(got, want) {
				t.Fatalf("idxInc=%d nIn=%d: got %d samples, want %d (first diff at %d)",
					idxInc, nIn, len(got), len(want), firstDiff(got, want))
			}
		}
	}
}

func TestLPCPredictQ10MatchesLoop(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, order := range []int{10, 16, 12, 6} {
		for trial := 0; trial < 2000; trial++ {
			var hist [silkMaxLPCOrder]int32
			for i := range hist {
				hist[i] = int32(rng.Uint32())
			}
			a := make([]int16, order)
			for i := range a {
				a[i] = int16(rng.Intn(65536) - 32768)
			}
			want := int32(order >> 1)
			for j := 0; j < order; j++ {
				want = silkSMLAWB(want, hist[silkMaxLPCOrder-1-j], a[j])
			}
			if got := silkLPCPredictQ10(&hist, a, order); got != want {
				t.Fatalf("order=%d trial=%d: got %d, want %d", order, trial, got, want)
			}
		}
	}
}

func firstDiff(a, b []int16) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}
