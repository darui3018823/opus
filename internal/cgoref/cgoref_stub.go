//go:build !opusref

// Package cgoref is the libopus CGO reference wrapper. The real implementation
// is built only under the `opusref` build tag (it needs a C toolchain and
// libopus). Without that tag the package is intentionally empty so that
// `go build ./...`, `go vet ./...`, and `go test ./...` work with no native
// dependency. Run the reference comparison with `go test -tags opusref`.
package cgoref
