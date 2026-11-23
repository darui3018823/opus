//go:build !amd64 || purego

package kernels

import "testing"

func disableAVXForTest(t *testing.T, f func(t *testing.T)) {}

func benchNarrowerPaths(b *testing.B, name string, f func(b *testing.B)) {}
