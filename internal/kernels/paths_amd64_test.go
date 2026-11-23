//go:build amd64 && !purego

package kernels

import "testing"

// disableAVXForTest reruns f with AVX off (SSE kernels) and with every
// kernel off (pure Go fallback through the public entry point).
func disableAVXForTest(t *testing.T, f func(t *testing.T)) {
	t.Helper()
	avx, sse := useAVX, useSSE
	defer func() { useAVX, useSSE = avx, sse }()
	if avx {
		useAVX = false
		t.Run("sse", f)
	}
	useAVX, useSSE = false, false
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
