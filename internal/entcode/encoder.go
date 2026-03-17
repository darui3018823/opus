package entcode

// Encoder is a range encoder bit-exact with libopus ec_enc (celt/entenc.c).
//
// State uses the carry-propagation scheme from libopus:
//   - val (uint32): 31-bit accumulator (values in [0, CodeTop))
//   - rng (uint32): current range, always in (CodeBot, CodeTop] after normalize
//   - rem (int):    pending output byte (-1 = empty)
//   - ext (uint32): count of pending 0xFF bytes (carry chain)
//   - buf ([]byte): output buffer
type Encoder struct {
	buf []byte
	val uint32
	rng uint32
	rem int    // -1 means empty (no pending byte)
	ext uint32 // number of pending 0xFF carry-chain bytes
}

// NewEncoder creates a new range encoder.
func NewEncoder(capacity int) *Encoder {
	return &Encoder{
		buf: make([]byte, 0, capacity),
		val: 0,
		rng: CodeTop, // 0x80000000
		rem: -1,
		ext: 0,
	}
}

// Bytes returns the encoded bytes. Call Flush first.
func (enc *Encoder) Bytes() []byte {
	return enc.buf
}

// Debug accessors for testing
func (enc *Encoder) GetVal() uint32 { return enc.val }
func (enc *Encoder) GetRng() uint32 { return enc.rng }
func (enc *Encoder) GetRem() int    { return enc.rem }
func (enc *Encoder) GetExt() uint32 { return enc.ext }

// Tell returns the number of range-coded bits used so far.
func (enc *Encoder) Tell() int {
	nbytes := len(enc.buf)
	if enc.rem >= 0 {
		nbytes++
	}
	nbytes += int(enc.ext)
	return nbytes*8 + (32 - ILog(enc.rng))
}

// carryOut handles carry propagation - matches ec_enc_carry_out in libopus.
func (enc *Encoder) carryOut(c uint32) {
	if c != SymMax {
		carry := c >> SymBits // 0 or 1
		if enc.rem >= 0 {
			enc.buf = append(enc.buf, byte(uint32(enc.rem)+carry))
		}
		for enc.ext > 0 {
			enc.buf = append(enc.buf, byte(SymMax+carry))
			enc.ext--
		}
		enc.rem = int(c & SymMax)
	} else {
		enc.ext++
	}
}

// normalize outputs bytes while range is below threshold.
// Matches ec_enc_normalize in libopus.
func (enc *Encoder) normalize() {
	for enc.rng <= CodeBot {
		enc.carryOut(enc.val >> CodeShift)
		enc.val = (enc.val << SymBits) & (CodeTop - 1)
		enc.rng <<= SymBits
	}
}

// EncodeIcdf encodes a symbol using a descending ICDF table with ft = 1<<ftb.
// icdf[s] = ft - CDF(s+1), last entry must be 0.
// Bit-exact with ec_enc_icdf in libopus.
func (enc *Encoder) EncodeIcdf(symbol int, icdf []uint8, ftb int) {
	r := enc.rng >> uint(ftb)
	if symbol > 0 {
		enc.val += enc.rng - r*uint32(icdf[symbol-1])
		enc.rng = r * (uint32(icdf[symbol-1]) - uint32(icdf[symbol]))
	} else {
		enc.rng -= r * uint32(icdf[symbol])
	}
	enc.normalize()
}

// EncodeBitLogp encodes a single bit with probability 1/(1<<logp) of being 1.
// Matches ec_enc_bit_logp in libopus.
func (enc *Encoder) EncodeBitLogp(bit bool, logp uint) {
	r := enc.rng >> logp
	if bit {
		enc.val += enc.rng - r
		enc.rng = r
	} else {
		enc.rng -= r
	}
	enc.normalize()
}

// Encode encodes a symbol in [fl, fh) out of [0, ft).
// Uses the same CDF-mapping convention as DecodeIcdf/Decode:
// symbol at CDF [fl, fh) maps to sub-range [r*fl, r*fh) with
// remainder going to the fl=0 (bottom) symbol.
func (enc *Encoder) Encode(fl, fh, ft uint32) {
	r := enc.rng / ft
	if fl > 0 {
		enc.val += enc.rng - r*(ft-fl)
		enc.rng = r * (fh - fl)
	} else {
		enc.rng -= r * (ft - fh)
	}
	enc.normalize()
}

// EncodeUint encodes val in [0, ft) using the ec_enc_uint scheme from libopus.
func (enc *Encoder) EncodeUint(val, ft uint32) {
	if ft <= 1 {
		return
	}
	ft1 := ft - 1
	ftb := ILog(ft1)
	if ftb > UintBits {
		ftb -= UintBits
		fl := val >> uint(ftb)
		ft1 >>= uint(ftb)
		enc.Encode(fl, fl+1, ft1+1)
		enc.EncodeBits(val&((1<<uint(ftb))-1), uint(ftb))
	} else {
		enc.Encode(val, val+1, ft)
	}
}

// EncodeBits writes raw bits through the range coder as uniform bits.
// In a full libopus implementation these go to the end of the packet,
// but for range-coder-only usage we encode them uniformly.
func (enc *Encoder) EncodeBits(val uint32, nbits uint) {
	for i := int(nbits) - 1; i >= 0; i-- {
		enc.EncodeBitLogp((val>>uint(i))&1 == 1, 1)
	}
}

// EncodeBit encodes a single bit. prob is probability of false on a 0-32768 scale.
func (enc *Encoder) EncodeBit(bit bool, prob uint16) {
	r := enc.rng >> 15
	split := r * uint32(prob)
	if bit {
		enc.val += split
		enc.rng -= split
	} else {
		enc.rng = split
	}
	enc.normalize()
}

// Flush finalizes encoding. Must be called before Bytes().
//
// The libopus ec_enc_done operates on a fixed-size pre-allocated buffer.
// Our encoder uses a dynamic buffer, so we directly compute the byte
// sequence that the decoder's init + normalize will reconstruct as a
// value within the final [val, val+rng) interval.
func (enc *Encoder) Flush() {
	// If nothing was encoded, nothing to output
	if enc.rem < 0 && enc.ext == 0 && enc.val == 0 && enc.rng == CodeTop {
		return
	}

	// Compute the minimum number of bits needed so that the symbols encoded
	// thus far will be decoded correctly regardless of trailing bits.
	// l = EC_CODE_BITS - EC_ILOG(rng)
	l := CodeBits - ILog(enc.rng)

	msk := (CodeTop - 1) >> uint(l)
	end := (enc.val + msk) &^ msk

	// Check if (end | msk) >= val + rng; if so, we need one more bit
	if (end | msk) >= enc.val+enc.rng {
		l++
		msk >>= 1
		end = (enc.val + msk) &^ msk
	}

	// Output the needed bytes through the carry chain
	for l > 0 {
		enc.carryOut(end >> CodeShift)
		end = (end << SymBits) & (CodeTop - 1)
		l -= SymBits
	}

	// Flush the buffered rem byte and any ext bytes
	if enc.rem >= 0 || enc.ext > 0 {
		if enc.rem >= 0 {
			enc.buf = append(enc.buf, byte(enc.rem))
		}
		for enc.ext > 0 {
			enc.buf = append(enc.buf, 0x00)
			enc.ext--
		}
		enc.rem = -1
	}
}
