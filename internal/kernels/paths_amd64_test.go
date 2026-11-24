//go:build amd64 && !purego

package kernels

import "testing"

// disableAVXForTest reruns f with AVX off (SSE kernels) and with every
// kernel off (pure Go fallback through the public entry point).
func disableAVXForTest(t *testing.T, f func(t *testing.T)) {
	t.Helper()
	avx, sse, sse41 := useAVX, useSSE, useSSE41
	defer func() { useAVX, useSSE, useSSE41 = avx, sse, sse41 }()
	if avx {
		useAVX = false
		t.Run("sse", f)
	}
	useAVX, useSSE, useSSE41 = false, false, false
	t.Run("fallback", f)
}

func benchNarrowerPaths(b *testing.B, name string, f func(b *testing.B)) {
	if !useAVX {
		return
	}
	useAVX = false
	defer func() { useAVX = true }()
	b.Run(name+"/sse", f)
}
