package celt

import "testing"

// TestPVQUTableMatchesRecurrence checks every table entry against the
// memoised recurrence the table replaces.
func TestPVQUTableMatchesRecurrence(t *testing.T) {
	var ref func(n, k int) uint64
	memo := map[[2]int]uint64{}
	ref = func(n, k int) uint64 {
		if n < 0 || k < 0 {
			return 0
		}
		if n > k {
			n, k = k, n
		}
		if n == 0 {
			if k == 0 {
				return 1
			}
			return 0
		}
		if v, ok := memo[[2]int{n, k}]; ok {
			return v
		}
		a, b, c := ref(n-1, k), ref(n, k-1), ref(n-1, k-1)
		v := cwrsMax
		if a <= cwrsMax-b && a+b <= cwrsMax-c {
			v = a + b + c
		}
		memo[[2]int{n, k}] = v
		return v
	}
	for n := 0; n < pvqUTableSmall+4; n++ {
		for k := 0; k < pvqUTableLarge+4; k++ {
			if got, want := celtPVQU(n, k), ref(n, k); got != want {
				t.Fatalf("celtPVQU(%d,%d) = %d, want %d", n, k, got, want)
			}
			if got, want := celtPVQU(k, n), ref(n, k); got != want {
				t.Fatalf("celtPVQU(%d,%d) = %d, want %d", k, n, got, want)
			}
		}
	}
}
