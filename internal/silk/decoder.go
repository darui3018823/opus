package silk

import (
	"fmt"
	"math"

	"github.com/darui3018823/opus/internal/entcode"
)

// Signal types per RFC 6716 Section 4.2.7.3
const (
	SignalTypeInactive = 0
	SignalTypeUnvoiced = 1
	SignalTypeVoiced   = 2
)

// Shell coder constants matching libopus silk/define.h
const (
	shellCodecFrameLength  = 16 // SHELL_CODEC_FRAME_LENGTH
	log2ShellCodecFrameLen = 4  // LOG2_SHELL_CODEC_FRAME_LENGTH
	silkMaxPulses          = 16 // SILK_MAX_PULSES
	nRateLevels            = 10 // N_RATE_LEVELS
	silkMaxLPCOrder        = 16 // MAX_LPC_ORDER
	silkLTPMemLengthMs     = 20 // LTP_MEM_LENGTH_MS
)

// ── Pulse decoding tables ────────────────────────────────────────────────────

// silkRateLevelsICDF — rate level iCDF [unvoiced/voiced][9].
var silkRateLevelsICDF = [2][9]uint8{
	{241, 190, 178, 132, 87, 74, 41, 14, 0},  // unvoiced (signalType>>1 == 0)
	{223, 193, 157, 140, 106, 57, 39, 18, 0}, // voiced   (signalType>>1 == 1)
}

