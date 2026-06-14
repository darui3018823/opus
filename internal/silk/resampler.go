package silk

import "fmt"

// This file is a bit-exact port of the libopus SILK sample-rate converter
// (silk/resampler.c and friends).  It operates on int16 throughout, exactly as
// libopus does, so SILK-mode decoder output matches the reference bitstream.
//
// Only the decoder direction is supported (forEnc = 0): input rates 8/12/16 kHz,
// output rates 8/12/16/24/48 kHz.

const (
	resamplerMaxBatchSizeMs = 10
	resamplerOrderFIR12     = 8
	resamplerDownOrderFIR0  = 18
	resamplerDownOrderFIR1  = 24
	resamplerDownOrderFIR2  = 36

	useResamplerCopy    = 0
	useResamplerUp2HQ   = 1
	useResamplerIIRFIR  = 2
	useResamplerDownFIR = 3
)

// delayMatrixDec[rateID(in)][rateID(out)] — decoder input-delay compensation.
//
//	in \ out   8  12  16  24  48
var delayMatrixDec = [3][5]int{
	{4, 0, 2, 0, 0},  //  8
	{0, 9, 4, 7, 4},  // 12
	{0, 3, 12, 7, 7}, // 16
}

// 2x upsampler allpass coefficients (high quality).
var silkResamplerUp2HQ0 = [3]int16{1746, 14986, 39083 - 65536}
var silkResamplerUp2HQ1 = [3]int16{6854, 25769, 55542 - 65536}

// Interpolation fractions 1/24, 3/24, ... 23/24.
var silkResamplerFracFIR12 = [12][4]int16{
	{189, -600, 617, 30567},
	{117, -159, -1070, 29704},
	{52, 221, -2392, 28276},
	{-4, 529, -3350, 26341},
	{-48, 758, -3956, 23973},
	{-80, 905, -4235, 21254},
	{-99, 972, -4222, 18278},
	{-107, 967, -3957, 15143},
	{-103, 896, -3487, 11950},
	{-91, 773, -2865, 8798},
	{-71, 611, -2143, 5784},
	{-46, 425, -1375, 2996},
}

// Downsampler IIR (first two entries: AR2 A_Q14) + FIR coefficient tables.
var silkResampler34Coefs = []int16{
	-20694, -13867,
	-49, 64, 17, -157, 353, -496, 163, 11047, 22205,
	-39, 6, 91, -170, 186, 23, -896, 6336, 19928,
	-19, -36, 102, -89, -24, 328, -951, 2568, 15909,
}
var silkResampler23Coefs = []int16{
	-14457, -14019,
	64, 128, -122, 36, 310, -768, 584, 9267, 17733,
	12, 128, 18, -142, 288, -117, -865, 4123, 14459,
}
var silkResampler12Coefs = []int16{
	616, -14323,
	-10, 39, 58, -46, -84, 120, 184, -315, -541, 1284, 5380, 9024,
}
var silkResampler13Coefs = []int16{
	16102, -15162,
	-13, 0, 20, 26, 5, -31, -43, -4, 65, 90, 7, -157, -248, -44, 593, 1583, 2612, 3271,
}
var silkResampler14Coefs = []int16{
	22500, -15099,
	3, -14, -20, -15, 2, 25, 37, 25, -16, -71, -107, -79, 50, 292, 623, 982, 1288, 1464,
}
var silkResampler16Coefs = []int16{
	27540, -15257,
	17, 12, 8, 1, -10, -22, -30, -32, -22, 3, 44, 100, 168, 243, 317, 381, 429, 455,
}

// Resampler is a bit-exact port of silk_resampler_state_struct + silk_resampler.
type Resampler struct {
	fsHzIn  int
	fsHzOut int

	sIIR     [6]int32
	sFIR16   [36]int16
	sFIR32   [36]int32
	delayBuf [48]int16

	resamplerFunction int
	batchSize         int
	invRatioQ16       int32
	firOrder          int
	firFracs          int
	fsInKHz           int
	fsOutKHz          int
	inputDelay        int
	coefs             []int16
}

// rateID maps [8000,12000,16000,24000,48000] to [0,1,2,3,4].
func rateID(r int) int {
	g := 0
	if r > 16000 {
		g = 1
	}
	s := 0
	if r > 24000 {
		s = 1
	}
	return (((r >> 12) - g) >> s) - 1
}

