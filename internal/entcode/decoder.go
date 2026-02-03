package entcode

import "errors"

// Decoder is a range decoder for entropy decoding.
type Decoder struct {
	buffer []byte // Input buffer
	pos    int    // Current position in buffer
	low    uint32 // Low end of current range
	rng    uint32 // Size of current range
	val    uint32 // Current value
	err    error  // Error state
	nbits  int    // Number of bits consumed
}

// NewDecoder creates a new range decoder.
func NewDecoder(data []byte) *Decoder {
	dec := &Decoder{
		buffer: data,
		pos:    0,
		low:    0,
		rng:    0xFFFFFFFF,
		val:    0,
		nbits:  0,
	}

	// Initialize value by reading first bytes
	for i := 0; i < 4 && dec.pos < len(dec.buffer); i++ {
		dec.val = (dec.val << 8) | uint32(dec.buffer[dec.pos])
		dec.pos++
	}

	return dec
}

// Tell returns the number of bits read so far.
func (dec *Decoder) Tell() int {
	return dec.nbits
}

// Error returns any error that occurred during decoding.
func (dec *Decoder) Error() error {
	return dec.err
}

// normalize performs range normalization and reads bytes when needed.
func (dec *Decoder) normalize() {
	for dec.rng < (1 << 24) {
		// Read a byte
		var sym byte
		if dec.pos < len(dec.buffer) {
			sym = dec.buffer[dec.pos]
			dec.pos++
		}

		dec.val = ((dec.val << 8) | uint32(sym)) & 0xFFFFFFFF
		dec.rng <<= 8
		dec.nbits += 8
	}
}

// DecodeBit decodes a single bit with a given probability.
// prob is on a scale of 0-32768, where 16384 = 50% probability.
func (dec *Decoder) DecodeBit(prob uint16) bool {
	if dec.err != nil {
		return false
	}

	split := (dec.rng >> 15) * uint32(prob)

	// Determine bit value
	bit := dec.val >= split

	if bit {
		dec.val -= split
		dec.rng -= split
	} else {
		dec.rng = split
	}

	dec.normalize()
	return bit
}

// DecodeSymbol decodes a symbol using an inverse CDF.
func (dec *Decoder) DecodeSymbol(icdf ICdf) int {
	if dec.err != nil {
		return 0
	}

	r := dec.rng
	ft := uint32(16384) // Total frequency (2^14)

	// Find symbol
	// Find symbol
	c := uint32(uint64(dec.val-dec.low) * uint64(ft) / uint64(r))

	// Binary search in ICDF
	symbol := 0
	for symbol < len(icdf)-1 && uint32(icdf[symbol+1]) > c {
		symbol++
	}

	if symbol >= len(icdf)-1 {
		dec.err = errors.New("entcode: invalid symbol")
		return 0
	}

	fl := uint32(icdf[symbol])
	fh := uint32(icdf[symbol+1])

	// Update range
	dec.low += (r * (ft - fh)) / ft
	if fl < fh {
		dec.rng = (r * (fh - fl)) / ft
	} else {
		dec.rng = r / ft
	}

	dec.normalize()
	return symbol
}

// DecodeUint decodes an unsigned integer using n bits.
func (dec *Decoder) DecodeUint(nbits int) uint32 {
	if dec.err != nil {
		return 0
	}

	if nbits == 0 {
		return 0
	}

	value := uint32(0)
	for i := 0; i < nbits; i++ {
		bit := dec.DecodeBit(16384) // 50% probability for uniform distribution
		if bit {
			value |= 1 << uint(nbits-1-i)
		}
	}

	return value
}

// DecodeGetCumu returns the cumulative frequency target for a symbol given the total frequency.
// This is Step 1 of decoding a symbol with explicit frequencies.
func (dec *Decoder) DecodeGetCumu(ft uint32) uint32 {
	if dec.err != nil {
		return 0
	}
	r := dec.rng / ft
	c := (dec.val - dec.low) / r
	return c
}