// silkPulsesPerBlockICDF — pulses per shell block iCDF [rateLevelIdx][18].
var silkPulsesPerBlockICDF = [10][18]uint8{
	{125, 51, 26, 18, 15, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
	{198, 105, 45, 22, 15, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
	{213, 162, 116, 83, 59, 43, 32, 24, 18, 15, 12, 9, 7, 6, 5, 3, 2, 0},
	{239, 187, 116, 59, 28, 16, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
	{250, 229, 188, 135, 86, 51, 30, 19, 13, 10, 8, 6, 5, 4, 3, 2, 1, 0},
	{249, 235, 213, 185, 156, 128, 103, 83, 66, 53, 42, 33, 26, 21, 17, 13, 10, 0},
	{254, 249, 235, 206, 164, 118, 77, 46, 27, 16, 10, 7, 5, 4, 3, 2, 1, 0},
	{255, 253, 249, 239, 220, 191, 156, 119, 85, 57, 37, 23, 15, 10, 6, 4, 2, 0},
	{255, 253, 251, 246, 237, 223, 203, 179, 152, 124, 98, 75, 55, 40, 29, 21, 15, 0},
	{255, 254, 253, 247, 220, 162, 106, 67, 42, 28, 18, 12, 9, 6, 4, 3, 2, 0},
}

// silkSignICDF — sign iCDF [signalType*2+quantOffset][7].
// 6 rows × 7 values (7 = max absolute pulse value + 1).
var silkSignICDF = [6][7]uint8{
	{254, 49, 67, 77, 82, 93, 99},
	{198, 11, 18, 24, 31, 36, 45},
	{255, 46, 66, 78, 87, 94, 104},
	{208, 14, 21, 32, 42, 51, 66},
	{255, 94, 104, 109, 112, 115, 118},
	{248, 53, 69, 80, 88, 95, 102},
}

// silkLSBICDFDec — LSB coding iCDF (2 symbols: 0 or 1).
var silkLSBICDFDec = [2]uint8{120, 0}

// silkNLSFExtICDF — NLSF extension iCDF (7 values).
// From silk_NLSF_EXT_iCDF in libopus tables_other.c
var silkNLSFExtICDF = [7]uint8{100, 40, 16, 7, 3, 1, 0}

// Shell code tables verbatim from libopus silk/tables_pulses_per_block.c.
var silkShellCodeTable0 = [152]uint8{
	128, 0, 214, 42, 0, 235, 128, 21,
	0, 244, 184, 72, 11, 0, 248, 214,
	128, 42, 7, 0, 248, 225, 170, 80,
	25, 5, 0, 251, 236, 198, 126, 54,
	18, 3, 0, 250, 238, 211, 159, 82,
	35, 15, 5, 0, 250, 231, 203, 168,
	128, 88, 53, 25, 6, 0, 252, 238,
	216, 185, 148, 108, 71, 40, 18, 4,
	0, 253, 243, 225, 199, 166, 128, 90,
	57, 31, 13, 3, 0, 254, 246, 233,
	212, 183, 147, 109, 73, 44, 23, 10,
	2, 0, 255, 250, 240, 223, 198, 166,
	128, 90, 58, 33, 16, 6, 1, 0,
	255, 251, 244, 231, 210, 181, 146, 110,
	75, 46, 25, 12, 5, 1, 0, 255,
	253, 248, 238, 221, 196, 164, 128, 92,
	60, 35, 18, 8, 3, 1, 0, 255,
	253, 249, 242, 229, 208, 180, 146, 110,
	76, 48, 27, 14, 7, 3, 1, 0,
}

var silkShellCodeTable1 = [152]uint8{
	129, 0, 207, 50, 0, 236, 129, 20,
	0, 245, 185, 72, 10, 0, 249, 213,
	129, 42, 6, 0, 250, 226, 169, 87,
	27, 4, 0, 251, 233, 194, 130, 62,
	20, 4, 0, 250, 236, 207, 160, 99,
	47, 17, 3, 0, 255, 240, 217, 182,
	131, 81, 41, 11, 1, 0, 255, 254,
	233, 201, 159, 107, 61, 20, 2, 1,
	0, 255, 249, 233, 206, 170, 128, 86,
	50, 23, 7, 1, 0, 255, 250, 238,
	217, 186, 148, 108, 70, 39, 18, 6,
	1, 0, 255, 252, 243, 226, 200, 166,
	128, 90, 56, 30, 13, 4, 1, 0,
	255, 252, 245, 231, 209, 180, 146, 110,
	76, 47, 25, 11, 4, 1, 0, 255,
	253, 248, 237, 219, 194, 163, 128, 93,
	62, 37, 19, 8, 3, 1, 0, 255,
	254, 250, 241, 226, 205, 177, 145, 111,
	79, 51, 30, 15, 6, 2, 1, 0,
}

var silkShellCodeTable2 = [152]uint8{
	129, 0, 203, 54, 0, 234, 129, 23,
	0, 245, 184, 73, 10, 0, 250, 215,
	129, 41, 5, 0, 252, 232, 173, 86,
	24, 3, 0, 253, 240, 200, 129, 56,
	15, 2, 0, 253, 244, 217, 164, 94,
	38, 10, 1, 0, 253, 245, 226, 189,
	132, 71, 27, 7, 1, 0, 253, 246,
	231, 203, 159, 105, 56, 23, 6, 1,
	0, 255, 248, 235, 213, 179, 133, 85,
	47, 19, 5, 1, 0, 255, 254, 243,
	221, 194, 159, 117, 70, 37, 12, 2,
	1, 0, 255, 254, 248, 234, 208, 171,
	128, 85, 48, 22, 8, 2, 1, 0,
	255, 254, 250, 240, 220, 189, 149, 107,
	67, 36, 16, 6, 2, 1, 0, 255,
	254, 251, 243, 227, 201, 166, 128, 90,
	55, 29, 13, 5, 2, 1, 0, 255,
	254, 252, 246, 234, 213, 183, 147, 109,
	73, 43, 22, 10, 4, 2, 1, 0,
}

var silkShellCodeTable3 = [152]uint8{
	130, 0, 200, 58, 0, 231, 130, 26,
	0, 244, 184, 76, 12, 0, 249, 214,
	130, 43, 6, 0, 252, 232, 173, 87,
	24, 3, 0, 253, 241, 203, 131, 56,
	14, 2, 0, 254, 246, 221, 167, 94,
	35, 8, 1, 0, 254, 249, 232, 193,
	130, 65, 23, 5, 1, 0, 255, 251,
	239, 211, 162, 99, 45, 15, 4, 1,
	0, 255, 251, 243, 223, 186, 131, 74,
	33, 11, 3, 1, 0, 255, 252, 245,
	230, 202, 158, 105, 57, 24, 8, 2,
	1, 0, 255, 253, 247, 235, 214, 179,
	132, 84, 44, 19, 7, 2, 1, 0,
	255, 254, 250, 240, 223, 196, 159, 112,
	69, 36, 15, 6, 2, 1, 0, 255,
	254, 253, 245, 231, 209, 176, 136, 93,
	55, 27, 11, 3, 2, 1, 0, 255,
	254, 253, 252, 239, 221, 194, 158, 117,
	76, 42, 18, 4, 3, 2, 1, 0,
}

// silkShellCodeTableOffsets — byte offsets into each shell code table for total=0..16.
var silkShellCodeTableOffsets = [17]uint8{
	0, 0, 2, 5, 9, 14, 20, 27,
	35, 44, 54, 65, 77, 90, 104, 119,
	135,
}

// ── silk_LSFCosTab_FIX_Q12: 129-element cosine table ────────────────────────
// silk_LSFCosTab_FIX_Q12[i] = round(2^12 * 2 * cos(pi * i / 128)) for i=0..128.
// From libopus silk/table_LSF_cos.c.
var silkLSFCosTabFixQ12 = [129]int32{
	8192, 8190, 8182, 8170, 8152, 8130, 8104, 8072,
	8034, 7994, 7946, 7896, 7840, 7778, 7714, 7644,
	7568, 7490, 7406, 7318, 7226, 7128, 7026, 6922,
	6812, 6698, 6580, 6458, 6332, 6204, 6070, 5934,
	5792, 5648, 5502, 5352, 5198, 5040, 4880, 4718,
	4552, 4382, 4212, 4038, 3862, 3684, 3502, 3320,
	3136, 2948, 2760, 2570, 2378, 2186, 1990, 1794,
	1598, 1400, 1202, 1002, 802, 602, 402, 202,
	0, -202, -402, -602, -802, -1002, -1202, -1400,
	-1598, -1794, -1990, -2186, -2378, -2570, -2760, -2948,
	-3136, -3320, -3502, -3684, -3862, -4038, -4212, -4382,
	-4552, -4718, -4880, -5040, -5198, -5352, -5502, -5648,
	-5792, -5934, -6070, -6204, -6332, -6458, -6580, -6698,
	-6812, -6922, -7026, -7128, -7226, -7318, -7406, -7490,
	-7568, -7644, -7714, -7778, -7840, -7896, -7946, -7994,
	-8034, -8072, -8104, -8130, -8152, -8170, -8182, -8190,
	-8192,
}

// Ordering arrays used by silk_NLSF2A for order 10 and 16.
// From libopus silk/NLSF2A.c
var nlsf2AOrdering10 = [10]int{0, 9, 6, 3, 4, 5, 8, 1, 2, 7}
var nlsf2AOrdering16 = [16]int{0, 15, 8, 7, 4, 11, 12, 3, 2, 13, 10, 5, 6, 9, 14, 1}

// ── Decoder state ──────────────────────────────────────────────────────────────

// Decoder represents a SILK decoder instance.
type Decoder struct {
	sampleRate int // 8000, 12000, 16000, 24000
	frameSize  int // samples per 20ms frame
	channels   int // 1 or 2
	lpcOrder   int // LPC order (10 for NB, 16 for WB)
	fsKHz      int // sample rate in kHz
	nSubframes int // subframes per frame (4)
	subfrmLen  int // subframe length

	// Decoder state (persists across frames)
	prevNLSFQ15    []int16 // previous NLSF in Q15
	lagPrev        int     // previous pitch lag
	prevLagIndex   int     // previous entropy-coded pitch lag index
	prevGainQ16    int32   // previous gain (Q16) for delta coding
	prevGainIndex  int8    // previous gain index for conditional coding
	prevSignalType int     // previous signal type
	firstFrame     bool    // true until the first decoded frame after reset
	lastFinalRange uint32

	// LPC synthesis state: last MAX_LPC_ORDER samples
	lpcState []int32 // Q14

	// LTP (long-term prediction) state: decoded PCM history, one sample per entry.
	ltpState []int32 // Q0, length LTP_MEM_LENGTH_MS * fsKHz

	// Random seed for excitation
	randSeed int32

	// PLC state
	plcCount        int
	prevLPCQ12      []int16
	prevOutput      []int32 // Q14
	lastFrameSilent bool    // last decoded frame was a digital-silence packet

	// Stereo packets code mid and side as separate SILK channel states.
	side                 *Decoder
	stereoPredPrevQ13    [2]int32
	stereoMid            [2]int16
	stereoSide           [2]int16
	prevDecodeOnlyMiddle bool

	trace *decodeTrace

	lastTellBeforeSigns int // diagnostic: ECTell() just before decodeSigns
}

type decodeTrace struct {
	VADFlags  [][]uint32
	LBRRFlags []uint32
	Frames    []frameTrace
}

type frameTrace struct {
	Channel         int
	VADFlag         uint32
	ConditionalGain bool
	SignalType      int
	QuantOffset     int
	RawGainIndices  []int
	AbsGainIndices  []int
	GainsQ16        []int32
	NLSFIndices     []int
	NLSFQ15         []int16
	InterpFactor    int
	PredCoef0Q12    []int16
	PredCoef1Q12    []int16
	PitchLags       []int
	LTPCoefQ14      []int16 // flattened nSubframes*5
	LTPScaleQ14     int16
	Seed            int32
	RateLevelIdx    int
	SumPulses       []int
	Pulses          []int16
	ExcQ14          []int32
	TellAfterPulses int
	RngAfterPulses  uint32
	TellBeforePulse int
	TellBeforeSigns int
}

// NewDecoder creates a new SILK decoder with 20ms frame size.
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	return NewDecoderWithFrameMs(sampleRate, channels, 20)
}

// NewDecoderWithFrameMs creates a new SILK decoder with the given frame duration in ms.
// frameMs must be 10 or 20 (40ms and 60ms are handled by calling DecodeMulti with nFrames=2 or 3).
func NewDecoderWithFrameMs(sampleRate, channels, frameMs int) (*Decoder, error) {
	if sampleRate != 8000 && sampleRate != 12000 && sampleRate != 16000 && sampleRate != 24000 {
		return nil, fmt.Errorf("invalid sample rate: %d (must be 8000, 12000, 16000, or 24000)", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("invalid channels: %d (must be 1 or 2)", channels)
	}
	if frameMs != 10 && frameMs != 20 {
		return nil, fmt.Errorf("invalid frame duration: %d ms (must be 10 or 20)", frameMs)
	}

	fsKHz := sampleRate / 1000
	lpcOrder := 10
	if fsKHz >= 16 {
		lpcOrder = 16
	}

	// For 10ms: 2 subframes; for 20ms: 4 subframes
	var nSubframes int
	if frameMs == 10 {
		nSubframes = 2
	} else {
		nSubframes = 4
	}
	frameSize := sampleRate * frameMs / 1000
	subfrmLen := frameSize / nSubframes
	ltpMemLen := silkLTPMemLengthMs * fsKHz

	d := &Decoder{
		sampleRate:     sampleRate,
		frameSize:      frameSize,
		channels:       channels,
		lpcOrder:       lpcOrder,
		fsKHz:          fsKHz,
		nSubframes:     nSubframes,
		subfrmLen:      subfrmLen,
		prevNLSFQ15:    make([]int16, lpcOrder),
		lagPrev:        100,
		prevLagIndex:   0,
		prevGainQ16:    65536,
		prevGainIndex:  10,
		prevSignalType: SignalTypeUnvoiced,
		firstFrame:     true,
		lpcState:       make([]int32, silkMaxLPCOrder),
		ltpState:       make([]int32, ltpMemLen),
		randSeed:       7818,
		prevLPCQ12:     make([]int16, lpcOrder),
		prevOutput:     make([]int32, frameSize),
	}

	// Initialize prevNLSFQ15 to evenly spaced values
	for i := 0; i < lpcOrder; i++ {
		d.prevNLSFQ15[i] = int16((float64(i+1) / float64(lpcOrder+1)) * 32768.0)
	}

	if channels == 2 {
		side, err := NewDecoderWithFrameMs(sampleRate, 1, frameMs)
		if err != nil {
			return nil, err
		}
		d.side = side
	}

	return d, nil
}

// SetFrameMs reconfigures the decoder's Opus frame duration (10 or 20 ms)
// WITHOUT resetting synthesis state. libopus uses a single SILK decoder per
// channel whose synthesis state (LPC/LTP history, previous gain, random seed,
// previous NLSF) carries across packets regardless of frame size; only the
// per-packet frame geometry (frameSize / nSubframes / subfrmLen) changes. The
// state buffers (lpcState, ltpState) are sized by sample rate, not frame size,
// so they are preserved here. This is required for bit-exact decoding of streams
// that switch between 10 ms and 20 ms configs at the same internal rate (e.g.
// NB config 1 -> config 0), where keeping separate 10 ms / 20 ms decoder
// instances would discontinue the synthesis state at every switch.
func (d *Decoder) SetFrameMs(frameMs int) {
	if frameMs != 10 && frameMs != 20 {
		return
	}
	nSubframes := 4
	if frameMs == 10 {
		nSubframes = 2
	}
	d.nSubframes = nSubframes
	d.frameSize = d.sampleRate * frameMs / 1000
	d.subfrmLen = d.frameSize / nSubframes
	if len(d.prevOutput) != d.frameSize {
		d.prevOutput = make([]int32, d.frameSize)
	}
	if d.side != nil {
		d.side.SetFrameMs(frameMs)
	}
}

// Decode decodes all SILK frames from a packet (one range-coded stream).
// nFrames specifies how many SILK frames are in this packet (1, 2, or 3).
// The packet must include the SILK payload (after the TOC byte has been stripped).
func (d *Decoder) Decode(packet []byte) ([]float64, error) {
	return d.DecodeMulti(packet, 1)
}

// FrameSize returns the number of internal-rate samples per decoded SILK frame.
func (d *Decoder) FrameSize() int {
	return d.frameSize
}

// DecodeMulti decodes nFrames SILK frames from a single range-coded packet.
// In SILK mode, all frames are encoded in a single bitstream:
//   - VAD flags for all frames (1 bit each)
//   - LBRR flag for channel (1 bit)
//   - Frame data for each frame sequentially
func (d *Decoder) DecodeMulti(packet []byte, nFrames int) ([]float64, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("empty packet")
	}

	if nFrames < 1 {
		nFrames = 1
	}

	if d.channels == 2 {
		return d.decodeMultiStereo(packet, nFrames)
	}

	// Single-byte silence packet
	if len(packet) == 1 && packet[0] == 0x00 {
		d.lastFinalRange = 0
		d.noteDigitalSilence()
		result := make([]float64, d.frameSize*d.channels*nFrames)
		return result, nil
	}

	// Need at least 2 bytes for range decoder
	if len(packet) < 2 {
		return d.concealPacketLoss()
	}

	dec := entcode.NewDecoder(packet)
	if dec.Error() != nil {
		return d.concealPacketLoss()
	}
	pcm, err := d.decodeMultiMonoEC(dec, nFrames)
	d.lastFinalRange = dec.GetRng()
	return pcm, err
}

// DecodeMultiWithDecoder decodes nFrames SILK frames from an already-initialised
// range decoder, used by the hybrid path where SILK and CELT share one entropy
// stream. The caller is responsible for the decoder lifetime; this method does
// not create or finalise it. The result is interleaved SILK PCM at the internal
// rate (one frame's worth per SILK frame).
func (d *Decoder) DecodeMultiWithDecoder(dec *entcode.Decoder, nFrames int) ([]float64, error) {
	if nFrames < 1 {
		nFrames = 1
	}
	var pcm []float64
	var err error
	if d.channels == 2 {
		pcm, err = d.decodeMultiStereoEC(dec, nFrames)
	} else {
		pcm, err = d.decodeMultiMonoEC(dec, nFrames)
	}
	d.lastFinalRange = dec.GetRng()
	return pcm, err
}

// LastFinalRange returns the entropy decoder range after the most recent
// standalone or shared-stream decode.
func (d *Decoder) LastFinalRange() uint32 { return d.lastFinalRange }

// Pitch returns the most recent SILK pitch period at the internal sample rate.
func (d *Decoder) Pitch() int { return d.lagPrev }

// DecodeFEC decodes LBRR data carried in the packet following a loss.
// nFrames is the number of 10 or 20 ms SILK frames represented by the packet.
// If no LBRR frame is present for a slot, packet-loss concealment is used for
// that slot, matching libopus decode_fec behaviour.
func (d *Decoder) DecodeFEC(packet []byte, nFrames int) ([]float64, error) {
	if nFrames < 1 {
		nFrames = 1
	}
	if len(packet) < 2 {
		d.lastFinalRange = 0
		return d.concealFECFrames(nFrames)
	}
	dec := entcode.NewDecoder(packet)
	if dec.Error() != nil {
		d.lastFinalRange = 0
		return d.concealFECFrames(nFrames)
	}
	if d.channels == 2 {
		pcm, err := d.decodeFECStereo(dec, nFrames)
		d.lastFinalRange = dec.GetRng()
		return pcm, err
	}

	// Regular-frame VAD flags precede the packet-level LBRR flag.
	for i := 0; i < nFrames; i++ {
		_ = dec.DecodeBitLogp(1)
	}
	lbrrFlag := dec.DecodeBitLogp(1)
	if !lbrrFlag {
		pcm, err := d.concealFECFrames(nFrames)
		d.lastFinalRange = dec.GetRng()
		return pcm, err
	}
	mask := 1
	if nFrames == 2 {
		mask = dec.DecodeIcdf(silkLBRRFlags2ICDF[:], 8) + 1
	} else if nFrames >= 3 {
		mask = dec.DecodeIcdf(silkLBRRFlags3ICDF[:], 8) + 1
	}

	pcm, err := d.decodeFECChannel(dec, nFrames, mask)
	d.lastFinalRange = dec.GetRng()
	return pcm, err
}

// DecodePLC conceals nFrames consecutive lost SILK frames using the current
// synthesis history. Stereo decoding conceals the mid and side states
// independently before applying the retained M/S predictor.
func (d *Decoder) DecodePLC(nFrames int) ([]float64, error) {
	if nFrames < 1 {
		nFrames = 1
	}
	if d.channels == 1 {
		return d.concealFECFrames(nFrames)
	}
	if d.side == nil {
		return nil, fmt.Errorf("missing SILK side-channel decoder")
	}

	out := make([]float64, 0, d.frameSize*nFrames*2)
	for frame := 0; frame < nFrames; frame++ {
		mid, err := d.concealPacketLossMono()
		if err != nil {
			return nil, err
		}
		side, err := d.side.concealPacketLoss()
		if err != nil {
			return nil, err
		}
		out = append(out, d.stereoMSToLR(mid, side, d.stereoPredPrevQ13)...)
		d.prevDecodeOnlyMiddle = false
	}
	return out, nil
}

func (d *Decoder) decodeFECChannel(dec *entcode.Decoder, nFrames, mask int) ([]float64, error) {
	allPCM := make([]float64, 0, d.frameSize*nFrames)
	prevPresent := false
	for i := 0; i < nFrames; i++ {
		present := mask&(1<<uint(i)) != 0
		if !present {
			pcm, err := d.concealPacketLoss()
			if err != nil {
				return nil, err
			}
			allPCM = append(allPCM, pcm...)
			prevPresent = false
			continue
		}
		pcm, err := d.decodeFrame(dec, 1, prevPresent)
		if err != nil {
			return nil, err
		}
		allPCM = append(allPCM, pcm...)
		prevPresent = true
	}
	return allPCM, nil
}

func (d *Decoder) decodeFECStereo(dec *entcode.Decoder, nFrames int) ([]float64, error) {
	if d.side == nil {
		return nil, fmt.Errorf("missing SILK side-channel decoder")
	}
	lbrrFlags := [2]bool{}
	for ch := 0; ch < 2; ch++ {
		for i := 0; i < nFrames; i++ {
			_ = dec.DecodeBitLogp(1)
		}
		lbrrFlags[ch] = dec.DecodeBitLogp(1)
	}
	masks := [2]int{}
	for ch := 0; ch < 2; ch++ {
		if !lbrrFlags[ch] {
			continue
		}
		if nFrames == 1 {
			masks[ch] = 1
		} else if nFrames == 2 {
			masks[ch] = dec.DecodeIcdf(silkLBRRFlags2ICDF[:], 8) + 1
		} else {
			masks[ch] = dec.DecodeIcdf(silkLBRRFlags3ICDF[:], 8) + 1
		}
	}
	out := make([]float64, 0, d.frameSize*nFrames*2)
	for frame := 0; frame < nFrames; frame++ {
		midPresent := masks[0]&(1<<uint(frame)) != 0
		sidePresent := masks[1]&(1<<uint(frame)) != 0
		predQ13 := d.stereoPredPrevQ13
		decodeOnlyMiddle := false
		if midPresent {
			predQ13 = decodeStereoPredQ13(dec)
			if !sidePresent {
				decodeOnlyMiddle = dec.DecodeIcdf(silkStereoOnlyCodeMidICDF[:], 8) != 0
			}
		}

		var mid []float64
		var err error
		if midPresent {
			mid, err = d.decodeFrameMono(dec, 1, frame > 0 && masks[0]&(1<<uint(frame-1)) != 0)
		} else {
			mid, err = d.concealPacketLossMono()
		}
		if err != nil {
			return nil, err
		}

		side := make([]float64, d.frameSize)
		if !decodeOnlyMiddle {
			if sidePresent {
				if d.prevDecodeOnlyMiddle {
					d.side.Reset()
				}
				side, err = d.side.decodeFrame(dec, 1, frame > 0 && masks[1]&(1<<uint(frame-1)) != 0)
			} else {
				side, err = d.side.concealPacketLoss()
			}
			if err != nil {
				return nil, err
			}
		}

		out = append(out, d.stereoMSToLR(mid, side, predQ13)...)
		d.prevDecodeOnlyMiddle = decodeOnlyMiddle
	}
	return out, nil
}

func (d *Decoder) concealPacketLossMono() ([]float64, error) {
	channels := d.channels
	d.channels = 1
	pcm, err := d.concealPacketLoss()
	d.channels = channels
	return pcm, err
}

func (d *Decoder) concealFECFrames(nFrames int) ([]float64, error) {
	allPCM := make([]float64, 0, d.frameSize*nFrames)
	for i := 0; i < nFrames; i++ {
		pcm, err := d.concealPacketLoss()
		if err != nil {
			return nil, err
		}
		allPCM = append(allPCM, pcm...)
	}
	return allPCM, nil
}

// decodeMultiMonoEC decodes the mono SILK frames from a shared range decoder.
func (d *Decoder) decodeMultiMonoEC(dec *entcode.Decoder, nFrames int) ([]float64, error) {
	// Per libopus dec_API.c: decode VAD flags for all frames first, then LBRR flag
	vadFlags := make([]uint32, nFrames)
	for i := 0; i < nFrames; i++ {
		if dec.DecodeBitLogp(1) {
			vadFlags[i] = 1
		}
	}
	// LBRR flag (1 bit for mono channel)
	lbrrFlag := dec.DecodeBitLogp(1)
	if d.trace != nil {
		vadCopy := append([]uint32(nil), vadFlags...)
		d.trace.VADFlags = append(d.trace.VADFlags, vadCopy)
		if lbrrFlag {
			d.trace.LBRRFlags = append(d.trace.LBRRFlags, 1)
		} else {
			d.trace.LBRRFlags = append(d.trace.LBRRFlags, 0)
		}
	}
	lbrrMask := 0
	if lbrrFlag && nFrames > 1 {
		if nFrames == 2 {
			lbrrMask = dec.DecodeIcdf(silkLBRRFlags2ICDF[:], 8) + 1
		} else {
			lbrrMask = dec.DecodeIcdf(silkLBRRFlags3ICDF[:], 8) + 1
		}
	} else if lbrrFlag {
		lbrrMask = 1
	}
	if lbrrMask != 0 {
		if err := d.consumeLBRRFrames(dec, nFrames, lbrrMask); err != nil {
			return d.concealPacketLoss()
		}
	}

	// Decode each frame sequentially
	var allPCM []float64
	for i := 0; i < nFrames; i++ {
		pcm, err := d.decodeFrame(dec, vadFlags[i], i > 0)
		if err != nil {
			// PLC for this frame
			pcm, _ = d.concealPacketLoss()
		}
		allPCM = append(allPCM, pcm...)
	}
	return allPCM, nil
}

// consumeLBRRFrames advances dec over the redundant frame bodies that precede
// the regular SILK frames. LBRR indices use their own decoder-state timeline:
// the first frame in each contiguous run is independent, and later frames in
// that run are conditional on the preceding LBRR frame. Decode them with a
// temporary SILK decoder so their gain/pitch history is available while the
// regular decoder's synthesis and predictor state remains untouched.
func (d *Decoder) consumeLBRRFrames(dec *entcode.Decoder, nFrames, mask int) error {
	frameMs := d.frameSize * 1000 / d.sampleRate
	tmp, err := NewDecoderWithFrameMs(d.sampleRate, 1, frameMs)
	if err != nil {
		return err
	}
	prevPresent := false
	for i := 0; i < nFrames; i++ {
		present := mask&(1<<uint(i)) != 0
		if !present {
			prevPresent = false
			continue
		}
		if _, err := tmp.decodeFrame(dec, 1, prevPresent); err != nil {
			return err
		}
		prevPresent = true
	}
	return nil
}

func (d *Decoder) decodeMultiStereo(packet []byte, nFrames int) ([]float64, error) {
	if d.side == nil {
		return nil, fmt.Errorf("missing SILK side-channel decoder")
	}

	if len(packet) == 1 && packet[0] == 0x00 {
		d.noteDigitalSilence()
		d.side.noteDigitalSilence()
		return make([]float64, d.frameSize*d.channels*nFrames), nil
	}
	if len(packet) < 2 {
		return d.concealPacketLoss()
	}

	dec := entcode.NewDecoder(packet)
	if dec.Error() != nil {
		return d.concealPacketLoss()
	}
	return d.decodeMultiStereoEC(dec, nFrames)
}

// decodeMultiStereoEC decodes the stereo SILK frames from a shared range decoder.
func (d *Decoder) decodeMultiStereoEC(dec *entcode.Decoder, nFrames int) ([]float64, error) {
	if d.side == nil {
		return nil, fmt.Errorf("missing SILK side-channel decoder")
	}

	vadFlags := [2][]uint32{
		make([]uint32, nFrames),
		make([]uint32, nFrames),
	}
	lbrrFlags := [2]uint32{}
	for ch := 0; ch < 2; ch++ {
		for i := 0; i < nFrames; i++ {
			if dec.DecodeBitLogp(1) {
				vadFlags[ch][i] = 1
			}
		}
		if dec.DecodeBitLogp(1) {
			lbrrFlags[ch] = 1
		}
	}
	if d.trace != nil {
		d.trace.VADFlags = append(d.trace.VADFlags,
			append([]uint32(nil), vadFlags[0]...),
			append([]uint32(nil), vadFlags[1]...),
		)
		d.trace.LBRRFlags = append(d.trace.LBRRFlags, lbrrFlags[0], lbrrFlags[1])
	}

	lbrrMasks := [2]int{}
	for ch := 0; ch < 2; ch++ {
		if lbrrFlags[ch] == 0 {
			continue
		}
		if nFrames == 1 {
			lbrrMasks[ch] = 1
		} else if nFrames == 2 {
			lbrrMasks[ch] = dec.DecodeIcdf(silkLBRRFlags2ICDF[:], 8) + 1
		} else {
			lbrrMasks[ch] = dec.DecodeIcdf(silkLBRRFlags3ICDF[:], 8) + 1
		}
	}
	if lbrrMasks[0] != 0 || lbrrMasks[1] != 0 {
		if err := d.consumeStereoLBRRFrames(dec, nFrames, lbrrMasks); err != nil {
			return nil, err
		}
	}

	var allPCM []float64
	for i := 0; i < nFrames; i++ {
		predQ13 := decodeStereoPredQ13(dec)
		decodeOnlyMiddle := false
		if vadFlags[1][i] == 0 {
			decodeOnlyMiddle = dec.DecodeIcdf(silkStereoOnlyCodeMidICDF[:], 8) != 0
		}

		mid, err := d.decodeFrameMono(dec, vadFlags[0][i], i > 0)
		if err != nil {
			mid, _ = d.concealPacketLoss()
			if len(mid) == d.frameSize*2 {
				tmp := make([]float64, d.frameSize)
				for j := range tmp {
					tmp[j] = mid[j*2]
				}
				mid = tmp
			}
		}

		side := make([]float64, d.frameSize)
		if !decodeOnlyMiddle {
			if d.prevDecodeOnlyMiddle {
				d.side.Reset()
			}
			sideConditional := i > 0 && !d.prevDecodeOnlyMiddle
			pcm, err := d.side.decodeFrame(dec, vadFlags[1][i], sideConditional)
			if err == nil {
				copy(side, pcm)
			}
		}

		allPCM = append(allPCM, d.stereoMSToLR(mid, side, predQ13)...)
		d.prevDecodeOnlyMiddle = decodeOnlyMiddle
	}
	return allPCM, nil
}

func (d *Decoder) consumeStereoLBRRFrames(dec *entcode.Decoder, nFrames int, masks [2]int) error {
	frameMs := d.frameSize * 1000 / d.sampleRate
	mid, err := NewDecoderWithFrameMs(d.sampleRate, 1, frameMs)
	if err != nil {
		return err
	}
	side, err := NewDecoderWithFrameMs(d.sampleRate, 1, frameMs)
	if err != nil {
		return err
	}
	prevPresent := [2]bool{}
	for frame := 0; frame < nFrames; frame++ {
		midPresent := masks[0]&(1<<uint(frame)) != 0
		sidePresent := masks[1]&(1<<uint(frame)) != 0
		if midPresent {
			_ = decodeStereoPredQ13(dec)
			if !sidePresent {
				_ = dec.DecodeIcdf(silkStereoOnlyCodeMidICDF[:], 8)
			}
			if _, err := mid.decodeFrame(dec, 1, prevPresent[0]); err != nil {
				return err
			}
		}
		if sidePresent {
			if _, err := side.decodeFrame(dec, 1, prevPresent[1]); err != nil {
				return err
			}
		}
		prevPresent[0] = midPresent
		prevPresent[1] = sidePresent
	}
	return nil
}

func (d *Decoder) decodeFrameMono(dec *entcode.Decoder, vadFlag uint32, conditionalGain bool) ([]float64, error) {
	channels := d.channels
	d.channels = 1
	pcm, err := d.decodeFrame(dec, vadFlag, conditionalGain)
	d.channels = channels
	return pcm, err
}

func decodeStereoPredQ13(dec *entcode.Decoder) [2]int32 {
	var pred [2]int32
	var ix [2][3]int

	n := dec.DecodeIcdf(silkStereoPredJointICDF[:], 8)
	ix[0][2] = n / 5
	ix[1][2] = n - 5*ix[0][2]
	for i := 0; i < 2; i++ {
		ix[i][0] = dec.DecodeIcdf(silkUniform3ICDF[:], 8)
		ix[i][1] = dec.DecodeIcdf(silkUniform5ICDF[:], 8)
	}

	for i := 0; i < 2; i++ {
		idx := ix[i][0] + 3*ix[i][2]
		if idx < 0 {
			idx = 0
		}
		if idx >= len(silkStereoPredQuantQ13)-1 {
			idx = len(silkStereoPredQuantQ13) - 2
		}
		lowQ13 := int32(silkStereoPredQuantQ13[idx])
		stepQ13 := int32((int64(int32(silkStereoPredQuantQ13[idx+1])-lowQ13) * 6554) >> 16)
		pred[i] = lowQ13 + stepQ13*int32(2*ix[i][1]+1)
	}
	pred[0] -= pred[1]
	return pred
}

func floatToInt16Sample(v float64) int16 {
	q := int32(math.Round(v * 32768.0))
	if q > 32767 {
		q = 32767
	} else if q < -32768 {
		q = -32768
	}
	return int16(q)
}

func clamp16(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func rshiftRound(v int64, shift uint) int32 {
	if shift == 0 {
		return int32(v)
	}
	return int32((v + (int64(1) << (shift - 1))) >> shift)
}

func silkRShiftRound(v int64, shift int) int32 {
	if shift <= 0 {
		return int32(v)
	}
	if shift == 1 {
		return int32((v >> 1) + (v & 1))
	}
	return int32(((v >> (shift - 1)) + 1) >> 1)
}

func (d *Decoder) stereoMSToLR(mid, side []float64, predQ13 [2]int32) []float64 {
	n := d.frameSize
	x1 := make([]int16, n+2)
	x2 := make([]int16, n+2)
	copy(x1[:2], d.stereoMid[:])
	copy(x2[:2], d.stereoSide[:])
	for i := 0; i < n; i++ {
		if i < len(mid) {
			x1[i+2] = floatToInt16Sample(mid[i])
		}
		if i < len(side) {
			x2[i+2] = floatToInt16Sample(side[i])
		}
	}
	d.stereoMid[0], d.stereoMid[1] = x1[n], x1[n+1]
	d.stereoSide[0], d.stereoSide[1] = x2[n], x2[n+1]

	pred0Q13 := d.stereoPredPrevQ13[0]
	pred1Q13 := d.stereoPredPrevQ13[1]
	interpLen := 8 * d.fsKHz
	if interpLen > n {
		interpLen = n
	}
	denomQ16 := int32((1 << 16) / (8 * d.fsKHz))
	delta0Q13 := rshiftRound(int64(predQ13[0]-d.stereoPredPrevQ13[0])*int64(denomQ16), 16)
	delta1Q13 := rshiftRound(int64(predQ13[1]-d.stereoPredPrevQ13[1])*int64(denomQ16), 16)

	for i := 0; i < n; i++ {
		if i < interpLen {
			pred0Q13 += delta0Q13
			pred1Q13 += delta1Q13
		} else {
			pred0Q13 = predQ13[0]
			pred1Q13 = predQ13[1]
		}

		sumQ11 := int64(int32(x1[i])+int32(x1[i+2])+(int32(x1[i+1])<<1)) << 9
		sideQ8 := int64(int32(x2[i+1])) << 8
		sumQ8 := sideQ8 + ((sumQ11 * int64(pred0Q13)) >> 16)
		sumQ8 += ((int64(int32(x1[i+1])) << 11) * int64(pred1Q13)) >> 16
		x2[i+1] = clamp16(rshiftRound(sumQ8, 8))
	}
	d.stereoPredPrevQ13 = predQ13

	out := make([]float64, n*2)
	for i := 0; i < n; i++ {
		l := clamp16(int32(x1[i+1]) + int32(x2[i+1]))
		r := clamp16(int32(x1[i+1]) - int32(x2[i+1]))
		out[i*2] = float64(l) / 32768.0
		out[i*2+1] = float64(r) / 32768.0
	}
	return out
}

// decodeFrame performs the full SILK frame decode per RFC 6716 §4.2 / libopus dec_API.c.
// vadFlag: 1 if frame is voice-active, 0 if not (pre-decoded from packet header).
// conditionalGain is true for the second and later SILK frames in the same range stream.
func (d *Decoder) decodeFrame(dec *entcode.Decoder, vadFlag uint32, conditionalGain bool) ([]float64, error) {

	// ── 2. Signal type + quantization offset type ────────────────────────────
	// From decode_indices.c: use VAD flag to select table
	// VAD=1 (or LBRR decode mode): silk_type_offset_VAD_iCDF (4 symbols) + 2
	// VAD=0: silk_type_offset_no_VAD_iCDF (2 symbols)
	var typeIx int
	if vadFlag != 0 {
		typeIx = dec.DecodeIcdf(silkTypeOffsetVADICDF[:], 8) + 2
	} else {
		typeIx = dec.DecodeIcdf(silkTypeOffsetNoVADICDF[:], 8)
	}
	// typeIx: 0=Inactive/Low, 1=Inactive/High, 2=Unvoiced/Low, 3=Unvoiced/High, 4=Voiced/Low, 5=Voiced/High
	signalType := typeIx >> 1 // 0=Inactive, 1=Unvoiced, 2=Voiced
	quantOffset := typeIx & 1 // 0=Low, 1=High

	// ── 3. Decode gains ──────────────────────────────────────────────────────
	// silk_gains_dequant from libopus silk/gain_quant.c
	// First subframe: MSBs from gain_iCDF[signalType], then 3 LSBs from uniform8.
	gainsQ16 := make([]int32, d.nSubframes)
	rawGainIndices := make([]int, d.nSubframes)
	absGainIndices := make([]int, d.nSubframes)
	// libopus indexes silk_gain_iCDF by signalType directly:
	// 0=inactive, 1=unvoiced, 2=voiced.
	stIdx := signalType
	if stIdx >= len(silkGainICDF) {
		stIdx = len(silkGainICDF) - 1
	}

	prevInd := int(d.prevGainIndex)
	if conditionalGain {
		// Conditional: delta-coded using silk_delta_gain_iCDF.
		// ind[0] = icdf_result, then apply double-step formula.
		deltaIdx := dec.DecodeIcdf(silkDeltaGainICDF[:], 8)
		rawGainIndices[0] = deltaIdx
		indTmp := deltaIdx + MinDeltaGainQuant
		dblStepThresh := 2*MaxDeltaGainQuant - NLevelsQGain + prevInd
		if indTmp > dblStepThresh {
			prevInd += 2*indTmp - dblStepThresh
		} else {
			prevInd += indTmp
		}
	} else {
		// Independent: gain_iCDF gives MSBs, uniform8 gives the 3 LSBs.
		// From libopus: ind[0] = LSHIFT(gain_iCDF, 3) + uniform8.
		gainMSB := dec.DecodeIcdf(silkGainICDF[stIdx][:], 8)
		gainLSB := dec.DecodeIcdf(silkUniform8ICDF[:], 8)
		prevInd = gainMSB*8 + gainLSB
		rawGainIndices[0] = prevInd
	}
	// Clamp to [0, N_LEVELS_QGAIN-1]
	if !conditionalGain && prevInd < int(d.prevGainIndex)-16 {
		prevInd = int(d.prevGainIndex) - 16
	}
	if prevInd < 0 {
		prevInd = 0
	}
	if prevInd >= NLevelsQGain {
		prevInd = NLevelsQGain - 1
	}
	absGainIndices[0] = prevInd
	gainsQ16[0] = silkGainDequantQ16(prevInd)

	for sf := 1; sf < d.nSubframes; sf++ {
		deltaIdx := dec.DecodeIcdf(silkDeltaGainICDF[:], 8)
		rawGainIndices[sf] = deltaIdx
		indTmp := deltaIdx + MinDeltaGainQuant // indTmp = ind[sf] + MIN_DELTA_GAIN_QUANT

		// double_step_size_threshold = 2*MAX_DELTA_GAIN_QUANT - N_LEVELS_QGAIN + prev_ind
		dblStepThresh := 2*MaxDeltaGainQuant - NLevelsQGain + prevInd
		if indTmp > dblStepThresh {
			prevInd += 2*indTmp - dblStepThresh
		} else {
			prevInd += indTmp
		}

		if prevInd < 0 {
			prevInd = 0
		}
		if prevInd >= NLevelsQGain {
			prevInd = NLevelsQGain - 1
		}
		absGainIndices[sf] = prevInd
		gainsQ16[sf] = silkGainDequantQ16(prevInd)
	}
	d.prevGainIndex = int8(prevInd)
	if d.trace != nil {
		d.trace.Frames = append(d.trace.Frames, frameTrace{
			VADFlag:         vadFlag,
			ConditionalGain: conditionalGain,
			SignalType:      signalType,
			QuantOffset:     quantOffset,
			RawGainIndices:  append([]int(nil), rawGainIndices...),
			AbsGainIndices:  append([]int(nil), absGainIndices...),
			GainsQ16:        append([]int32(nil), gainsQ16...),
		})
	}

	// ── 4. Decode NLSF indices ───────────────────────────────────────────────
	cb := getNLSFCB(d.lpcOrder)
	nlsfQ15, nlsfIndices, err := d.decodeNLSF(dec, cb, signalType)
	if err != nil {
		return nil, err
	}

	// NLSF interpolation factor (0..4 for 4-subframe frames)
	interpFactor := 4 // default: no interpolation (use current frame NLSF)
	if d.nSubframes == 4 {
		interpFactor = dec.DecodeIcdf(silkNLSFInterpFactorICDF[:], 8)
	}
	if d.firstFrame {
		interpFactor = 4
	}

	// Build LPC coefficients using libopus's 2-set approach from decode_parameters.c:
	//   PredCoef_Q12[0] = nlsfToLPC(interpolated NLSF)  → subframes 0,1
	//   PredCoef_Q12[1] = nlsfToLPC(current NLSF)       → subframes 2,3
	// NLSFInterpCoef_Q2 (interpFactor) range 0..4; 4 = no interpolation.
	var lpcSets [2][]int16
	lpcSets[1] = nlsfToLPCLibopus(nlsfQ15, d.lpcOrder)

	if interpFactor < 4 {
		interpNLSF := make([]int16, d.lpcOrder)
		for i := 0; i < d.lpcOrder; i++ {
			prev := int32(d.prevNLSFQ15[i])
			curr := int32(nlsfQ15[i])
			interpNLSF[i] = int16(prev + ((int32(interpFactor) * (curr - prev)) >> 2))
		}
		silkNLSFStabilize(interpNLSF, cb.deltaMinQ15, d.lpcOrder)
		lpcSets[0] = nlsfToLPCLibopus(interpNLSF, d.lpcOrder)
	} else {
		lpcSets[0] = lpcSets[1]
	}

	lpcCoeffsQ12 := make([][]int16, d.nSubframes)
	for sf := 0; sf < d.nSubframes; sf++ {
		lpcCoeffsQ12[sf] = lpcSets[sf>>1]
	}
	if d.trace != nil && len(d.trace.Frames) > 0 {
		tf := &d.trace.Frames[len(d.trace.Frames)-1]
		tf.NLSFIndices = append([]int(nil), nlsfIndices...)
		tf.NLSFQ15 = append([]int16(nil), nlsfQ15...)
		tf.InterpFactor = interpFactor
		tf.PredCoef0Q12 = append([]int16(nil), lpcSets[0]...)
		tf.PredCoef1Q12 = append([]int16(nil), lpcSets[1]...)
	}

	// ── 5. Decode pitch parameters (voiced only) ─────────────────────────────
	pitchLags := make([]int, d.nSubframes)
	for sf := range pitchLags {
		pitchLags[sf] = d.lagPrev
	}
	ltpCoeffsQ14 := make([][5]int16, d.nSubframes)
	ltpScaleQ14 := int16(15565) // ~0.95 in Q14

	if signalType == SignalTypeVoiced {
		decodeAbsoluteLagIndex := true
		lag := 0
		if conditionalGain && d.prevSignalType == SignalTypeVoiced {
			deltaLagIndex := dec.DecodeIcdf(silkPitchDeltaICDF[:], 8)
			if deltaLagIndex > 0 {
				deltaLagIndex -= 9
				lag = d.prevLagIndex + deltaLagIndex
				decodeAbsoluteLagIndex = false
			}
		}
		if decodeAbsoluteLagIndex {
			lagIndex := dec.DecodeIcdf(silkPitchLagICDF[:], 8)
			lagLowBits := decodePitchLagLowBits(dec, d.fsKHz)
			step := d.fsKHz >> 1
			lag = lagIndex*step + lagLowBits
		}
		d.prevLagIndex = lag

		// Pitch contour
		var pitchContourIdx int
		switch {
		case d.fsKHz == 8 && d.nSubframes == 4:
			pitchContourIdx = dec.DecodeIcdf(silkPitchContourNBICDF[:], 8)
		case d.fsKHz == 8:
			pitchContourIdx = dec.DecodeIcdf(silkPitchContour10msNBICDF[:], 8)
		case d.nSubframes == 4:
			pitchContourIdx = dec.DecodeIcdf(silkPitchContourICDF[:], 8)
		default:
			pitchContourIdx = dec.DecodeIcdf(silkPitchContour10msICDF[:], 8)
		}
		minLag := PitchEstMinLagMs * d.fsKHz
		maxLag := PitchEstMaxLagMs * d.fsKHz
		baseLag := minLag + lag
		contourOffsets := silkPitchContourOffsets(pitchContourIdx, d.nSubframes, d.fsKHz)
		for sf := 0; sf < d.nSubframes; sf++ {
			pitchLags[sf] = baseLag + contourOffsets[sf]
			if pitchLags[sf] < minLag {
				pitchLags[sf] = minLag
			}
			if pitchLags[sf] > maxLag {
				pitchLags[sf] = maxLag
			}
		}
		d.lagPrev = pitchLags[d.nSubframes-1]

		// LTP gains
		ltpPerIdx := dec.DecodeIcdf(silkLTPPerIndexICDF[:], 8)
		for sf := 0; sf < d.nSubframes; sf++ {
			var ltpGainIdx int
			switch ltpPerIdx {
			case 0:
				ltpGainIdx = dec.DecodeIcdf(silkLTPGainICDF0[:], 8)
				for k := 0; k < 5; k++ {
					ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ0[ltpGainIdx][k]) << 7
				}
			case 1:
				ltpGainIdx = dec.DecodeIcdf(silkLTPGainICDF1[:], 8)
				for k := 0; k < 5; k++ {
					ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ1[ltpGainIdx][k]) << 7
				}
			default:
				ltpGainIdx = dec.DecodeIcdf(silkLTPGainICDF2[:], 8)
				for k := 0; k < 5; k++ {
					ltpCoeffsQ14[sf][k] = int16(silkLTPGainVQ2[ltpGainIdx][k]) << 7
				}
			}
		}

		if !conditionalGain {
			ltpScaleIdx := dec.DecodeIcdf(silkLTPScaleICDF[:], 8)
			ltpScaleQ14 = silkLTPScalesTable[ltpScaleIdx]
		} else {
			ltpScaleQ14 = silkLTPScalesTable[0]
		}
	}

	// ── 6. Decode seed ───────────────────────────────────────────────────────
	seedIdx := dec.DecodeIcdf(silkUniform4ICDF[:], 8)
	seed := int32(seedIdx)

	// ── 7. Decode pulses ─────────────────────────────────────────────────────
	tellBeforePulse := 0
	if d.trace != nil {
		tellBeforePulse = dec.ECTell()
	}
	pulses := d.decodePulses(dec, signalType, quantOffset, d.frameSize)

	if d.trace != nil && len(d.trace.Frames) > 0 {
		tf := &d.trace.Frames[len(d.trace.Frames)-1]
		tf.PitchLags = append([]int(nil), pitchLags...)
		flat := make([]int16, 0, d.nSubframes*5)
		for sf := 0; sf < d.nSubframes; sf++ {
			flat = append(flat, ltpCoeffsQ14[sf][:]...)
		}
		tf.LTPCoefQ14 = flat
		tf.LTPScaleQ14 = ltpScaleQ14
		tf.Seed = seed
		tf.Pulses = append([]int16(nil), pulses...)
		tf.TellAfterPulses = dec.ECTell()
		tf.RngAfterPulses = dec.GetRng()
		tf.TellBeforePulse = tellBeforePulse
		tf.TellBeforeSigns = d.lastTellBeforeSigns
	}

	// ── 8. Synthesize ────────────────────────────────────────────────────────
	outputI16 := d.synthesize(pulses, gainsQ16, lpcCoeffsQ12,
		pitchLags, ltpCoeffsQ14, ltpScaleQ14,
		signalType, quantOffset, seed)

	// ── 9. Update state ───────────────────────────────────────────────────────
	copy(d.prevNLSFQ15, nlsfQ15)
	if len(lpcCoeffsQ12) > 0 {
		copy(d.prevLPCQ12, lpcCoeffsQ12[len(lpcCoeffsQ12)-1])
	}
	d.prevSignalType = signalType
	d.firstFrame = false
	d.plcCount = 0
	d.lastFrameSilent = false

	// Convert int16 PCM → float64 normalized to [-1, 1]
	result := make([]float64, d.frameSize)
	for i, s := range outputI16 {
		result[i] = float64(s) / 32768.0
	}

	if d.channels == 2 {
		stereo := make([]float64, d.frameSize*2)
		for i, s := range result {
			stereo[i*2] = s
			stereo[i*2+1] = s
		}
		return stereo, nil
	}
	return result, nil
}

// decodePitchLagLowBits decodes the pitch lag low bits, matching libopus
// silk/decode_indices.c which uses uniform4 for 8 kHz and uniform8 for others.
func decodePitchLagLowBits(dec *entcode.Decoder, fsKHz int) int {
	if fsKHz == 8 {
		return dec.DecodeIcdf(silkUniform4ICDF[:], 8) // 4 values, step=4
	}
	if fsKHz == 12 {
		return dec.DecodeIcdf(silkUniform6ICDF[:], 8) // 6 values, step=6
	}
	return dec.DecodeIcdf(silkUniform8ICDF[:], 8) // 8 values, step=8
}

// silkLog2Lin converts a log2-scale input (Q7) to a linear scale output.
// Implements silk_log2lin from libopus silk/log2lin.c.
// Input range: 0 to 3967 (inclusive). Returns 0 for negative input.
func silkLog2Lin(inLogQ7 int32) int32 {
	if inLogQ7 < 0 {
		return 0
	}
	if inLogQ7 >= 3967 {
		return math.MaxInt32
	}

	out := int32(1) << uint(inLogQ7>>7)
	fracQ7 := inLogQ7 & 0x7F

	// Approximation of 2^(frac/128) using quadratic correction
	// silk_SMLAWB(frac_Q7, silk_SMULBB(frac_Q7, 128-frac_Q7), -174)
	correction := fracQ7 + ((fracQ7*int32(128-fracQ7))*(-174))>>16

	if inLogQ7 < 2048 {
		// out += (out * correction) >> 7
		out += (out * correction) >> 7
	} else {
		// out += (out >> 7) * correction
		out += (out >> 7) * correction
	}
	return out
}

// silkGainDequantQ16 converts a gain index (0..63) to a Q16 linear gain value.
// Implements silk_gains_dequant for a single index.
// From libopus silk/gain_quant.c:
//
//	gain_Q16 = silk_log2lin(min(silk_SMULWB(INV_SCALE_Q16, prev_ind) + OFFSET, 3967))
//
// Constants:
//
//	MIN_QGAIN_DB=2, MAX_QGAIN_DB=88, N_LEVELS_QGAIN=64
//	OFFSET = (2 * 128 / 6) + 16 * 128 = 42 + 2048 = 2090
//	INV_SCALE_Q16 = (65536 * ((88-2)*128/6)) / (64-1) = 0x1D1C71
//
// silk_SMULWB(a, b) = (a * (b & 0xFFFF)) >> 16  [lower 16 bits of b treated as signed]
// Actually silk_SMULWB(a, b) = silk_RSHIFT_ROUND(a * b_lower16, 16)
// But prev_ind fits in int8 so: (INV_SCALE_Q16 * prev_ind) >> 16
func silkGainDequantQ16(prevInd int) int32 {
	const (
		offset      = int32(2090)     // (2*128/6) + 16*128
		invScaleQ16 = int32(0x1D1C71) // (65536 * (((88-2)*128)/6)) / (64-1)
	)
	// silk_SMULWB(INV_SCALE_Q16, prev_ind): inv_scale * prev_ind >> 16
	logQ7 := (invScaleQ16 * int32(prevInd)) >> 16
	logQ7 += offset
	if logQ7 > 3967 {
		logQ7 = 3967
	}
	return silkLog2Lin(logQ7)
}

func inverseGainQ31(gainQ16 int32) int32 {
	if gainQ16 <= 0 {
		return math.MaxInt32
	}
	v := (int64(1) << 47) / int64(gainQ16)
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(v)
}

func lpcAnalysisResidualQ0(samples []int32, idx int, aQ12 []int16, order int) int32 {
	pred := int64(0)
	for j := 0; j < order; j++ {
		past := idx - j - 1
		if past < 0 {
			break
		}
		pred += int64(samples[past]) * int64(aQ12[j])
	}
	res := samples[idx] - int32(pred>>12)
	if res > 32767 {
		return 32767
	}
	if res < -32768 {
		return -32768
	}
	return res
}

// decodeNLSF decodes NLSF values from the range coder.
// Implements silk_NLSF_decode + silk_decode_indices NLSF portion from libopus.
// Returns NLSF in Q15.
func (d *Decoder) decodeNLSF(dec *entcode.Decoder, cb *nlsfCBParams, signalType int) ([]int16, []int, error) {
	order := cb.order
	// NLSF_QUANT_MAX_AMPLITUDE = 4
	const nlsfQuantMaxAmp = 4

	// Step 1: Decode stage-1 codebook index (NLSFIndices[0])
	// The iCDF has 2*32 entries: first 32 for unvoiced/inactive, second 32 for voiced.
	// Select based on prevSignalType>>1
	cb1ICDFOffset := (signalType >> 1) * cb.nEntries
	if cb1ICDFOffset+cb.nEntries > len(cb.cb1ICDF) {
		cb1ICDFOffset = 0
	}
	cb1Idx := dec.DecodeIcdf(cb.cb1ICDF[cb1ICDFOffset:cb1ICDFOffset+cb.nEntries], 8)
	if cb1Idx < 0 {
		cb1Idx = 0
	}
	if cb1Idx >= cb.nEntries {
		cb1Idx = cb.nEntries - 1
	}
	nlsfIndices := make([]int, order+1)
	nlsfIndices[0] = cb1Idx

	// Step 2: Unpack ec_ix[] and pred_Q8[] from cb2Select table
	// silk_NLSF_unpack: for each pair of coefficients
	// ec_sel_ptr = &psNLSF_CB->ec_sel[CB1_index * order / 2]
	// entry = *ec_sel_ptr++
	// ec_ix[i]   = smulbb((entry>>1)&7, 2*NLSF_QUANT_MAX_AMPLITUDE+1)
	// pred_Q8[i] = pred_Q8_table[i + (entry & 1) * (order-1)]
	// ec_ix[i+1] = smulbb((entry>>5)&7, 2*NLSF_QUANT_MAX_AMPLITUDE+1)
	// pred_Q8[i+1] = pred_Q8_table[i + ((entry>>4)&1) * (order-1) + 1]

	ecIx := make([]int, order)
	predQ8 := make([]uint8, order)
	ecSelBase := cb1Idx * (order / 2)

	for i := 0; i < order; i += 2 {
		entry := cb.cb2Select[ecSelBase+i/2]
		ecIx[i] = ((int(entry) >> 1) & 7) * (2*nlsfQuantMaxAmp + 1)
		predQ8[i] = cb.predQ8[i+int((entry&1))*int(order-1)]
		ecIx[i+1] = ((int(entry) >> 5) & 7) * (2*nlsfQuantMaxAmp + 1)
		predQ8[i+1] = cb.predQ8[i+int((entry>>4)&1)*int(order-1)+1]
	}

	// Step 3: Decode residual indices and dequantize
	// silk_NLSF_residual_dequant: backward loop
	// out_Q10 = 0
	// for i = order-1 down to 0:
	//   pred_Q10 = (out_Q10 * pred_Q8[i]) >> 8
	//   out_Q10 = Ix << 10
	//   if out_Q10 > 0: out_Q10 -= NLSF_QUANT_LEVEL_ADJ_Q10
	//   elif out_Q10 < 0: out_Q10 += NLSF_QUANT_LEVEL_ADJ_Q10
	//   out_Q10 = smlawb(pred_Q10, out_Q10, quantStepSize_Q16)
	//   x_Q10[i] = out_Q10

	// First decode the raw indices from the bitstream (NLSFIndices[1..order])
	nlsfRawIdx := make([]int8, order)
	for i := 0; i < order; i++ {
		// ec_ix[i] is the offset into cb2ICDF, table has (2*NLSF_QUANT_MAX_AMPLITUDE+1)=9 entries
		ix := dec.DecodeIcdf(cb.cb2ICDF[ecIx[i]:ecIx[i]+2*nlsfQuantMaxAmp+1], 8)
		// Extension: if ix==0 subtract ext, if ix==2*max add ext
		if ix == 0 {
			ix -= dec.DecodeIcdf(silkNLSFExtICDF[:], 8)
		} else if ix == 2*nlsfQuantMaxAmp {
			ix += dec.DecodeIcdf(silkNLSFExtICDF[:], 8)
		}
		nlsfRawIdx[i] = int8(ix - nlsfQuantMaxAmp)
		nlsfIndices[i+1] = int(nlsfRawIdx[i])
	}

	// Now dequantize residuals backward (silk_NLSF_residual_dequant)
	// quantStepSize_Q16 = SILK_FIX_CONST(0.18, 16) = round(0.18 * 65536) = 11796 for NB/MB
	// quantStepSize_Q16 = SILK_FIX_CONST(0.15, 16) = round(0.15 * 65536) = 9830 for WB
	// NLSF_QUANT_LEVEL_ADJ = 0.1, so Q10 = round(0.1 * 1024) = 102
	const nlsfQuantLevelAdjQ10 = int32(102) // SILK_FIX_CONST(0.1, 10)

	var quantStepSizeQ16 int32
	if order == 16 {
		quantStepSizeQ16 = 9830 // SILK_FIX_CONST(0.15, 16)
	} else {
		quantStepSizeQ16 = 11796 // SILK_FIX_CONST(0.18, 16)
	}

	resQ10 := make([]int32, order)
	outQ10 := int32(0)
	for i := order - 1; i >= 0; i-- {
		predQ10 := (outQ10 * int32(predQ8[i])) >> 8
		outQ10 = int32(nlsfRawIdx[i]) << 10
		if outQ10 > 0 {
			outQ10 -= nlsfQuantLevelAdjQ10
		} else if outQ10 < 0 {
			outQ10 += nlsfQuantLevelAdjQ10
		}
		// smlawb(pred_Q10, out_Q10, quantStepSize_Q16) = pred_Q10 + (out_Q10 * quantStepSize_Q16) >> 16
		outQ10 = predQ10 + int32((int64(outQ10)*int64(quantStepSizeQ16))>>16)
		resQ10[i] = outQ10
	}

	// Step 4: Apply to stage-1 codebook entry
	// From silk_NLSF_decode:
	// NLSF_Q15_tmp = silk_ADD_LSHIFT32(silk_DIV32_16(silk_LSHIFT((opus_int32)res_Q10[i], 14), pCB_Wght_Q9[i]), (opus_int16)pCB_element[i], 7)
	// pNLSF_Q15[i] = silk_LIMIT(NLSF_Q15_tmp, 0, 32767)
	//
	// pCB_element[i] = cb.cb1Q8[cb1Idx*order + i]  (Q8)
	// pCB_Wght_Q9[i] = cb.cb1WghtQ9[cb1Idx*order + i]  (Q9)
	// ADD_LSHIFT32(a, b, 7) = a + (b << 7)
	// DIV32_16(a, b) = a / b

	nlsfQ15 := make([]int16, order)
	for i := 0; i < order; i++ {
		cb1Val := int32(cb.cb1Q8[cb1Idx*order+i])     // Q8
		wghtQ9 := int32(cb.cb1WghtQ9[cb1Idx*order+i]) // Q9

		// silk_DIV32_16(silk_LSHIFT(res_Q10[i], 14), wghtQ9)
		// = (res_Q10[i] << 14) / wghtQ9
		// Then add (cb1Val << 7) to get Q15
		numerator := resQ10[i] << 14
		var div int32
		if wghtQ9 != 0 {
			div = numerator / wghtQ9
		}
		nlsfQ15Tmp := div + (cb1Val << 7)
		if nlsfQ15Tmp < 0 {
			nlsfQ15Tmp = 0
		}
		if nlsfQ15Tmp > 32767 {
			nlsfQ15Tmp = 32767
		}
		nlsfQ15[i] = int16(nlsfQ15Tmp)
	}

	// Step 5: NLSF stabilization
	silkNLSFStabilize(nlsfQ15, cb.deltaMinQ15, order)

	return nlsfQ15, nlsfIndices, nil
}

// silkNLSFStabilize implements silk_NLSF_stabilize.
// Ensures minimum spacing between NLSF values and enforces bounds.
func silkNLSFStabilize(nlsf []int16, deltaMin []int16, order int) {
	const maxIter = 20

	for iter := 0; iter < maxIter; iter++ {
		// Find the location of the largest constraint violation
		I := -1
		minVal := int32(-32767)

		// Check lower bound: nlsf[0] >= deltaMin[0]
		violation := int32(deltaMin[0]) - int32(nlsf[0])
		if violation > minVal {
			minVal = violation
			I = 0
		}
		// Check upper bound: nlsf[order-1] <= 32767 - deltaMin[order]
		violation = int32(nlsf[order-1]) - (32767 - int32(deltaMin[order]))
		if violation > minVal {
			minVal = violation
			I = order
		}
		// Check spacing: nlsf[i] >= nlsf[i-1] + deltaMin[i]
		for i := 1; i < order; i++ {
			violation = int32(deltaMin[i]) + int32(nlsf[i-1]) - int32(nlsf[i])
			if violation > minVal {
				minVal = violation
				I = i
			}
		}

		if minVal <= 0 {
			break // No violations
		}

		// Fix the violation
		if I == 0 {
			nlsf[0] = deltaMin[0]
		} else if I == order {
			nlsf[order-1] = int16(32767 - int32(deltaMin[order]))
		} else {
			// Move both nlsf[I-1] and nlsf[I] to center
			mid := (int32(nlsf[I-1]) + int32(nlsf[I])) >> 1
			nlsf[I-1] = int16(mid - int32(deltaMin[I])>>1)
			nlsf[I] = int16(mid + int32(deltaMin[I]) - int32(deltaMin[I])>>1)
		}
	}

	// Final clamp
	if nlsf[0] < deltaMin[0] {
		nlsf[0] = deltaMin[0]
	}
	for i := 1; i < order; i++ {
		minV := int32(nlsf[i-1]) + int32(deltaMin[i])
		if int32(nlsf[i]) < minV {
			nlsf[i] = int16(minV)
		}
	}
	maxBound := int16(32767 - int32(deltaMin[order]))
	if nlsf[order-1] > maxBound {
		nlsf[order-1] = maxBound
	}
	for i := order - 2; i >= 0; i-- {
		maxV := int32(nlsf[i+1]) - int32(deltaMin[i+1])
		if int32(nlsf[i]) > maxV {
			nlsf[i] = int16(maxV)
		}
	}
}

// nlsfToLPCLibopus converts NLSF values (Q15) to LPC coefficients (Q12).
// Implements silk_NLSF2A from libopus silk/NLSF2A.c using fixed-point arithmetic.
// QA = 16
func nlsfToLPCLibopus(nlsfQ15 []int16, order int) []int16 {
	const QA = 16

	var ordering []int
	if order == 16 {
		ordering = nlsf2AOrdering16[:]
	} else {
		ordering = nlsf2AOrdering10[:]
	}

	cLSF := make([]int32, order)
	for i := 0; i < order; i++ {
		n := int32(nlsfQ15[i])
		if n < 0 {
			n = 0
		}
		if n > 32767 {
			n = 32767
		}
		fInt := n >> 8 // index into table (0..127)
		fFrac := n & 0xFF
		if fInt < 0 {
			fInt = 0
		}
		if fInt >= 128 {
			fInt = 127
		}
		cosVal := silkLSFCosTabFixQ12[fInt]
		delta := silkLSFCosTabFixQ12[fInt+1] - cosVal
		interp := int64(cosVal<<8) + int64(delta)*int64(fFrac)
		cLSF[ordering[i]] = silkRShiftRound(interp, 20-QA)
	}

	halfOrder := order / 2
	P := make([]int32, halfOrder+1)
	Q := make([]int32, halfOrder+1)

	nlsf2APolyFindPoly(P, cLSF, halfOrder)
	nlsf2APolyFindPoly(Q, cLSF[1:], halfOrder)

	a32QA1 := make([]int32, order)
	for k := 0; k < halfOrder; k++ {
		Ptmp := P[k+1] + P[k]
		Qtmp := Q[k+1] - Q[k]
		a32QA1[k] = -Qtmp - Ptmp
		a32QA1[order-k-1] = Qtmp - Ptmp
	}

	coeffs := silkLPCFit(a32QA1, 12, QA+1, order)
	for i := 0; i < 16 && silkLPCInversePredGainQ12(coeffs, order) == 0; i++ {
		silkBWExpander32(a32QA1, order, 65536-(2<<i))
		for k := 0; k < order; k++ {
			coeffs[k] = int16(silkRShiftRound(int64(a32QA1[k]), QA+1-12))
		}
	}
	return coeffs
}

// nlsf2APolyFindPoly implements silk_NLSF2A_find_poly.
// Computes a polynomial of degree dd from the cLSF array (2-spaced entries).
// out[0..dd] in QA format.
func nlsf2APolyFindPoly(out []int32, cLSF []int32, dd int) {
	const QA = 16
	out[0] = 1 << QA
	out[1] = -cLSF[0]
	for k := 1; k < dd; k++ {
		ftmp := cLSF[2*k]
		out[k+1] = (out[k-1] << 1) - silkRShiftRound(int64(ftmp)*int64(out[k]), QA)
		for n := k; n > 1; n-- {
			out[n] += out[n-2] - silkRShiftRound(int64(ftmp)*int64(out[n-1]), QA)
		}
		out[1] -= ftmp
	}
}

func silkLPCFit(aQIN []int32, qOut, qIn, order int) []int16 {
	coeffs := make([]int16, order)
	shift := qIn - qOut

	fit := false
	for i := 0; i < 10; i++ {
		maxabs := int32(0)
		idx := 0
		for k := 0; k < order; k++ {
			absval := aQIN[k]
			if absval < 0 {
				absval = -absval
			}
			if absval > maxabs {
				maxabs = absval
				idx = k
			}
		}
		maxabs = silkRShiftRound(int64(maxabs), shift)
		if maxabs <= 32767 {
			fit = true
			break
		}
		if maxabs > 163838 {
			maxabs = 163838
		}
		denom := int32((int64(maxabs) * int64(idx+1)) >> 2)
		chirpQ16 := int32(65470)
		if denom != 0 {
			chirpQ16 -= int32((int64(maxabs-32767) << 14) / int64(denom))
		}
		silkBWExpander32(aQIN, order, chirpQ16)
	}

	for k := 0; k < order; k++ {
		coeffs[k] = clamp16(silkRShiftRound(int64(aQIN[k]), shift))
		if !fit {
			aQIN[k] = int32(coeffs[k]) << shift
		}
	}
	return coeffs
}

func silkBWExpander32(ar []int32, order int, chirpQ16 int32) {
	chirpMinusOneQ16 := chirpQ16 - 65536
	for i := 0; i < order-1; i++ {
		ar[i] = silkSMULWW(chirpQ16, ar[i])
		chirpQ16 += silkRShiftRound(int64(chirpQ16)*int64(chirpMinusOneQ16), 16)
	}
	if order > 0 {
		ar[order-1] = silkSMULWW(chirpQ16, ar[order-1])
	}
}

// silkLPCInversePredGainQ12 ports silk_LPC_inverse_pred_gain_c. It returns the
// inverse prediction gain in Q30, or zero when the LPC filter is unstable or
// exceeds SILK's maximum prediction-power gain.
func silkLPCInversePredGainQ12(aQ12 []int16, order int) int32 {
	const (
		qa            = 24
		aLimitQ24     = int32(16773022) // round(0.99975 * 2^24)
		minInvGainQ30 = int32(107374)   // round((1 / 1e4) * 2^30)
	)
	if order <= 0 || order > len(aQ12) {
		return 0
	}
	aQA := make([]int32, order)
	dcResp := int32(0)
	for k := 0; k < order; k++ {
		dcResp += int32(aQ12[k])
		aQA[k] = int32(aQ12[k]) << (qa - 12)
	}
	if dcResp >= 4096 {
		return 0
	}

	invGainQ30 := int32(1 << 30)
	for k := order - 1; k > 0; k-- {
		if aQA[k] > aLimitQ24 || aQA[k] < -aLimitQ24 {
			return 0
		}
		rcQ31 := -(aQA[k] << (31 - qa))
		rcMult1Q30 := int32(1<<30) - silkSMMUL(rcQ31, rcQ31)
		invGainQ30 = silkSMMUL(invGainQ30, rcMult1Q30) << 2
		if invGainQ30 < minInvGainQ30 {
			return 0
		}

		mult2Q := 32 - silkCLZ32(silkAbs32(rcMult1Q30))
		rcMult2 := silkInverse32VarQ(rcMult1Q30, mult2Q+30)
		for n := 0; n < (k+1)>>1; n++ {
			tmp1 := aQA[n]
			tmp2 := aQA[k-n-1]
			tmp2RC := silkRShiftRound(int64(tmp2)*int64(rcQ31), 31)
			v1 := int64(silkSUBSAT32(tmp1, tmp2RC)) * int64(rcMult2)
			v1 = silkRShiftRound64(v1, mult2Q)
			if v1 > math.MaxInt32 || v1 < math.MinInt32 {
				return 0
			}
			tmp1RC := silkRShiftRound(int64(tmp1)*int64(rcQ31), 31)
			v2 := int64(silkSUBSAT32(tmp2, tmp1RC)) * int64(rcMult2)
			v2 = silkRShiftRound64(v2, mult2Q)
			if v2 > math.MaxInt32 || v2 < math.MinInt32 {
				return 0
			}
			aQA[n] = int32(v1)
			aQA[k-n-1] = int32(v2)
		}
	}
	if aQA[0] > aLimitQ24 || aQA[0] < -aLimitQ24 {
		return 0
	}
	rcQ31 := -(aQA[0] << (31 - qa))
	rcMult1Q30 := int32(1<<30) - silkSMMUL(rcQ31, rcQ31)
	invGainQ30 = silkSMMUL(invGainQ30, rcMult1Q30) << 2
	if invGainQ30 < minInvGainQ30 {
		return 0
	}
	return invGainQ30
}

func silkRShiftRound64(v int64, shift int) int64 {
	if shift <= 0 {
		return v
	}
	if shift == 1 {
		return (v >> 1) + (v & 1)
	}
	return ((v >> (shift - 1)) + 1) >> 1
}

func silkSMULWW(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 16)
}