// NewResampler initializes a decoder-direction resampler for the given rates.
func NewResampler(fsHzIn, fsHzOut int) (*Resampler, error) {
	switch fsHzIn {
	case 8000, 12000, 16000:
	default:
		return nil, fmt.Errorf("silk resampler: unsupported input rate %d", fsHzIn)
	}
	switch fsHzOut {
	case 8000, 12000, 16000, 24000, 48000:
	default:
		return nil, fmt.Errorf("silk resampler: unsupported output rate %d", fsHzOut)
	}
	s := &Resampler{fsHzIn: fsHzIn, fsHzOut: fsHzOut}
	if err := s.init(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Resampler) init() error {
	s.sIIR = [6]int32{}
	s.sFIR16 = [36]int16{}
	s.sFIR32 = [36]int32{}
	s.delayBuf = [48]int16{}

	s.inputDelay = delayMatrixDec[rateID(s.fsHzIn)][rateID(s.fsHzOut)]
	s.fsInKHz = s.fsHzIn / 1000
	s.fsOutKHz = s.fsHzOut / 1000
	s.batchSize = s.fsInKHz * resamplerMaxBatchSizeMs

	up2x := 0
	switch {
	case s.fsHzOut > s.fsHzIn:
		if s.fsHzOut == s.fsHzIn*2 {
			s.resamplerFunction = useResamplerUp2HQ
		} else {
			s.resamplerFunction = useResamplerIIRFIR
			up2x = 1
		}
	case s.fsHzOut < s.fsHzIn:
		s.resamplerFunction = useResamplerDownFIR
		switch {
		case s.fsHzOut*4 == s.fsHzIn*3:
			s.firFracs = 3
			s.firOrder = resamplerDownOrderFIR0
			s.coefs = silkResampler34Coefs
		case s.fsHzOut*3 == s.fsHzIn*2:
			s.firFracs = 2
			s.firOrder = resamplerDownOrderFIR0
			s.coefs = silkResampler23Coefs
		case s.fsHzOut*2 == s.fsHzIn:
			s.firFracs = 1
			s.firOrder = resamplerDownOrderFIR1
			s.coefs = silkResampler12Coefs
		case s.fsHzOut*3 == s.fsHzIn:
			s.firFracs = 1
			s.firOrder = resamplerDownOrderFIR2
			s.coefs = silkResampler13Coefs
		case s.fsHzOut*4 == s.fsHzIn:
			s.firFracs = 1
			s.firOrder = resamplerDownOrderFIR2
			s.coefs = silkResampler14Coefs
		case s.fsHzOut*6 == s.fsHzIn:
			s.firFracs = 1
			s.firOrder = resamplerDownOrderFIR2
			s.coefs = silkResampler16Coefs
		default:
			return fmt.Errorf("silk resampler: no downsampler for %d->%d", s.fsHzIn, s.fsHzOut)
		}
	default:
		s.resamplerFunction = useResamplerCopy
	}

	invRatio := int32((s.fsHzIn << uint(14+up2x)) / s.fsHzOut)
	invRatio <<= 2
	for silkSMULWW(invRatio, int32(s.fsHzOut)) < int32(s.fsHzIn<<uint(up2x)) {
		invRatio++
	}
	s.invRatioQ16 = invRatio
	return nil
}

// Reset clears the filter/delay state (keeping the configured rates).
func (s *Resampler) Reset() {
	_ = s.init()
}

// Process resamples the int16 input and returns the int16 output, mirroring
// silk_resampler() including the input-delay buffering across calls.
func (s *Resampler) Process(in []int16) []int16 {
	inLen := len(in)
	if inLen < s.fsInKHz {
		// libopus asserts inLen >= Fs_in_kHz; be defensive instead.
		return nil
	}
	nSamples := s.fsInKHz - s.inputDelay

	// Copy first part to delay buffer (delayBuf[0:inputDelay] holds prior tail).
	copy(s.delayBuf[s.inputDelay:s.fsInKHz], in[:nSamples])

	// The second chunk processes only `inLen - fsInKHz` samples (libopus
	// silk_resampler), i.e. in[nSamples : inLen-inputDelay]. The final
	// inputDelay samples are held back for the next call's delay buffer. When
	// inputDelay==0 (e.g. 8 kHz->48 kHz) this is in[nSamples:inLen]; for 12/16
	// kHz (inputDelay 4/7) the tail must be excluded or it is processed twice.
	secondEnd := inLen - s.inputDelay

	var out []int16
	switch s.resamplerFunction {
	case useResamplerUp2HQ:
		out = s.up2HQ(out, s.delayBuf[:s.fsInKHz])
		out = s.up2HQ(out, in[nSamples:secondEnd])
	case useResamplerIIRFIR:
		out = s.iirFIR(out, s.delayBuf[:s.fsInKHz])
		out = s.iirFIR(out, in[nSamples:secondEnd])
	case useResamplerDownFIR:
		out = s.downFIR(out, s.delayBuf[:s.fsInKHz])
		out = s.downFIR(out, in[nSamples:secondEnd])
	default:
		out = append(out, s.delayBuf[:s.fsInKHz]...)
		out = append(out, in[nSamples:secondEnd]...)
	}

	// Save the tail for the next call.
	copy(s.delayBuf[:s.inputDelay], in[inLen-s.inputDelay:inLen])
	return out
}

// up2HQ appends 2*len(in) upsampled samples to out.
func (s *Resampler) up2HQ(out []int16, in []int16) []int16 {
	o := make([]int16, 2*len(in))
	s.up2HQInternal(o, in)
	return append(out, o...)
}

// up2HQInternal mirrors silk_resampler_private_up2_HQ writing into out[0:2*len(in)].
func (s *Resampler) up2HQInternal(out []int16, in []int16) {
	for k := 0; k < len(in); k++ {
		in32 := int32(in[k]) << 10

		// Even output sample.
		y := in32 - s.sIIR[0]
		x := silkSMULWB(y, silkResamplerUp2HQ0[0])
		out321 := s.sIIR[0] + x
		s.sIIR[0] = in32 + x

		y = out321 - s.sIIR[1]
		x = silkSMULWB(y, silkResamplerUp2HQ0[1])
		out322 := s.sIIR[1] + x
		s.sIIR[1] = out321 + x

		y = out322 - s.sIIR[2]
		x = silkSMLAWB(y, y, silkResamplerUp2HQ0[2])
		out321 = s.sIIR[2] + x
		s.sIIR[2] = out322 + x

		out[2*k] = silkSAT16(rshiftRound(int64(out321), 10))

		// Odd output sample.
		y = in32 - s.sIIR[3]
		x = silkSMULWB(y, silkResamplerUp2HQ1[0])
		out321 = s.sIIR[3] + x
		s.sIIR[3] = in32 + x

		y = out321 - s.sIIR[4]
		x = silkSMULWB(y, silkResamplerUp2HQ1[1])
		out322 = s.sIIR[4] + x
		s.sIIR[4] = out321 + x

		y = out322 - s.sIIR[5]
		x = silkSMLAWB(y, y, silkResamplerUp2HQ1[2])
		out321 = s.sIIR[5] + x
		s.sIIR[5] = out322 + x

		out[2*k+1] = silkSAT16(rshiftRound(int64(out321), 10))
	}
}

// iirFIR mirrors silk_resampler_private_IIR_FIR (2x upsample + FIR interpolation).
func (s *Resampler) iirFIR(out []int16, in []int16) []int16 {
	inLen := len(in)
	buf := make([]int16, 2*s.batchSize+resamplerOrderFIR12)
	copy(buf[:resamplerOrderFIR12], s.sFIR16[:resamplerOrderFIR12])

	idxInc := s.invRatioQ16
	nSamplesIn := 0
	inOff := 0
	for {
		nSamplesIn = inLen
		if nSamplesIn > s.batchSize {
			nSamplesIn = s.batchSize
		}
		s.up2HQInternal(buf[resamplerOrderFIR12:], in[inOff:inOff+nSamplesIn])
		maxIndexQ16 := int32(nSamplesIn) << (16 + 1)
		out = iirFIRInterpol(out, buf, maxIndexQ16, idxInc)
		inOff += nSamplesIn
		inLen -= nSamplesIn
		if inLen > 0 {
			copy(buf[:resamplerOrderFIR12], buf[nSamplesIn<<1:nSamplesIn<<1+resamplerOrderFIR12])
		} else {
			break
		}
	}
	copy(s.sFIR16[:resamplerOrderFIR12], buf[nSamplesIn<<1:nSamplesIn<<1+resamplerOrderFIR12])
	return out
}

func iirFIRInterpol(out []int16, buf []int16, maxIndexQ16, idxInc int32) []int16 {
	for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += idxInc {
		tableIndex := silkSMULWB(indexQ16&0xFFFF, 12)
		bp := buf[indexQ16>>16:]
		res := silkSMULBB(int32(bp[0]), int32(silkResamplerFracFIR12[tableIndex][0]))
		res = silkSMLABB(res, int32(bp[1]), int32(silkResamplerFracFIR12[tableIndex][1]))
		res = silkSMLABB(res, int32(bp[2]), int32(silkResamplerFracFIR12[tableIndex][2]))
		res = silkSMLABB(res, int32(bp[3]), int32(silkResamplerFracFIR12[tableIndex][3]))
		res = silkSMLABB(res, int32(bp[4]), int32(silkResamplerFracFIR12[11-tableIndex][3]))
		res = silkSMLABB(res, int32(bp[5]), int32(silkResamplerFracFIR12[11-tableIndex][2]))
		res = silkSMLABB(res, int32(bp[6]), int32(silkResamplerFracFIR12[11-tableIndex][1]))
		res = silkSMLABB(res, int32(bp[7]), int32(silkResamplerFracFIR12[11-tableIndex][0]))
		out = append(out, silkSAT16(rshiftRound(int64(res), 15)))
	}
	return out
}

// downFIR mirrors silk_resampler_private_down_FIR (AR2 filter + FIR interpolation).
func (s *Resampler) downFIR(out []int16, in []int16) []int16 {
	inLen := len(in)
	buf := make([]int32, s.batchSize+s.firOrder)
	copy(buf[:s.firOrder], s.sFIR32[:s.firOrder])
	firCoefs := s.coefs[2:]

	idxInc := s.invRatioQ16
	nSamplesIn := 0
	inOff := 0
	for {
		nSamplesIn = inLen
		if nSamplesIn > s.batchSize {
			nSamplesIn = s.batchSize
		}
		s.ar2(buf[s.firOrder:], in[inOff:inOff+nSamplesIn])
		maxIndexQ16 := int32(nSamplesIn) << 16
		out = downFIRInterpol(out, buf, firCoefs, s.firOrder, s.firFracs, maxIndexQ16, idxInc)
		inOff += nSamplesIn
		inLen -= nSamplesIn
		if inLen > 1 {
			copy(buf[:s.firOrder], buf[nSamplesIn:nSamplesIn+s.firOrder])
		} else {
			break
		}
	}
	copy(s.sFIR32[:s.firOrder], buf[nSamplesIn:nSamplesIn+s.firOrder])
	return out
}

// ar2 mirrors silk_resampler_private_AR2; A_Q14 are s.coefs[0:2].
func (s *Resampler) ar2(outQ8 []int32, in []int16) {
	a0 := s.coefs[0]
	a1 := s.coefs[1]
	for k := 0; k < len(in); k++ {
		out32 := s.sIIR[0] + (int32(in[k]) << 8)
		outQ8[k] = out32
		out32 <<= 2
		s.sIIR[0] = silkSMLAWB(s.sIIR[1], out32, a0)
		s.sIIR[1] = silkSMULWB(out32, a1)
	}
}

func downFIRInterpol(out []int16, buf []int32, firCoefs []int16, firOrder, firFracs int, maxIndexQ16, idxInc int32) []int16 {
	switch firOrder {
	case resamplerDownOrderFIR0:
		for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += idxInc {
			bp := buf[indexQ16>>16:]
			interpolInd := silkSMULWB(indexQ16&0xFFFF, int16(firFracs))
			ip := firCoefs[resamplerDownOrderFIR0/2*interpolInd:]
			res := silkSMULWB(bp[0], ip[0])
			res = silkSMLAWB(res, bp[1], ip[1])
			res = silkSMLAWB(res, bp[2], ip[2])
			res = silkSMLAWB(res, bp[3], ip[3])
			res = silkSMLAWB(res, bp[4], ip[4])
			res = silkSMLAWB(res, bp[5], ip[5])
			res = silkSMLAWB(res, bp[6], ip[6])
			res = silkSMLAWB(res, bp[7], ip[7])
			res = silkSMLAWB(res, bp[8], ip[8])
			ip = firCoefs[resamplerDownOrderFIR0/2*(int32(firFracs)-1-interpolInd):]
			res = silkSMLAWB(res, bp[17], ip[0])
			res = silkSMLAWB(res, bp[16], ip[1])
			res = silkSMLAWB(res, bp[15], ip[2])
			res = silkSMLAWB(res, bp[14], ip[3])
			res = silkSMLAWB(res, bp[13], ip[4])
			res = silkSMLAWB(res, bp[12], ip[5])
			res = silkSMLAWB(res, bp[11], ip[6])
			res = silkSMLAWB(res, bp[10], ip[7])
			res = silkSMLAWB(res, bp[9], ip[8])
			out = append(out, silkSAT16(rshiftRound(int64(res), 6)))
		}
	case resamplerDownOrderFIR1:
		for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += idxInc {
			bp := buf[indexQ16>>16:]
			res := silkSMULWB(bp[0]+bp[23], firCoefs[0])
			res = silkSMLAWB(res, bp[1]+bp[22], firCoefs[1])
			res = silkSMLAWB(res, bp[2]+bp[21], firCoefs[2])
			res = silkSMLAWB(res, bp[3]+bp[20], firCoefs[3])
			res = silkSMLAWB(res, bp[4]+bp[19], firCoefs[4])
			res = silkSMLAWB(res, bp[5]+bp[18], firCoefs[5])
			res = silkSMLAWB(res, bp[6]+bp[17], firCoefs[6])
			res = silkSMLAWB(res, bp[7]+bp[16], firCoefs[7])
			res = silkSMLAWB(res, bp[8]+bp[15], firCoefs[8])
			res = silkSMLAWB(res, bp[9]+bp[14], firCoefs[9])
			res = silkSMLAWB(res, bp[10]+bp[13], firCoefs[10])
			res = silkSMLAWB(res, bp[11]+bp[12], firCoefs[11])
			out = append(out, silkSAT16(rshiftRound(int64(res), 6)))
		}
	case resamplerDownOrderFIR2:
		for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += idxInc {
			bp := buf[indexQ16>>16:]
			res := silkSMULWB(bp[0]+bp[35], firCoefs[0])
			res = silkSMLAWB(res, bp[1]+bp[34], firCoefs[1])
			res = silkSMLAWB(res, bp[2]+bp[33], firCoefs[2])
			res = silkSMLAWB(res, bp[3]+bp[32], firCoefs[3])
			res = silkSMLAWB(res, bp[4]+bp[31], firCoefs[4])
			res = silkSMLAWB(res, bp[5]+bp[30], firCoefs[5])
			res = silkSMLAWB(res, bp[6]+bp[29], firCoefs[6])
			res = silkSMLAWB(res, bp[7]+bp[28], firCoefs[7])
			res = silkSMLAWB(res, bp[8]+bp[27], firCoefs[8])
			res = silkSMLAWB(res, bp[9]+bp[26], firCoefs[9])
			res = silkSMLAWB(res, bp[10]+bp[25], firCoefs[10])
			res = silkSMLAWB(res, bp[11]+bp[24], firCoefs[11])
			res = silkSMLAWB(res, bp[12]+bp[23], firCoefs[12])
			res = silkSMLAWB(res, bp[13]+bp[22], firCoefs[13])
			res = silkSMLAWB(res, bp[14]+bp[21], firCoefs[14])
			res = silkSMLAWB(res, bp[15]+bp[20], firCoefs[15])
			res = silkSMLAWB(res, bp[16]+bp[19], firCoefs[16])
			res = silkSMLAWB(res, bp[17]+bp[18], firCoefs[17])
			out = append(out, silkSAT16(rshiftRound(int64(res), 6)))
		}
	}
	return out
}

// silkSMULBB = (int16)a * (int16)b.
func silkSMULBB(a, b int32) int32 { return int32(int16(a)) * int32(int16(b)) }

// silkSMLABB = a + (int16)b * (int16)c.
func silkSMLABB(a, b, c int32) int32 { return a + int32(int16(b))*int32(int16(c)) }

// silkSAT16 saturates to int16 range.
func silkSAT16(a int32) int16 {
	if a > 32767 {
		return 32767
	}
	if a < -32768 {
		return -32768
	}
	return int16(a)
}
