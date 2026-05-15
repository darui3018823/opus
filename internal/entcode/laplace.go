package entcode

// Laplace encoder/decoder — exact port of libopus celt/laplace.c.
//
// EncodeLaplace and DecodeLaplace implement entropy coding for
// Laplace-distributed integer values.  They are used by CELT coarse energy
// coding (RFC 6716 §4.3.3).
//
// Parameters:
//   - fs    — probability of zero, scaled by 32768  (range: (0, 32768))
//   - decay — per-step probability decay, scaled by 32768  (range: [0, 32768))
//
// The total frequency table has ft = 32768.

const (
	laplaceLogMinp = 0
	laplaceMinp    = 1 << laplaceLogMinp // 1
	laplaceNmin    = 16
)

// laplaceGetFreq1 returns the frequency allocated to the symbols ±1 (each)
// given the zero-symbol frequency fs0 and the decay factor.
// Matches ec_laplace_get_freq1 in libopus.
func laplaceGetFreq1(fs0 uint32, decay int) uint32 {
	ft := uint32(32768) - laplaceMinp*uint32(2*laplaceNmin) - fs0
	return uint32(int64(ft)*int64(16384-decay)) >> 15
}

// EncodeLaplace encodes an integer value assumed to be Laplace-distributed.
//
// value is passed by pointer; it may be clamped to the maximum representable
// value if the range coder runs out of precision, matching libopus behaviour.
//
// fs is Pr(X==0)×32768; decay is the per-step decay×32768.
//
// Matches ec_laplace_encode in libopus celt/laplace.c.
func (enc *Encoder) EncodeLaplace(value *int, fs uint32, decay int) {
	var fl uint32
	val := *value

	fl = 0
	if val != 0 {
		var s int
		if val < 0 {
			s = -1
		}
		val = (val + s) ^ s // |val|

		fl = fs
		fs = laplaceGetFreq1(fs, decay)

		i := 1
		for fs > 0 && i < val {
			fs *= 2
			fl += fs + 2*laplaceMinp
			fs = uint32(int64(fs)*int64(decay)) >> 15
			i++
		}

		if fs == 0 {
			// Clamp: compute maximum representable di and update value.
			ndiMax := int((32768-fl+laplaceMinp-1) >> laplaceLogMinp)
			ndiMax = (ndiMax - s) >> 1
			di := val - i
			if di > ndiMax-1 {
				di = ndiMax - 1
			}
			fl += uint32(2*di+1+s) * laplaceMinp
			fs = laplaceMinp
			if 32768-fl < fs {
				fs = 32768 - fl
			}
			*value = (i + di + s) ^ s
		} else {
			fs += laplaceMinp
			// In C: fl += fs & ~s  where s is 0 or -1 (all-bits set).
			// s==0  → ~s = -1 (all ones) → fl += fs
			// s==-1 → ~s = 0              → fl += 0
			if s == 0 {
				fl += fs
			}
		}
	}

	enc.Encode(fl, fl+fs, 32768)
}

// DecodeLaplace decodes an integer value assumed to be Laplace-distributed.
//
// fs is Pr(X==0)×32768; decay is the per-step decay×32768.
//
// Matches ec_laplace_decode in libopus celt/laplace.c.
func (dec *Decoder) DecodeLaplace(fs uint32, decay int) int {
	val := 0
	fl := uint32(0)
	fm := dec.Decode(32768)

	if fm >= fs {
		val++
		fl = fs
		fs = laplaceGetFreq1(fs, decay) + laplaceMinp

		for fs > laplaceMinp && fm >= fl+2*fs {
			fs *= 2
			fl += fs
			fs = uint32(int64(fs-2*laplaceMinp)*int64(decay))>>15 + laplaceMinp
			val++
		}

		if fs <= laplaceMinp {
			di := int(fm-fl) >> (laplaceLogMinp + 1)
			val += di
			fl += uint32(2*di) * laplaceMinp
		}

		if fm < fl+fs {
			val = -val
		} else {
			fl += fs
		}
	}

	hi := fl + fs
	if hi > 32768 {
		hi = 32768
	}
	dec.DecodeUpdate(fl, hi, 32768)
	return val
}