// silkPitchContourOffsets returns per-subframe pitch lag offsets from contour index.
func silkPitchContourOffsets(contourIdx, nSubframes, fsKHz int) []int {
	offsets := make([]int, nSubframes)
	if fsKHz == 8 && nSubframes == 4 {
		patterns := [4][11]int{
			{0, 2, -1, -1, -1, 0, 0, 1, 1, 0, 1},
			{0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0},
			{0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0},
			{0, -1, 2, 1, 0, 1, 1, 0, 0, -1, -1},
		}
		if contourIdx >= 0 && contourIdx < 11 {
			for sf := 0; sf < 4; sf++ {
				offsets[sf] = patterns[sf][contourIdx]
			}
		}
		return offsets
	}
	if fsKHz == 8 && nSubframes == 2 {
		patterns := [2][3]int{
			{0, 1, 0},
			{0, 0, 1},
		}
		if contourIdx >= 0 && contourIdx < 3 {
			for sf := 0; sf < 2; sf++ {
				offsets[sf] = patterns[sf][contourIdx]
			}
		}
		return offsets
	}
	if nSubframes == 4 {
		patterns := [4][34]int{
			{0, 0, 1, -1, 0, 1, -1, 0, -1, 1, -2, 2, -2, -2, 2, -3, 2, 3, -3, -4, 3, -4, 4, 4, -5, 5, -6, -5, 6, -7, 6, 5, 8, -9},
			{0, 0, 1, 0, 0, 0, 0, 0, 0, 0, -1, 1, 0, 0, 1, -1, 0, 1, -1, -1, 1, -1, 2, 1, -1, 2, -2, -2, 2, -2, 2, 2, 3, -3},
			{0, 1, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0, 0, 1, -1, 1, 0, 0, 2, 1, -1, 2, -1, -1, 2, -1, 2, 2, -1, 3, -2, -2, -2, 3},
			{0, 1, 0, 0, 1, 0, 1, -1, 2, -1, 2, -1, 2, 3, -2, 3, -2, -2, 4, 4, -3, 5, -3, -4, 6, -4, 6, 5, -5, 8, -6, -5, -7, 9},
		}
		if contourIdx >= 0 && contourIdx < 34 {
			for sf := 0; sf < 4; sf++ {
				offsets[sf] = patterns[sf][contourIdx]
			}
		}
		return offsets
	}
	patterns := [2][12]int{
		{0, 0, 1, -1, 1, -1, 2, -2, 2, -2, 3, -3},
		{0, 1, 0, 1, -1, 2, -1, 2, -2, 3, -2, 3},
	}
	if contourIdx >= 0 && contourIdx < 12 {
		for sf := 0; sf < 2; sf++ {
			offsets[sf] = patterns[sf][contourIdx]
		}
	}
	return offsets
}

