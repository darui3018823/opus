package kernels

import (
	"math/rand"
	"testing"
)

// fir8Reference is the scalar SILK iir_FIR interpolation step.
func fir8Reference(dst, buf []int16, table *FIR8Table, indexQ16, idxInc int32) {
	for i := range dst {
		row := &table[((indexQ16&0xFFFF)*12)>>16]
		bp := buf[indexQ16>>16:][:8]
		var res int32
		for k := 0; k < 8; k++ {
			res += int32(bp[k]) * int32(row[k])
		}
		v := int32((int64(res) + 1<<14) >> 15)
		switch {
		case v > 32767:
			v = 32767
		case v < -32768:
			v = -32768
		}
		dst[i] = int16(v)
		indexQ16 += idxInc
	}
}

func randomFIR8Table(r *rand.Rand, extreme bool) *FIR8Table {
	var t FIR8Table
	for i := range t {
		for k := range t[i] {
			if extreme {
				t[i][k] = [...]int16{-32768, 32767, -32768, 0}[r.Intn(4)]
			} else {
				t[i][k] = int16(r.Intn(65536) - 32768)
			}
		}
	}
	return &t
}

func randomInt16s(r *rand.Rand, n int, extreme bool) []int16 {
	s := make([]int16, n)
	for i := range s {
		if extreme {
			s[i] = [...]int16{-32768, 32767, -32768, -1, 1}[r.Intn(5)]
		} else {
			s[i] = int16(r.Intn(65536) - 32768)
		}
	}
	return s
}

func TestFIR8InterpMatchesReference(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		r := rand.New(rand.NewSource(10))
		steps := []int32{10923, 16384, 21846, 32768, 43690, 49152, 65535, 65536, 7, 1 << 17, 98304}
		for trial := 0; trial < 3000; trial++ {
			extreme := trial%3 == 0
			table := randomFIR8Table(r, extreme)
			idxInc := steps[r.Intn(len(steps))]
			start := int32(r.Intn(1 << 18))
			n := r.Intn(70)
			last := start + int32(max(n-1, 0))*idxInc
			buf := randomInt16s(r, int(last>>16)+8, extreme)
			want := make([]int16, n)
			fir8Reference(want, buf, table, start, idxInc)
			got := make([]int16, n+1)
			got[n] = 0x5a5a // guard
			done := FIR8Interp(got[:n], buf, table, start, idxInc)
			if done > n || done%4 != 0 {
				t.Fatalf("done = %d for n = %d", done, n)
			}
			fir8Reference(got[done:n], buf, table, start+int32(done)*idxInc, idxInc)
			for i := 0; i < n; i++ {
				if got[i] != want[i] {
					t.Fatalf("trial %d (inc %d start %d n %d) sample %d: got %d, want %d",
						trial, idxInc, start, n, i, got[i], want[i])
				}
			}
			if got[n] != 0x5a5a {
				t.Fatalf("trial %d: wrote past dst", trial)
			}
		}
	})
}

// TestFIR8InterpOverflowWraps pins the int32 wrap-around case: eight
// products of -32768*-32768 sum to 2^33, which wraps to 0 in int32 in both
// the scalar and the PMADDWD versions.
func TestFIR8InterpOverflowWraps(t *testing.T) {
	withPaths(t, func(t *testing.T) {
		var table FIR8Table
		for i := range table {
			for k := range table[i] {
				table[i][k] = -32768
			}
		}
		buf := make([]int16, 64)
		for i := range buf {
			buf[i] = -32768
		}
		got := make([]int16, 8)
		want := make([]int16, 8)
		FIR8Interp(got, buf, &table, 0, 21846)
		fir8Reference(want, buf, &table, 0, 21846)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("sample %d: got %d, want %d", i, got[i], want[i])
			}
		}
	})
}

func BenchmarkFIR8Interp(b *testing.B) {
	r := rand.New(rand.NewSource(11))
	table := randomFIR8Table(r, false)
	// 16 kHz -> 48 kHz, one 10 ms batch: 160 inputs, 480 outputs.
	const idxInc = 21846
	n := 480
	buf := randomInt16s(r, 2*160+8+8, false)
	dst := make([]int16, n)
	b.Run("generic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			fir8Reference(dst, buf, table, 0, idxInc)
		}
	})
	b.Run(Features(), func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			done := FIR8Interp(dst, buf, table, 0, idxInc)
			fir8Reference(dst[done:], buf, table, int32(done)*idxInc, idxInc)
		}
	})
}

func TestFIR8InterpLeavesUnsafeIndexesToCaller(t *testing.T) {
	var table FIR8Table
	buf := make([]int16, 64)
	dst := make([]int16, 8)
	if got := FIR8Interp(dst, buf, &table, -1, 1000); got != 0 {
		t.Errorf("negative start: computed %d samples", got)
	}
	if got := FIR8Interp(dst, buf, &table, 0, -1000); got != 0 {
		t.Errorf("negative step: computed %d samples", got)
	}
	if got := FIR8Interp(dst, buf, &table, 1<<31-10, 1<<20); got != 0 {
		t.Errorf("int32 overflow: computed %d samples", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("short buf did not panic")
		}
	}()
	FIR8Interp(dst, buf[:14], &table, 0, 1<<16) // needs buf[7+7]
}
