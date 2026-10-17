package entcode

import "testing"

// TestILogMatchesShiftLoop checks the bits.Len forms against the shift loops
// they replaced.
func TestILogMatchesShiftLoop(t *testing.T) {
	loopLen := func(v uint64) int {
		n := 0
		for v > 0 {
			v >>= 1
			n++
		}
		return n
	}
	vals := []uint32{0, 1, 2, 3, 4, 5, 7, 8, 255, 256, 65535, 65536, 1<<31 - 1, 1 << 31, 1<<32 - 1}
	for s := 0; s < 32; s++ {
		vals = append(vals, 1<<s-1, 1<<s, 1<<s+1)
	}
	for _, v := range vals {
		if got, want := ILog(v), loopLen(uint64(v)); got != want {
			t.Fatalf("ILog(%d) = %d, want %d", v, got, want)
		}
	}
	for n := -3; n < 1<<17; n++ {
		want := 0
		if n > 1 {
			want = loopLen(uint64(n - 1))
		}
		if got := Log2Ceiling(n); got != want {
			t.Fatalf("Log2Ceiling(%d) = %d, want %d", n, got, want)
		}
	}
}
