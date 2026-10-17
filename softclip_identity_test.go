package opus

import (
	"math"
	"testing"
)

// TestSoftClipIdentityFastPath checks the early return of SoftClipFloat32
// against the full non-linearity: with zero memories and every sample in
// [-1, 1] both must give the same samples and memories, bit for bit.
func TestSoftClipIdentityFastPath(t *testing.T) {
	seed := uint32(7)
	next := func() float32 {
		seed = seed*1664525 + 1013904223
		return float32(int32(seed)) / float32(1<<31)
	}
	for _, channels := range []int{1, 2, 3} {
		for trial := 0; trial < 200; trial++ {
			n := 1 + trial%97
			pcm := make([]float32, n*channels)
			for i := range pcm {
				pcm[i] = next() // [-1, 1)
			}
			pcm[0] = 1
			if n > 1 {
				pcm[len(pcm)-1] = -1
			}
			mem := make([]float32, channels)
			if trial%3 == 0 {
				mem[0] = float32(math.Copysign(0, -1)) // -0 behaves as zero
			}
			// Reference: the full non-linearity on copies.
			want := append([]float32(nil), pcm...)
			wantMem := append([]float32(nil), mem...)
			softClipFull(want, channels, n, wantMem)
			if err := SoftClipFloat32(pcm, channels, mem); err != nil {
				t.Fatal(err)
			}
			for i := range pcm {
				if math.Float32bits(pcm[i]) != math.Float32bits(want[i]) {
					t.Fatalf("channels %d trial %d: sample %d: fast %v, full %v", channels, trial, i, pcm[i], want[i])
				}
			}
			for c := range mem {
				if math.Float32bits(mem[c]) != math.Float32bits(wantMem[c]) {
					t.Fatalf("channels %d trial %d: mem[%d]: fast %v, full %v", channels, trial, c, mem[c], wantMem[c])
				}
			}
		}
	}
	// NaN and out-of-range samples keep the full path.
	if softClipIsIdentity([]float32{float32(math.NaN())}, []float32{0}) {
		t.Fatal("NaN must take the full path")
	}
	if softClipIsIdentity([]float32{1.0000001}, []float32{0}) || softClipIsIdentity([]float32{0.5}, []float32{0.1}) {
		t.Fatal("out-of-range sample or non-zero memory must take the full path")
	}
}
