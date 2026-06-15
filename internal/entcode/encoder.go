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

	// Raw end-of-packet bit buffer, mirroring libopus end_window/nend_bits.
	// Raw bits (ec_enc_bits) are written to the END of the packet, LSB first,
	// symmetric with the decoder's DecodeBits (readByteFromEnd). endBytes holds
	// the tail bytes in tail order: endBytes[0] is the very last packet byte,
	// endBytes[1] the second-last, etc.
	endWindow uint32
	nendBits  uint
	endBytes  []byte
	nbitsRaw  int // total raw bits emitted (for Tell)

	// capacity is the target packet size; raw bits are placed at offset capacity
	// (the absolute end) so a fixed-size packet keeps a zeroed gap between the
	// forward range bytes and the trailing raw bytes, matching libopus storage.
	capacity int

	// Merge byte stashed by Flush: the leftover (<8) raw window bits that share
	// the byte immediately before the raw tail (libopus buf[storage-end_offs-1]).
	mergeBits uint32
	mergeUsed int
}

// NewEncoder creates a new range encoder. capacity is the intended packet size
// in bytes; it sets where end-of-packet raw bits are placed (the absolute end).
func NewEncoder(capacity int) *Encoder {
	return &Encoder{
		buf:      make([]byte, 0, capacity),
		val:      0,
		rng:      CodeTop, // 0x80000000
		rem:      -1,
		ext:      0,
		capacity: capacity,
	}
}

// Bytes returns the encoded packet. Call Flush first.
//
// When no raw end-of-packet bits were written, this returns the minimal range-
// coded front buffer (preserving the historical byte-length semantics relied on
// by size-measuring tests). When raw bits exist, it assembles the libopus-style
// layout: forward range bytes at the front, a zeroed gap, and the raw bytes at
// the absolute end (so the decoder's DecodeBits reads them from the end). The
// packet is sized to capacity when that leaves room, matching a fixed-size Opus
// payload; otherwise it grows to fit.
func (enc *Encoder) Bytes() []byte {
	tailLen := len(enc.endBytes)
	if tailLen == 0 && enc.mergeUsed == 0 {
		return enc.buf
	}

	frontLen := len(enc.buf)
	// The packet is the fixed capacity (libopus storage). The merge byte holds
	// the leftover (<8) raw window bits and shares buf[storage-end_offs-1] — the
	// byte just before the raw tail — which is also the last range-front byte when
	// the packet is full, so it must NOT add a byte. Only grow past capacity when
	// the range front and raw tail genuinely cannot coexist (real over-budget).
	n := enc.capacity
	if need := frontLen + tailLen; need > n {
		n = need
	}

	out := make([]byte, n)
	copy(out, enc.buf)
	// Raw tail: endBytes[0] is the very last packet byte.
	for i, b := range enc.endBytes {
		out[n-1-i] = b
	}
	// Merge byte: leftover (<8) raw bits OR'd into the slot just before the tail
	// (shared with the last range-front byte when full).
	if enc.mergeUsed > 0 {
		out[n-tailLen-1] |= byte(enc.mergeBits)
	}
	return out
}

// Shrink reduces the encoder's capacity (storage) to newSize bytes.
// This is the equivalent of libopus ec_enc_shrink: for VBR mode, after encoding
// all symbols, the packet can be shrunk so that the decoder sees a smaller
// packet and computes its allocation accordingly. The raw end-of-packet bits
// are logically relocated to the new end position.
//
// In libopus the raw tail bytes must be memmove'd because they live at the end
// of a single flat buffer. Our encoder keeps endBytes in a separate slice, so
// only the capacity field needs updating — Bytes() already places the tail at
// offset capacity-1.
//
// Precondition: newSize >= len(buf) + len(endBytes) (the range-coded front and
// raw tail must both fit).
func (enc *Encoder) Shrink(newSize int) {
	if newSize < len(enc.buf)+len(enc.endBytes) {
		// Cannot shrink below the actually-used bytes.
		return
	}
	enc.capacity = newSize
}

// UsedBytes returns the minimum number of bytes needed to hold the encoded
// content. Valid after Flush(). This mirrors (ec_tell(&enc)+7)/8 in libopus
// which is used to determine the shrink target for VBR packets.
func (enc *Encoder) UsedBytes() int {
	n := (enc.ECTell() + 7) >> 3
	if n < 2 {
		n = 2 // Opus minimum packet size
	}
	return n
}

// Debug accessors for testing
func (enc *Encoder) GetVal() uint32 { return enc.val }
func (enc *Encoder) GetRng() uint32 { return enc.rng }
func (enc *Encoder) GetRem() int    { return enc.rem }
func (enc *Encoder) GetExt() uint32 { return enc.ext }

// Tell returns the number of bits used so far, including end-of-packet raw bits
// (mirrors libopus ec_tell, whose nbits_total counts raw bits via ec_enc_bits).
func (enc *Encoder) Tell() int {
	nbytes := len(enc.buf)
	if enc.rem >= 0 {
		nbytes++
	}
	nbytes += int(enc.ext)
	return nbytes*8 + (32 - ILog(enc.rng)) + enc.nbitsRaw
}

