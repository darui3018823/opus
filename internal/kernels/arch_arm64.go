//go:build arm64 && go1.27 && !purego

package kernels

// NEON (Advanced SIMD) is mandatory on arm64. The kernels need Go 1.27's
// assembler, which added the vector floating-point instructions; older
// toolchains build the pure Go references (arch_generic.go). Advanced SIMD
// honours FPCR like the scalar unit, so subnormals and rounding match.
var useNEON = true

// Features reports the accelerated instruction sets in use.
func Features() string {
	if useNEON {
		return "neon"
	}
	return "generic"
}
