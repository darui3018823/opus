package silk

import "testing"

// TestCLZ32MatchesShiftLoop checks silkCLZ32 against the shift loop it
// replaced.
func TestCLZ32MatchesShiftLoop(t *testing.T) {
	loop := func(a int32) int {
		u := uint32(a)
		if u == 0 {
			return 32
		}
		n := 0
		for u&0x80000000 == 0 {
			n++
			u <<= 1
		}
		return n
	}
	vals := []int32{0, 1, -1, 2, 3, 0x7fffffff, -0x80000000, 12345, -12345}
	for s := 0; s < 32; s++ {
		v := uint32(1) << s
		vals = append(vals, int32(v), int32(v-1), int32(v+1))
	}
	for _, v := range vals {
		if got, want := silkCLZ32(v), loop(v); got != want {
			t.Fatalf("silkCLZ32(%d) = %d, want %d", v, got, want)
		}
	}
}
