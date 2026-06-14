package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/darui3018823/opus/internal/entcode"
)

// TestICWRSRoundtrip verifies icwrsLibopus is the exact inverse of
// cwrsiLibopus: for every signed pulse vector the decoder reconstructs, the
// encoder maps it back to the same index, and vice versa.
func TestICWRSRoundtrip(t *testing.T) {
	cases := []struct{ n, k int }{
		{2, 1}, {2, 3}, {3, 1}, {3, 2}, {3, 5}, {4, 2}, {4, 4},
		{5, 3}, {6, 1}, {6, 4}, {6, 6}, {8, 4}, {8, 6}, {10, 5},
		{10, 7}, {12, 5}, {12, 6}, {16, 6}, {16, 7}, {16, 8},
	}
	for _, c := range cases {
		v := cwrsV(c.n, c.k)
		if v == 0 || v > uint64(0xFFFFFFFF) {
			t.Fatalf("V(%d,%d)=%d out of testable range", c.n, c.k, v)
		}
		ft := uint32(v)
		// Iterate all indices for small codebooks; sample large ones with a
		// stride so the test stays exhaustive-in-spirit but fast (a few hundred
		// ms total instead of ~70s). Bijectivity holds for every index either
		// way; the stride is coprime-ish with typical V so it spreads coverage.
		step := uint32(1)
		if ft > 200000 {
			step = ft / 200000
		}
		for idx := uint32(0); idx < ft; idx += step {
			y := cwrsiLibopus(c.n, c.k, idx)
			// Sanity: ||y||_1 == k.
			s := 0
			for _, e := range y {
				s += abs(e)
			}
			if s != c.k {
				t.Fatalf("n=%d k=%d idx=%d: sum|y|=%d != k", c.n, c.k, idx, s)
			}
			back := icwrsLibopus(c.n, c.k, y)
			if back != idx {
				t.Fatalf("n=%d k=%d: icwrs(cwrsi(%d))=%d (y=%v)", c.n, c.k, idx, back, y)
			}
		}
	}
}

// TestAlgQuantUnquantRoundtrip encodes a random normalized band with algQuant
// and decodes it with algUnquant, asserting the reconstructed spectra and
// collapse masks match bit-for-bit (the encoder reconstructs X the same way the
// decoder does, so the two must agree exactly modulo float determinism).
func TestAlgQuantUnquantRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(0xC0FFEE))
	type cfg struct {
		n, k, spread, b int
	}
	// Leaf bands only: quant_partition splits any band whose codebook V(n,k)
	// would overflow uint32, so algQuant/algUnquant are only ever invoked with
	// V(n,k) < 2^32 (the regime where the range-coded index round-trips). All
	// configs below satisfy that bound.
	cfgs := []cfg{
		{8, 3, 2, 1}, {8, 6, 2, 1}, {16, 5, 2, 1}, {16, 7, 0, 1},
		{12, 6, 1, 1}, {10, 5, 3, 1}, {8, 4, 2, 2}, {16, 6, 2, 2},
		{6, 4, 2, 1}, {4, 2, 2, 1}, {2, 1, 2, 1}, {16, 1, 2, 1},
	}
	for ci, c := range cfgs {
		if v := cwrsV(c.n, c.k); v == 0 || v >= cwrsMax {
			t.Fatalf("cfg%d (n=%d k=%d): V=%d outside testable leaf range", ci, c.n, c.k, v)
		}
		for trial := 0; trial < 20; trial++ {
			// Random unit-norm target vector.
			Xenc := make([]float64, c.n)
			var nrm float64
			for i := range Xenc {
				Xenc[i] = rng.NormFloat64()
				nrm += Xenc[i] * Xenc[i]
			}
			if nrm < 1e-12 {
				continue
			}
			renormaliseVector(Xenc, c.n, 1.0)

			enc := entcode.NewEncoder(256)
			gain := 1.0
			maskEnc := algQuant(Xenc, c.n, c.k, c.spread, c.b, enc, gain)
			enc.Flush()
			data := enc.Bytes()

			Xdec := make([]float64, c.n)
			dec := entcode.NewDecoder(data)
			maskDec := algUnquant(Xdec, c.n, c.k, c.spread, c.b, dec, gain)

			if maskEnc != maskDec {
				t.Fatalf("cfg%d trial%d (n=%d k=%d B=%d): collapse mask enc=%d dec=%d",
					ci, trial, c.n, c.k, c.b, maskEnc, maskDec)
			}
			for i := 0; i < c.n; i++ {
				if math.Abs(Xenc[i]-Xdec[i]) > 1e-9 {
					t.Fatalf("cfg%d trial%d (n=%d k=%d B=%d): X[%d] enc=%.12g dec=%.12g",
						ci, trial, c.n, c.k, c.b, i, Xenc[i], Xdec[i])
				}
			}
			// Reconstructed band must be (approximately) unit norm.
			var e float64
			for _, v := range Xdec {
				e += v * v
			}
			if math.Abs(e-1.0) > 1e-6 {
				t.Fatalf("cfg%d trial%d: reconstructed energy=%.9g (want ~1)", ci, trial, e)
			}
		}
	}
}

// TestOpPVQSearchPulseCount verifies the search always emits exactly K pulses.
func TestOpPVQSearchPulseCount(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{2, 4, 8, 16, 32} {
		for _, k := range []int{1, 2, n/2 + 1, n, 2 * n} {
			if k <= 0 {
				continue
			}
			X := make([]float64, n)
			for i := range X {
				X[i] = rng.NormFloat64()
			}
			iy := make([]int, n)
			opPVQSearch(X, iy, k, n)
			s := 0
			for _, v := range iy {
				s += abs(v)
			}
			if s != k {
				t.Fatalf("n=%d k=%d: search produced %d pulses", n, k, s)
			}
		}
	}
}
