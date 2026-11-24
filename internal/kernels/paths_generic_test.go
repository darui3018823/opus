//go:build !(amd64 || (arm64 && go1.27)) || purego

package kernels

import "testing"

func disableAVXForTest(t *testing.T, f func(t *testing.T)) {}

func benchNarrowerPaths(b *testing.B, name string, f func(b *testing.B)) {}