// decodePulses decodes the excitation pulse sequence using shell coding.
// Returns signed pulse values (before dequantization/gain apply).
func (d *Decoder) decodePulses(dec *entcode.Decoder, signalType, quantOffset, frameLen int) []int16 {
	// Decode rate level
	rateLevelIdx := dec.DecodeIcdf(silkRateLevelsICDF[signalType>>1][:], 8)

	// Number of shell coding blocks. For 12 kHz 10 ms (frameLen=120) this is the
	// only case where iter*16 > frameLen: libopus processes the full, block-
	// aligned 16-sample blocks (decoding pulses, LSBs and signs for the trailing
	// "phantom" positions beyond frameLen) and only truncates at the end. We
	// mirror that by working on a block-aligned buffer; clamping to frameLen here
	// would desync the range coder by the phantom positions' LSB/sign bits.
	iter := frameLen >> log2ShellCodecFrameLen
	if iter*shellCodecFrameLength < frameLen {
		iter++
	}
	alignedLen := iter * shellCodecFrameLength
	pulses := make([]int16, alignedLen)

	sumPulses := make([]int, iter)
	nLShifts := make([]int, iter)

	for i := 0; i < iter; i++ {
		nLShifts[i] = 0
		sumPulses[i] = dec.DecodeIcdf(silkPulsesPerBlockICDF[rateLevelIdx][:], 8)

		for sumPulses[i] == silkMaxPulses+1 {
			nLShifts[i]++
			rowIdx := nRateLevels - 1
			offset := 0
			if nLShifts[i] == 10 {
				offset = 1
			}
			sumPulses[i] = dec.DecodeIcdf(silkPulsesPerBlockICDF[rowIdx][offset:], 8)
		}
	}

	// Shell-decode each full block.
	for i := 0; i < iter; i++ {
		blockStart := i * shellCodecFrameLength
		if sumPulses[i] > 0 {
			var tmpBuf [shellCodecFrameLength]int16
			d.shellDecode(dec, tmpBuf[:], sumPulses[i], shellCodecFrameLength)
			copy(pulses[blockStart:blockStart+shellCodecFrameLength], tmpBuf[:])
		}
	}

	// Apply LSB shifts over the full block (including phantom positions).
	for i := 0; i < iter; i++ {
		if nLShifts[i] > 0 {
			blockStart := i * shellCodecFrameLength
			for k := blockStart; k < blockStart+shellCodecFrameLength; k++ {
				absQ := int(pulses[k])
				for j := 0; j < nLShifts[i]; j++ {
					absQ <<= 1
					absQ += dec.DecodeIcdf(silkLSBICDFDec[:], 8)
				}
				pulses[k] = int16(absQ)
			}
			sumPulses[i] |= nLShifts[i] << 5
		}
	}

	// Decode signs over the full block-aligned buffer.
	if d.trace != nil {
		d.lastTellBeforeSigns = dec.ECTell()
	}
	d.decodeSigns(dec, pulses, alignedLen, signalType, quantOffset, sumPulses)
	if d.trace != nil && len(d.trace.Frames) > 0 {
		tf := &d.trace.Frames[len(d.trace.Frames)-1]
		tf.RateLevelIdx = rateLevelIdx
		tf.SumPulses = append([]int(nil), sumPulses...)
	}
	return pulses[:frameLen]
}

