// Package entcode provides entropy coding (range coding) for Opus,
// bit-exact with the range coder in libopus 1.3.1 (celt/entcode.c,
// celt/entenc.c, celt/entdec.c).
package entcode

// Range coding constants matching libopus.
const (
	// EC_SYM_BITS: number of bits per symbol output.
	SymBits = 8

	// EC_CODE_BITS: total bits in the code register.
	CodeBits = 32

	// EC_SYM_MAX: (1<<EC_SYM_BITS)-1 = 255.
	SymMax = (1 << SymBits) - 1

	// EC_CODE_SHIFT: EC_CODE_BITS - EC_SYM_BITS - 1 = 23.
	CodeShift = CodeBits - SymBits - 1

	// EC_CODE_TOP: 1<<(EC_CODE_BITS-1) = 0x80000000.
	CodeTop = uint32(1 << (CodeBits - 1))

	// EC_CODE_BOT: EC_CODE_TOP >> EC_SYM_BITS = 0x00800000.
	CodeBot = CodeTop >> SymBits

	// EC_CODE_EXTRA: (EC_CODE_BITS-2)%EC_SYM_BITS+1 = 7.
	CodeExtra = (CodeBits-2)%SymBits + 1

	// EC_UINT_BITS: number of bits for the high part of ec_enc_uint.
	UintBits = 8
)

// Log2Ceiling computes ceil(log2(n)).
func Log2Ceiling(n int) int {
	if n <= 1 {
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

// ILog returns the number of bits needed to represent val (floor(log2(val))+1).
// Returns 0 for val == 0. Matches libopus EC_ILOG.
func ILog(val uint32) int {
	log := 0
	for val > 0 {
		val >>= 1
		log++
	}
	return log
}
