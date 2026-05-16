package entcode

// Decoder is a range decoder bit-exact with libopus ec_dec (celt/entdec.c).
//
// libopus uses a TOP-DOWN convention:
//   - rng: current range
//   - dif: difference = (top of range) - (coded value)
//   - rem: remainder from byte-unpacking (EC_CODE_EXTRA bits)
//
// The decoder reads bytes and combines them using the EC_CODE_EXTRA
// (7-bit) overlap scheme from libopus.
type Decoder struct {
	buf       []byte
	pos       int    // forward range-coded read position in buf
	endOffs   int    // raw-bit bytes consumed from the end of buf
	endWindow uint32 // raw-bit window, least-significant bits first
	nendBits  uint   // number of valid bits in endWindow
	rng       uint32 // current range
	dif       uint32 // coded value complement (top-down)
	rem       int    // remainder bits from previous byte read
	err       error
}

// NewDecoder creates a new range decoder from encoded data.
// Matches ec_dec_init in libopus.
func NewDecoder(data []byte) *Decoder {
	dec := &Decoder{
		buf: data,
		pos: 0,
		rng: 1 << CodeExtra, // 128
	}

	// Read first byte
	firstByte := uint32(dec.readByte())

	// dif = rng - 1 - (firstByte >> (EC_SYM_BITS - EC_CODE_EXTRA))
	// = 127 - (firstByte >> 1)
	dec.rem = int(firstByte)
	dec.dif = dec.rng - 1 - (firstByte >> (SymBits - CodeExtra))

	// Normalize to fill the range
	dec.normalize()

	return dec
}

// Tell returns the number of bits consumed so far.
// Matches ec_tell: bits from start (range coder) + bits consumed from end (raw).
func (dec *Decoder) Tell() int {
	return dec.pos*8 - ILog(dec.rng) + dec.endOffs*SymBits - int(dec.nendBits)
}

// Error returns any decoding error.
func (dec *Decoder) Error() error {
	return dec.err
}

// Debug accessors for testing
func (dec *Decoder) GetDif() uint32 { return dec.dif }
func (dec *Decoder) GetRng() uint32 { return dec.rng }
func (dec *Decoder) GetRem() int    { return dec.rem }
func (dec *Decoder) GetPos() int    { return dec.pos }

// readByte reads one byte from the buffer, returning 0 past the end.
func (dec *Decoder) readByte() byte {
	if dec.pos+dec.endOffs < len(dec.buf) {
		b := dec.buf[dec.pos]
		dec.pos++
		return b
	}
	return 0
}

func (dec *Decoder) readByteFromEnd() byte {
	if dec.pos+dec.endOffs < len(dec.buf) {
		dec.endOffs++
		return dec.buf[len(dec.buf)-dec.endOffs]
	}
	return 0
}

// normalize reads bytes while range is below threshold.
// Matches ec_dec_normalize in libopus.
func (dec *Decoder) normalize() {
	for dec.rng != 0 && dec.rng <= CodeBot {
		dec.rng <<= SymBits

		// libopus: sym=rem; rem=readByte(); sym=(sym<<8|rem)>>1;
		oldRem := dec.rem
		newByte := int(dec.readByte())
		dec.rem = newByte
		sym := (oldRem<<SymBits | newByte) >> (SymBits - CodeExtra)

		c := uint32(SymMax) &^ uint32(sym)
		dec.dif = ((dec.dif << SymBits) + c) & (CodeTop - 1)
	}
}

// DecodeIcdf decodes a symbol from a descending ICDF table with ft = 1<<ftb.
// Bit-exact with ec_dec_icdf in libopus.
//
// The libopus algorithm iterates through icdf entries:
//
//	s = rng; d = dif; r = rng >> ftb; ret = -1;
//	do { t = s; s = r * icdf[++ret]; } while (d < s);
//	dif = d - s; rng = t - s; normalize();
func (dec *Decoder) DecodeIcdf(icdf []uint8, ftb int) int {
	if dec.err != nil {
		return 0
	}

	r := dec.rng >> uint(ftb)
	d := dec.dif
	s := dec.rng
	ret := -1

	var t uint32
	for {
		t = s
		ret++
		s = r * uint32(icdf[ret])
		if d >= s {
			break
		}
	}

	dec.dif = d - s
	dec.rng = t - s
	dec.normalize()
	return ret
}