// shellDecode decodes 16 pulse values using the silk_shell_decoder algorithm.
func (d *Decoder) shellDecode(dec *entcode.Decoder, pulses []int16, n int, available int) {
	if n <= 0 || available <= 0 {
		return
	}
	buf := make([]int16, shellCodecFrameLength)

	splitDecode := func(p0, p1 *int16, total int, table []uint8) {
		if total <= 0 {
			*p0 = 0
			*p1 = 0
			return
		}
		if total > 16 {
			total = 16
		}
		off := int(silkShellCodeTableOffsets[total])
		sym := dec.DecodeIcdf(table[off:off+total+1], 8)
		*p0 = int16(sym)
		*p1 = int16(total) - int16(sym)
	}

	var p3 [2]int16
	var p2 [4]int16
	var p1 [8]int16

	splitDecode(&p3[0], &p3[1], n, silkShellCodeTable3[:])
	splitDecode(&p2[0], &p2[1], int(p3[0]), silkShellCodeTable2[:])
	splitDecode(&p1[0], &p1[1], int(p2[0]), silkShellCodeTable1[:])
	splitDecode(&buf[0], &buf[1], int(p1[0]), silkShellCodeTable0[:])
	splitDecode(&buf[2], &buf[3], int(p1[1]), silkShellCodeTable0[:])
	splitDecode(&p1[2], &p1[3], int(p2[1]), silkShellCodeTable1[:])
	splitDecode(&buf[4], &buf[5], int(p1[2]), silkShellCodeTable0[:])
	splitDecode(&buf[6], &buf[7], int(p1[3]), silkShellCodeTable0[:])
	splitDecode(&p2[2], &p2[3], int(p3[1]), silkShellCodeTable2[:])
	splitDecode(&p1[4], &p1[5], int(p2[2]), silkShellCodeTable1[:])
	splitDecode(&buf[8], &buf[9], int(p1[4]), silkShellCodeTable0[:])
	splitDecode(&buf[10], &buf[11], int(p1[5]), silkShellCodeTable0[:])
	splitDecode(&p1[6], &p1[7], int(p2[3]), silkShellCodeTable1[:])
	splitDecode(&buf[12], &buf[13], int(p1[6]), silkShellCodeTable0[:])
	splitDecode(&buf[14], &buf[15], int(p1[7]), silkShellCodeTable0[:])

	for i := 0; i < available && i < shellCodecFrameLength; i++ {
		pulses[i] = buf[i]
	}
}

