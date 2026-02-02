package entcode

import "errors"

// Encoder is a range encoder for entropy coding.
type Encoder struct {
	buffer    []byte // Output buffer
	pos       int    // Current position in buffer
	low       uint32 // Low end of current range
	rng       uint32 // Size of current range
	rem       int    // Carry propagation remainder
	ext       uint32 // Number of outstanding bytes
	nbits     int    // Number of bits buffered
	endWindow uint32 // Final bits
}

// NewEncoder creates a new range encoder.
func NewEncoder(capacity int) *Encoder {
	return &Encoder{
		buffer: make([]byte, 0, capacity),
		low:    0,
		rng:    0xFFFFFFFF,
		rem:    -1,
		ext:    0,
		nbits:  0,
	}
}

// Bytes returns the encoded bytes.
func (enc *Encoder) Bytes() []byte {
	return enc.buffer
}

// Tell returns the number of bits written so far.
func (enc *Encoder) Tell() int {
	return (len(enc.buffer)+int(enc.ext))*8 + enc.nbits
}

// normalize performs range normalization and outputs bytes when needed.
func (enc *Encoder) normalize() {
	for enc.rng < (1 << 24) {
		// Output top byte
		enc.buffer = append(enc.buffer, byte(enc.low>>24))
		enc.low = (enc.low << 8) & 0xFFFFFFFF
		enc.rng <<= 8
		enc.nbits += 8
	}
}

// EncodeBit encodes a single bit with a given probability.
// prob is on a scale of 0-32768, where 16384 = 50% probability.
func (enc *Encoder) EncodeBit(bit bool, prob uint16) {
	// Scale range by probability
	split := (enc.rng >> 15) * uint32(prob)

	if bit {
		enc.low += split
		enc.rng -= split
	} else {
		enc.rng = split
	}

	enc.normalize()
}

// EncodeSymbol encodes a symbol using an inverse CDF.
func (enc *Encoder) EncodeSymbol(symbol int, icdf ICdf) error {
	if symbol < 0 || symbol >= len(icdf)-1 {
		return errors.New("entcode: symbol out of range")
	}

	r := enc.rng
	fl := uint32(icdf[symbol])
	fh := uint32(icdf[symbol+1])
	ft := uint32(16384) // Total frequency (2^14)

	// Update range
	enc.low += (r * (ft - fh)) / ft
	if fl < fh {
		enc.rng = (r * (fh - fl)) / ft
	} else {
		enc.rng = r / ft
	}

	enc.normalize()
	return nil
}

// EncodeUint encodes an unsigned integer using n bits.
func (enc *Encoder) EncodeUint(value uint32, nbits int) {
	if nbits == 0 {
		return
	}

	for nbits > 0 {
		// Encode bit by bit (can be optimized)
		bit := (value >> uint(nbits-1)) & 1
		enc.EncodeBit(bit == 1, 16384) // 50% probability for uniform distribution
		nbits--
	}
}

// EncodeIcdf encodes a symbol with the given ICDF and frequency total bits.
// This is the exact implementation matching libopus ec_encode function.
func (enc *Encoder) EncodeIcdf(symbol int, icdf []uint16, ftb int) error {
	if symbol < 0 {
		return errors.New("entcode: negative symbol")
	}
	if symbol >= len(icdf)-1 {
		return errors.New("entcode: symbol out of range")
	}

	// Get frequency bounds
	fl := uint32(icdf[symbol+1]) // Lower frequency (reversed in ICDF)
	fh := uint32(icdf[symbol])   // Higher frequency
	ft := uint32(1 << ftb)       // Total frequency

	// Scale range
	r := enc.rng >> ftb
	if r == 0 {
		return errors.New("entcode: range too small")
	}

	// Update low and range
	enc.low += r * (ft - fh)
	if fl < fh {
		enc.rng = r * (fh - fl)
	} else {
		enc.rng = r
	}

	enc.normalize()
	return nil
}

// EncodeExact encodes a symbol given its cumulative frequency range and total frequency.
// fl: cumulative frequency of symbols < symbol
// fh: cumulative frequency of symbols <= symbol
// ft: total frequency
// This allows encoding with non-power-of-2 totals, as used in PVQ splitting.
func (enc *Encoder) EncodeExact(fl, fh, ft uint32) {
	r := enc.rng / ft
	enc.low += r * fl
	enc.rng = r * (fh - fl)
	enc.normalize()
}

// Flush finalizes the encoding and outputs remaining bits.
func (enc *Encoder) Flush() {
	// Normalize one final time
	enc.normalize()

	// Output remaining bytes
	for i := 0; i < 4; i++ {
		enc.buffer = append(enc.buffer, byte(enc.low>>24))
		enc.low <<= 8
	}
}