// ecNbitsTotal returns the libopus encoder nbits_total value. libopus tracks
// nbits_total = (EC_CODE_BITS+1) + EC_SYM_BITS*(symbols shifted out) + raw bits.
// The number of symbols shifted out equals the bytes committed to the range
// stream (those in buf, the pending rem byte, and any buffered 0xFF ext bytes).
func (enc *Encoder) ecNbitsTotal() int {
	nbytes := len(enc.buf)
	if enc.rem >= 0 {
		nbytes++
	}
	nbytes += int(enc.ext)
	return nbytes*8 + (CodeBits + 1) + enc.nbitsRaw
}

// ECTell returns bits consumed using the libopus ec_tell convention (== 1
// immediately after init). Our internal Tell() reports ec_tell-1, so this is
// Tell()+1, matching the decoder's ECTell(). Use this where porting libopus
// guards verbatim so encoder and decoder budget decisions stay symmetric.
func (enc *Encoder) ECTell() int { return enc.Tell() + 1 }

// TellFrac returns bits used in 1/8-bit (Q3) resolution, bit-exact with
// ec_tell_frac in libopus (celt/entcode.c). Symmetric with Decoder.TellFrac so
// the shared CELT quant/allocation code computes identical budgets in both
// directions.
func (enc *Encoder) TellFrac() int {
	correction := [8]uint32{35733, 38967, 42495, 46340, 50535, 55109, 60097, 65535}
	nbits := enc.ecNbitsTotal() << 3
	l := ILog(enc.rng)
	r := enc.rng >> uint(l-16)
	b := (r >> 12) - 8
	if r > correction[b] {
		b++
	}
	l = (l << 3) + int(b)
	return nbits - l
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
	for enc.rng != 0 && enc.rng <= CodeBot {
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
	if ft == 0 {
		return
	}
	r := enc.rng / ft
	if r == 0 {
		// ft exceeds current range; skip to avoid rng → 0 and infinite normalize.
		enc.normalize()
		return
	}
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

// writeByteAtEnd appends one raw byte to the end-of-packet tail buffer.
// Mirrors libopus ec_write_byte_at_end (writing buf[storage-(++end_offs)]).
func (enc *Encoder) writeByteAtEnd(value byte) {
	enc.endBytes = append(enc.endBytes, value)
}

// EncodeBits writes nbits raw bits to the END of the packet, LSB first.
// Bit-exact with libopus ec_enc_bits (celt/entenc.c): symmetric with the
// decoder's DecodeBits, which reads raw bits from the end of the packet.
func (enc *Encoder) EncodeBits(fl uint32, nbits uint) {
	if nbits == 0 {
		return
	}
	window := enc.endWindow
	used := int(enc.nendBits)
	if used+int(nbits) > 32 {
		// Flush whole bytes out of the window (do-while in libopus).
		for {
			enc.writeByteAtEnd(byte(window & SymMax))
			window >>= SymBits
			used -= SymBits
			if used < SymBits {
				break
			}
		}
	}
	window |= fl << uint(used)
	used += int(nbits)
	enc.endWindow = window
	enc.nendBits = uint(used)
	enc.nbitsRaw += int(nbits)
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
	// If nothing was encoded, nothing to output.
	if enc.rem < 0 && enc.ext == 0 && enc.val == 0 && enc.rng == CodeTop && enc.nbitsRaw == 0 {
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

	// Output the needed bytes through the carry chain.
	for l > 0 {
		enc.carryOut(end >> CodeShift)
		end = (end << SymBits) & (CodeTop - 1)
		l -= SymBits
	}

	// Flush the buffered rem byte and any ext bytes via a final carry-out with
	// no carry, exactly as libopus ec_enc_done does:
	//     if(_this->rem>=0||_this->ext>0)ec_enc_carry_out(_this,0);
	// This resolves pending ext (0xFF carry-chain) bytes to 0xFF — NOT 0x00 —
	// when there is no trailing carry. Hardcoding 0x00 here corrupts the tail of
	// any packet that ends with the range at the top of its interval (e.g. a
	// single top-of-range symbol flushed to 0x00 instead of 0xFF, decoding to 0).
	if enc.rem >= 0 || enc.ext > 0 {
		enc.carryOut(0)
	}

	// Flush whole bytes still held in the raw end-of-packet window, then stash
	// the leftover (<8) bits as the merge byte that shares the slot immediately
	// before the raw tail (libopus ec_enc_done: buf[storage-end_offs-1] |= win).
	window := enc.endWindow
	used := int(enc.nendBits)
	for used >= SymBits {
		enc.writeByteAtEnd(byte(window & SymMax))
		window >>= SymBits
		used -= SymBits
	}
	enc.mergeBits = window
	enc.mergeUsed = used
	enc.endWindow = 0
	enc.nendBits = 0
}

// EncodeExact encodes a symbol given its exact cumulative frequency range.
// fl: cumulative frequency of symbols < symbol
// fh: cumulative frequency of symbols <= symbol
// ft: total frequency (may be non-power-of-2)
func (enc *Encoder) EncodeExact(fl, fh, ft uint32) {
	enc.Encode(fl, fh, ft)
}