// decodeSigns decodes signs for non-zero pulses.
func (d *Decoder) decodeSigns(dec *entcode.Decoder, pulses []int16, frameLen, signalType, quantOffset int, sumPulses []int) {
	iter := frameLen >> log2ShellCodecFrameLen
	if iter*shellCodecFrameLength < frameLen {
		iter++
	}

	signRow := signalType*2 + quantOffset
	if signRow < 0 {
		signRow = 0
	}
	if signRow > 5 {
		signRow = 5
	}

	for i := 0; i < iter; i++ {
		p := sumPulses[i] & 0x1F
		if p == 0 {
			continue
		}
		col := p
		if col > 6 {
			col = 6
		}
		icdfVal := silkSignICDF[signRow][col]
		icdf2 := [2]uint8{icdfVal, 0}

		blockStart := i * shellCodecFrameLength
		end := blockStart + shellCodecFrameLength
		if end > frameLen {
			end = frameLen
		}
		for k := blockStart; k < end; k++ {
			if pulses[k] != 0 {
				sym := dec.DecodeIcdf(icdf2[:], 8)
				if sym == 0 {
					pulses[k] = -pulses[k]
				}
			}
		}
	}
}

func silkSMULWB(a int32, b int16) int32 {
	return int32((int64(a) * int64(b)) >> 16)
}

