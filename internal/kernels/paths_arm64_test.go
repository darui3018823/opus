//go:build arm64 && go1.27 && !purego

package kernels

import "testing"

// disableAVXForTest reruns f with the NEON kernels off (pure Go fallback
// through the public entry points).
func disableAVXForTest(t *testing.T, f func(t *testing.T)) {
	t.Helper()
	neon := useNEON
	defer func() { useNEON = neon }()
	useNEON = false
	t.Run("fallback", f)
}

func benchNarrowerPaths(b *testing.B, name string, f func(b *testing.B)) {}
