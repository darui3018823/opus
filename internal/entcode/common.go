// Package entcode provides entropy coding (range coding) for Opus.
// This implementation is based on the range coder used in libopus.
package entcode

// Range coding constants
const (
	// WindowSize is the number of bits in the range coder window
	WindowSize = 32

	// CodeBits is the number of bits used for range coding
	CodeBits = 32

	// CodeShift shifts bits for range coding
	CodeShift = CodeBits - WindowSize

	// CodeTop is the top of the range coder range
	CodeTop = uint32(1 << (CodeBits - 1))

	// CodeBottom is the minimum range size
	CodeBottom = CodeTop >> 8
)

// Common range coder utilities

// ICdf represents an inverse cumulative distribution function.
// Used for entropy coding with known probability distributions.
type ICdf []uint16

// NewUniformICdf creates a uniform distribution ICDF with n symbols.
func NewUniformICdf(n int) ICdf {
	icdf := make(ICdf, n+1)
	step := uint32(65536) / uint32(n)
	for i := 0; i < n; i++ {
		icdf[i] = uint16(uint32(i) * step)
	}
	icdf[n] = 0 // Sentinel
	return icdf
}

// BitProb represents a probability for binary symbols (0-32768 scale).
type BitProb uint16

// Log2Ceiling computes the ceiling of log2(n).
func Log2Ceiling(n int) int {
	if n <= 0 {
		return 0
	}
	log := 0
	n--
	for n > 0 {
		n >>= 1
		log++
	}
	return log
}

// ILog computes integer log base 2 (floor).
func ILog(val uint32) int {
	if val == 0 {
		return 0
	}
	log := 0
	for val > 0 {
		val >>= 1
		log++
	}
	return log - 1
}