// DecodeUpdate updates the decoder state after the symbol has been identified.
// fl: cumulative frequency of symbols < symbol
// fh: cumulative frequency of symbols <= symbol
// ft: total frequency
func (dec *Decoder) DecodeUpdate(fl, fh, ft uint32) {
	if dec.err != nil {
		return
	}
	r := dec.rng / ft
	dec.low += r * fl
	dec.rng = r * (fh - fl)
	dec.normalize()
}

// DecodeLaplace decodes a value from a Laplace distribution.
// Matches ec_laplace_decode in libopus.
func (dec *Decoder) DecodeLaplace(fs int, decay int) int {
	if dec.err != nil {
		return 0
	}

	// 1. Decode "Zero" vs "Not Zero"
	// "Slot 0" has probability fs/32768.
	// We check if the target cumulative frequency falls in [0, fs).
	// Total range is 32768.

	val := 0
	c := dec.DecodeGetCumu(32768)

	if c < uint32(fs) {
		// It is zero.
		dec.DecodeUpdate(0, uint32(fs), 32768)
		return 0
	}

	// It is not zero.
	dec.DecodeUpdate(uint32(fs), 32768, 32768)

	// 2. Decode Magnitude
	// We effectively iterate, decoding "continue" bits.
	// EncodeLaplace logic:
	// for i=0..val-1: EncodeBit(true, decay)
	// EncodeBit(false, decay)

	val = 1
	for {
		// Decode "Continue" bit
		// Prob of continue is 'decay'
		// Note: DecodeBit(prob) returns true if we fall in the "upper" part?
		// Revisit DecodeBit:
		// split = (rng >> 15) * prob
		// bit := dec.val >= split
		// if bit (true) -> upper part (low += split)
		// else (false) -> lower part (rng = split)

		// In EncodeBit(bit, prob):
		// if bit: low += split (upper)
		// else: rng = split (lower)

		// In EncodeLaplace: enc.EncodeBit(true, decay) -> Upper

		continuing := dec.DecodeBit(uint16(decay))
		if !continuing {
			break
		}
		val++

		// Safety limit to prevent infinite loops on broken streams
		if val > 10000 {
			dec.err = errors.New("entcode: laplace value too large")
			return 0
		}
	}

	// 3. Decode Sign
	// EncodeLaplace used 50% probability (16384)
	isNegative := dec.DecodeBit(16384)

	if isNegative {
		return -val
	}
	return val
}

// DecodeIcdf decodes a symbol with the given ICDF and frequency total bits.
// This is the exact implementation matching libopus ec_decode function.
func (dec *Decoder) DecodeIcdf(icdf []uint16, ftb int) int {
	if dec.err != nil {
		return 0
	}

	// Get frequency total
	ft := uint32(1 << ftb)
	r := dec.rng >> ftb
	if r == 0 {
		dec.err = errors.New("entcode: range too small")
		return 0
	}

	// Find cumulative frequency
	c := (dec.val - dec.low) / r

	// Binary search in ICDF for symbol
	symbol := 0
	for symbol < len(icdf)-1 {
		if c < uint32(icdf[symbol]) {
			break
		}
		symbol++
	}

	if symbol >= len(icdf)-1 {
		dec.err = errors.New("entcode: invalid symbol")
		return 0
	}

	// Get frequency bounds (reversed in ICDF)
	fl := uint32(icdf[symbol+1])
	fh := uint32(icdf[symbol])

	// Update decoder state
	dec.low += r * (ft - fh)
	if fl < fh {
		dec.rng = r * (fh - fl)
	} else {
		dec.rng = r
	}

	dec.normalize()
	return symbol
}

// BytesLeft returns the number of bytes remaining in the buffer.
func (dec *Decoder) BytesLeft() int {
	return len(dec.buffer) - dec.pos
}