// DecodeBitLogp decodes a single bit with probability 1/(1<<logp) of being 1.
// Matches ec_dec_bit_logp in libopus.
//
// Encoder convention (ec_enc_bit_logp):
//
//	r = rng >> logp
//	if bit: val += rng - r; rng = r   (true at top, width r)
//	else:   rng -= r                    (false at bottom, width rng-r)
//
// Top-down decoder: dif < r => true, dif >= r => false.
func (dec *Decoder) DecodeBitLogp(logp uint) bool {
	if dec.err != nil {
		return false
	}
	r := dec.rng >> logp
	d := dec.dif

	val := d < r
	if val {
		dec.rng = r
	} else {
		dec.dif = d - r
		dec.rng -= r
	}
	dec.normalize()
	return val
}

// Decode returns the CDF position for a symbol from [0, ft).
// Must be followed by DecodeUpdate. Matches ec_decode in libopus.
//
// In top-down convention, dif/r gives position from top.
func (dec *Decoder) Decode(ft uint32) uint32 {
	if dec.err != nil || ft == 0 {
		return 0
	}
	r := dec.rng / ft
	if r == 0 {
		// Range is smaller than the alphabet; return the last symbol as a
		// safe fallback (DecodeUpdate will correct the state).
		return ft - 1
	}
	s := dec.dif / r
	if s >= ft {
		return 0
	}
	return ft - 1 - s
}

// DecodeUpdate updates decoder state after Decode.
// fl, fh are CDF bounds [fl, fh) out of [0, ft).
// Matches ec_dec_update in libopus.
func (dec *Decoder) DecodeUpdate(fl, fh, ft uint32) {
	r := dec.rng / ft
	dec.dif -= r * (ft - fh)
	if fl > 0 {
		dec.rng = r * (fh - fl)
	} else {
		dec.rng -= r * (ft - fh)
	}
	dec.normalize()
}

// DecodeUint decodes a value in [0, ft) using ec_dec_uint scheme from libopus.
func (dec *Decoder) DecodeUint(ft uint32) uint32 {
	if ft <= 1 {
		return 0
	}
	ft1 := ft - 1
	ftb := ILog(ft1)
	if ftb > UintBits {
		ftb -= UintBits
		ft1 >>= uint(ftb)
		s := dec.Decode(ft1 + 1)
		dec.DecodeUpdate(s, s+1, ft1+1)
		low := dec.DecodeBits(uint(ftb))
		return s<<uint(ftb) | low
	}
	s := dec.Decode(ft)
	dec.DecodeUpdate(s, s+1, ft)
	return s
}

// DecodeBits decodes raw bits from the end of the packet, matching ec_dec_bits.
func (dec *Decoder) DecodeBits(nbits uint) uint32 {
	if nbits == 0 {
		return 0
	}
	for dec.nendBits < nbits {
		dec.endWindow |= uint32(dec.readByteFromEnd()) << dec.nendBits
		dec.nendBits += SymBits
	}
	var mask uint32
	if nbits >= 32 {
		mask = ^uint32(0)
	} else {
		mask = (uint32(1) << nbits) - 1
	}
	ret := dec.endWindow & mask
	if nbits >= 32 {
		dec.endWindow = 0
	} else {
		dec.endWindow >>= nbits
	}
	dec.nendBits -= nbits
	return ret
}

// DecodeBit decodes a single bit. prob is probability of false on 0-32768 scale.
// Matches the custom EncodeBit in encoder.go (not a libopus API).
//
// Encoder: split = rng - (rng >> 15) * prob
//
//	true:  val += split, rng -= split  (true at top in val-space)
//	false: rng = split                  (false at bottom in val-space)
//
// Top-down: true => dif in [0, rng-split), false => dif in [rng-split, rng)
// rng - split = (rng >> 15) * prob
func (dec *Decoder) DecodeBit(prob uint16) bool {
	if dec.err != nil {
		return false
	}
	d := dec.dif
	// rng - split = (rng >> 15) * prob = true-interval width in dif-space
	trueWidth := (dec.rng >> 15) * uint32(prob)

	bit := d < trueWidth
	if bit {
		// true: stay in [0, trueWidth), rng = trueWidth
		dec.rng = trueWidth
	} else {
		// false: shift into [0, rng-trueWidth), rng = split = rng - trueWidth
		dec.dif = d - trueWidth
		dec.rng -= trueWidth
	}
	dec.normalize()
	return bit
}

// DecodeGetCumu returns the cumulative frequency position of the current symbol
// in [0, ft). This is equivalent to Decode but named for use with
// explicit-frequency protocols such as recursive PVQ splitting.
func (dec *Decoder) DecodeGetCumu(ft uint32) uint32 {
	return dec.Decode(ft)
}

// BytesLeft returns remaining unread bytes.
func (dec *Decoder) BytesLeft() int {
	return len(dec.buf) - dec.pos - dec.endOffs
}