func silkSMLAWB(a, b int32, c int16) int32 {
	return a + silkSMULWB(b, c)
}

func silkSMLAWW(a, b, c int32) int32 {
	return a + silkSMULWW(b, c)
}

func silkSMMUL(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 32)
}

func silkLShiftSat32(a int32, shift int) int32 {
	if shift <= 0 {
		return a
	}
	lo := int32(math.MinInt32 >> shift)
	hi := int32(math.MaxInt32 >> shift)
	if a < lo {
		a = lo
	} else if a > hi {
		a = hi
	}
	return a << shift
}

func silkAddSat32(a, b int32) int32 {
	sum := int64(a) + int64(b)
	if sum > math.MaxInt32 {
		return math.MaxInt32
	}
	if sum < math.MinInt32 {
		return math.MinInt32
	}
	return int32(sum)
}

func silkAbs32(a int32) int32 {
	if a < 0 {
		if a == math.MinInt32 {
			return math.MaxInt32
		}
		return -a
	}
	return a
}

func silkCLZ32(a int32) int {
	u := uint32(a)
	if u == 0 {
		return 32
	}
	n := 0
	for (u & 0x80000000) == 0 {
		n++
		u <<= 1
	}
	return n
}

func silkDIV32VarQ(a32, b32 int32, qres int) int32 {
	aHeadrm := silkCLZ32(silkAbs32(a32)) - 1
	a32Nrm := a32 << aHeadrm
	bHeadrm := silkCLZ32(silkAbs32(b32)) - 1
	b32Nrm := b32 << bHeadrm
	b32Inv := int32((math.MaxInt32 >> 2) / int(b32Nrm>>16))
	result := silkSMULWB(a32Nrm, int16(b32Inv))
	a32Nrm = a32Nrm - (silkSMMUL(b32Nrm, result) << 3)
	result = silkSMLAWB(result, a32Nrm, int16(b32Inv))
	lshift := 29 + aHeadrm - bHeadrm - qres
	if lshift < 0 {
		return silkLShiftSat32(result, -lshift)
	}
	if lshift < 32 {
		return result >> lshift
	}
	return 0
}

func silkInverse32VarQ(b32 int32, qres int) int32 {
	bHeadrm := silkCLZ32(silkAbs32(b32)) - 1
	b32Nrm := b32 << bHeadrm
	b32Inv := int32((math.MaxInt32 >> 2) / int(b32Nrm>>16))
	result := b32Inv << 16
	errQ32 := ((int32(1) << 29) - silkSMULWB(b32Nrm, int16(b32Inv))) << 3
	result = silkSMLAWW(result, errQ32, b32Inv)
	lshift := 61 - bHeadrm - qres
	if lshift <= 0 {
		return silkLShiftSat32(result, -lshift)
	}
	if lshift < 32 {
		return result >> lshift
	}
	return 0
}

func silkLPCAnalysisFilter(out []int16, in []int32, b []int16, length, order int) {
	for ix := order; ix < length; ix++ {
		inPtr := ix - 1
		out32Q12 := int32(int16(in[inPtr])) * int32(b[0])
		for j := 1; j < order; j++ {
			out32Q12 += int32(int16(in[inPtr-j])) * int32(b[j])
		}
		out32Q12 = (int32(in[ix]) << 12) - out32Q12
		out[ix] = clamp16(silkRShiftRound(int64(out32Q12), 12))
	}
	for ix := 0; ix < order && ix < length; ix++ {
		out[ix] = 0
	}
}

// synthesize performs inverse NSQ (noise-shaping quantizer) synthesis.
// Implements silk_decode_core from libopus silk/decode_core.c.
// synthesize implements libopus silk/decode_core.c, producing int32 Q14 output samples.
// The output is the unscaled LPC synthesis; caller applies gain and converts to float.
func (d *Decoder) synthesize(
	pulses []int16,
	gainsQ16 []int32,
	lpcCoeffsQ12 [][]int16,
	pitchLags []int,
	ltpCoeffsQ14 [][5]int16,
	ltpScaleQ14 int16,
	signalType, quantOffset int,
	seed int32,
) []int16 {
	// Quantization offset in Q10
	// Index: [0=unvoiced, 1=voiced][quantOffset]
	uvIdx := 0
	if signalType == SignalTypeVoiced {
		uvIdx = 1
	}
	offsetQ10 := int32(silkQuantizationOffsetsQ10[uvIdx][quantOffset])

	// ── Step 1: Build excitation array (entire frame) per libopus decode_core.c ──
	// rand_seed = silk_RAND(rand_seed);  // update FIRST
	// exc_Q14[i] = pulses[i] << 14
	// if exc > 0: exc -= QUANT_LEVEL_ADJUST_Q10 << 4 (= 80*16 = 1280)
	// if exc < 0: exc += 1280
	// exc += offset_Q10 << 4   (unconditional)
	// if rand_seed < 0: exc = -exc
	// rand_seed += pulses[i]   (silk_ADD32_ovflw)
	excQ14 := make([]int32, d.frameSize)
	for i := 0; i < d.frameSize; i++ {
		seed = 196314165*seed + 907633515 // silk_RAND: LCG first
		excQ14[i] = int32(pulses[i]) << 14
		if excQ14[i] > 0 {
			excQ14[i] -= 80 << 4 // QUANT_LEVEL_ADJUST_Q10=80, <<4 to Q14
		} else if excQ14[i] < 0 {
			excQ14[i] += 80 << 4
		}
		excQ14[i] += offsetQ10 << 4
		if seed < 0 {
			excQ14[i] = -excQ14[i]
		}
		seed += int32(pulses[i]) // silk_ADD32_ovflw (wrapping)
	}
	if d.trace != nil && len(d.trace.Frames) > 0 {
		tf := &d.trace.Frames[len(d.trace.Frames)-1]
		tf.ExcQ14 = append([]int32(nil), excQ14...)
	}

	ltpMemLen := len(d.ltpState)
	sLTPQ15 := make([]int32, ltpMemLen+d.frameSize)
	outBufQ0 := make([]int32, ltpMemLen+d.frameSize)
	copy(outBufQ0, d.ltpState)
	sLTPBufIdx := ltpMemLen

	sLPCQ14 := make([]int32, silkMaxLPCOrder+d.subfrmLen)
	copy(sLPCQ14[:silkMaxLPCOrder], d.lpcState)
	output := make([]int16, d.frameSize)
	nlsfInterpolation := false
	if d.nSubframes == 4 && len(lpcCoeffsQ12[0]) > 0 && len(lpcCoeffsQ12[d.nSubframes-1]) > 0 {
		nlsfInterpolation = &lpcCoeffsQ12[0][0] != &lpcCoeffsQ12[d.nSubframes-1][0]
	}

	for sf := 0; sf < d.nSubframes; sf++ {
		start := sf * d.subfrmLen
		gainQ10 := gainsQ16[sf] >> 6
		invGainQ31 := silkInverse32VarQ(gainsQ16[sf], 47)
		gainAdjQ16 := int32(1 << 16)
		if gainsQ16[sf] != d.prevGainQ16 {
			gainAdjQ16 = silkDIV32VarQ(d.prevGainQ16, gainsQ16[sf], 16)
			for i := 0; i < silkMaxLPCOrder; i++ {
				sLPCQ14[i] = silkSMULWW(gainAdjQ16, sLPCQ14[i])
			}
		}
		d.prevGainQ16 = gainsQ16[sf]
		lpc := lpcCoeffsQ12[sf]
		lag := pitchLags[sf]

		if signalType == SignalTypeVoiced {
			// libopus decode_core.c:141 — re-whiten when
			//   k == 0 || (k == 2 && NLSF_interpolation_flag).
			// NLSF_interpolation_flag == 1 iff interpFactor < 4 (interpolation active),
			// i.e. nlsfInterpolation == true. So with interpolation we re-whiten at
			// sf=0 AND sf=2; without it (interp=4) we re-whiten only at sf=0.
			if sf == 0 || (sf == 2 && nlsfInterpolation) {
				startIdx := sLTPBufIdx - lag - d.lpcOrder - 2
				if startIdx < 0 {
					startIdx = 0
				}
				if sf == 2 {
					for i := 0; i < 2*d.subfrmLen; i++ {
						outBufQ0[ltpMemLen+i] = int32(output[i])
					}
				}
				filterLen := sLTPBufIdx - startIdx
				sLTP := make([]int16, filterLen)
				silkLPCAnalysisFilter(sLTP, outBufQ0[startIdx:startIdx+filterLen], lpc, filterLen, d.lpcOrder)
				if sf == 0 {
					invGainQ31 = silkSMULWB(invGainQ31, ltpScaleQ14) << 2
				}
				for i := 0; i < lag+2 && i < filterLen; i++ {
					sLTPQ15[sLTPBufIdx-i-1] = silkSMULWB(invGainQ31, sLTP[filterLen-i-1])
				}
			} else if gainAdjQ16 != 1<<16 {
				for i := 0; i < lag+2 && sLTPBufIdx-i-1 >= 0; i++ {
					idx := sLTPBufIdx - i - 1
					sLTPQ15[idx] = silkSMULWW(gainAdjQ16, sLTPQ15[idx])
				}
			}
		}

		presQ14 := excQ14[start : start+d.subfrmLen]
		for i := 0; i < d.subfrmLen; i++ {
			if signalType == SignalTypeVoiced {
				ltpPredQ13 := int32(2) // rounding bias (matches libopus)
				for k := 0; k < 5; k++ {
					ltpIdx := sLTPBufIdx - lag + 2 - k
					if ltpIdx >= 0 && ltpIdx < len(sLTPQ15) {
						ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[ltpIdx], ltpCoeffsQ14[sf][k])
					}
				}
				presQ14[i] = excQ14[start+i] + (ltpPredQ13 << 1)
				sLTPQ15[sLTPBufIdx] = presQ14[i] << 1
				sLTPBufIdx++
			}

			lpcPredQ10 := int32(d.lpcOrder >> 1)
			for j := 0; j < d.lpcOrder; j++ {
				lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPCQ14[silkMaxLPCOrder+i-j-1], lpc[j])
			}
			predQ14 := silkLShiftSat32(lpcPredQ10, 4)
			v := silkAddSat32(presQ14[i], predQ14)
			sLPCQ14[silkMaxLPCOrder+i] = v
			pxq := silkRShiftRound(int64(silkSMULWW(v, gainQ10)), 8)
			output[start+i] = clamp16(pxq)
		}

		copy(sLPCQ14[:silkMaxLPCOrder], sLPCQ14[d.subfrmLen:d.subfrmLen+silkMaxLPCOrder])
	}

	copy(d.lpcState, sLPCQ14[:silkMaxLPCOrder])
	if d.frameSize <= ltpMemLen {
		mvLen := ltpMemLen - d.frameSize
		copy(d.ltpState[:mvLen], d.ltpState[d.frameSize:])
		for i := 0; i < d.frameSize; i++ {
			d.ltpState[mvLen+i] = int32(output[i])
		}
	}
	d.randSeed = seed
	return output
}

// decodeSilence returns a zero-filled frame.
func (d *Decoder) decodeSilence() ([]float64, error) {
	return make([]float64, d.frameSize*d.channels), nil
}

// noteDigitalSilence records a decoded digital-silence packet. The synthesis
// history is cleared so that a following packet loss is concealed as continued
// silence instead of replaying stale pre-silence speech.
func (d *Decoder) noteDigitalSilence() {
	d.lastFrameSilent = true
	d.plcCount = 0
	d.prevSignalType = SignalTypeUnvoiced
	for i := range d.lpcState {
		d.lpcState[i] = 0
	}
	for i := range d.ltpState {
		d.ltpState[i] = 0
	}
	for i := range d.prevOutput {
		d.prevOutput[i] = 0
	}
	// The true delayed samples after a silence frame are zeros; without this
	// the stereo unmix replays the pre-silence tail into the next frame.
	d.stereoMid = [2]int16{}
	d.stereoSide = [2]int16{}
}

// concealPacketLoss performs pitch-history based packet loss concealment.
func (d *Decoder) concealPacketLoss() ([]float64, error) {
	if d.lastFrameSilent {
		// Digital silence continues as silence; the cleared history would
		// otherwise only feed the concealment noise generator.
		d.plcCount++
		return make([]float64, d.frameSize*d.channels), nil
	}
	d.plcCount++

	output := make([]int32, d.frameSize)
	lag := d.lagPrev
	if lag <= 0 || lag > len(d.ltpState) {
		lag = d.fsKHz * 10
	}
	if lag <= 0 {
		lag = 1
	}

	histStart := len(d.ltpState) - lag
	for i := range output {
		var pred int32
		src := histStart + i
		switch {
		case src >= 0 && src < len(d.ltpState):
			pred = d.ltpState[src]
		case i >= lag:
			pred = output[i-lag]
		case len(d.ltpState) > 0:
			pred = d.ltpState[len(d.ltpState)-1]
		}

		if d.prevSignalType != SignalTypeVoiced {
			d.randSeed = 196314165*d.randSeed + 907633515
			noise := d.randSeed >> 20
			var lpcPred64 int64
			for j := 0; j < d.lpcOrder; j++ {
				pastIdx := i - j - 1
				var past int32
				if pastIdx >= 0 {
					past = output[pastIdx] << 14
				} else {
					histIdx := len(d.lpcState) + pastIdx
					if histIdx >= 0 && histIdx < len(d.lpcState) {
						past = d.lpcState[histIdx]
					}
				}
				lpcPred64 += int64(d.prevLPCQ12[j]) * int64(past)
			}
			pred = int32(lpcPred64>>26) + noise
		}

		frameFade := 1.0 - 0.25*float64(i)/float64(max(1, d.frameSize-1))
		lossFade := math.Pow(0.85, float64(d.plcCount-1))
		output[i] = clampPCM32(int64(math.Round(float64(pred) * frameFade * lossFade)))
	}

	d.updatePLCState(output)
	result := make([]float64, d.frameSize)
	for i, s := range output {
		result[i] = float64(s) / 32768.0
	}

	if d.channels == 2 {
		stereo := make([]float64, d.frameSize*2)
		for i, s := range result {
			stereo[i*2] = s
			stereo[i*2+1] = s
		}
		return stereo, nil
	}
	return result, nil
}

func (d *Decoder) updatePLCState(output []int32) {
	if len(output) == 0 {
		return
	}
	if len(d.prevOutput) != d.frameSize {
		d.prevOutput = make([]int32, d.frameSize)
	}
	for i, sample := range output {
		d.prevOutput[i] = sample << 14
	}
	for i := range d.lpcState {
		src := len(output) - len(d.lpcState) + i
		if src >= 0 {
			d.lpcState[i] = output[src] << 14
		} else {
			d.lpcState[i] = 0
		}
	}
	if len(output) <= len(d.ltpState) {
		mvLen := len(d.ltpState) - len(output)
		copy(d.ltpState[:mvLen], d.ltpState[len(output):])
		for i, sample := range output {
			d.ltpState[mvLen+i] = sample
		}
	} else {
		// output longer than the LTP buffer (e.g. 16 kHz: 320 > 288): keep the
		// most recent len(d.ltpState) samples so the pitch history stays fresh.
		offset := len(output) - len(d.ltpState)
		for i := range d.ltpState {
			d.ltpState[i] = output[offset+i]
		}
	}
}

func clampPCM32(v int64) int32 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int32(v)
}

// Reset resets the decoder state.
func (d *Decoder) Reset() {
	for i := range d.prevNLSFQ15 {
		d.prevNLSFQ15[i] = int16((float64(i+1) / float64(d.lpcOrder+1)) * 32768.0)
	}
	d.lagPrev = 100
	d.prevLagIndex = 0
	d.prevGainQ16 = 65536
	d.prevGainIndex = 10
	d.prevSignalType = SignalTypeUnvoiced
	d.firstFrame = true
	d.lastFinalRange = 0
	for i := range d.lpcState {
		d.lpcState[i] = 0
	}
	for i := range d.ltpState {
		d.ltpState[i] = 0
	}
	d.randSeed = 7818
	d.plcCount = 0
	d.lastFrameSilent = false
	d.stereoPredPrevQ13 = [2]int32{}
	d.stereoMid = [2]int16{}
	d.stereoSide = [2]int16{}
	d.prevDecodeOnlyMiddle = false
	if d.side != nil {
		d.side.Reset()
	}
}

// CopyPrimaryStateFrom copies the mono/mid-channel decoder state from src.
// Stereo side-channel and M/S predictor state are intentionally left intact.
func (d *Decoder) CopyPrimaryStateFrom(src *Decoder) {
	if src == nil {
		return
	}
	copy(d.prevNLSFQ15, src.prevNLSFQ15)
	d.lagPrev = src.lagPrev
	d.prevLagIndex = src.prevLagIndex
	d.prevGainQ16 = src.prevGainQ16
	d.prevGainIndex = src.prevGainIndex
	d.prevSignalType = src.prevSignalType
	d.firstFrame = src.firstFrame
	copy(d.lpcState, src.lpcState)
	copy(d.ltpState, src.ltpState)
	d.randSeed = src.randSeed
	d.plcCount = src.plcCount
	d.lastFrameSilent = src.lastFrameSilent
	copy(d.prevLPCQ12, src.prevLPCQ12)
	copy(d.prevOutput, src.prevOutput)
}

// DequantizeSubframeGains dequantizes subframe gain indices (API compatibility).
func DequantizeSubframeGains(indices []int) []float64 {
	gains := make([]float64, len(indices))
	for i, idx := range indices {
		gainDB := float64(idx)*0.5 - 10.0
		gains[i] = math.Pow(10.0, gainDB/20.0)
	}
	return gains
}
