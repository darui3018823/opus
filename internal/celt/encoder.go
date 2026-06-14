package celt

import (
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/darui3018823/opus/internal/dsp"
	"github.com/darui3018823/opus/internal/entcode"
)

var encDebug = os.Getenv("OPUS_ENC_DEBUG") != ""
var encTrace = os.Getenv("OPUS_ENC_TRACE") != ""

func etr(enc *entcode.Encoder, label string) {
	if encTrace {
		fmt.Fprintf(os.Stderr, "[ENC %-12s] tell=%d tellf=%d rng=%08x\n",
			label, enc.ECTell(), enc.TellFrac(), enc.GetRng())
	}
}

// Encoder is a CELT encoder instance. Its output is decoded by the CELT decoder
// in this package: the encode path is the structural mirror of decodeCELTRange,
// emitting the same range-coder symbol sequence in the same order so that the
// (RFC-conformant) decoder reconstructs the signal.
type Encoder struct {
	mode     *Mode
	celtMode *dsp.CELTMode // forward (analysis) MDCT, N-point long block

	// Per-channel streaming state.
	overlap    [][]float64 // MDCT analysis overlap memory (Overlap samples each)
	preemphMem []float64   // pre-emphasis filter memory (×32768 domain: 0.85*prev)

	// Encoder configuration.
	bitrate    int
	complexity int
	vbr        bool

	// Inter-frame coarse-energy predictor state (oldBandE), channel-major
	// mean-subtracted log2-amplitude. Mirrors the decoder's prevEnergies.
	prevBandEnergies []float64
	// foldSeed mirrors the decoder's lastFinalRange (the range value used to seed
	// PVQ noise folding). Because the range register evolves identically in the
	// encoder and decoder for the same symbols, storing enc.GetRng() before flush
	// reproduces the decoder's lastFinalRange for the next frame.
	foldSeed   uint32
	finalRange uint32
	frameCount int
}

// FinalRange returns the range coder rng after the most recent Encode (before
// flush). For a correctly paired packet it equals the decoder's LastFinalRange.
func (e *Encoder) FinalRange() uint32 { return e.finalRange }

// EncoderConfig holds encoder configuration
type EncoderConfig struct {
	Bitrate    int  // Target bitrate in bps
	Complexity int  // Complexity level (0-10)
	VBR        bool // Variable bitrate
}

// DefaultEncoderConfig returns default encoder configuration
func DefaultEncoderConfig() *EncoderConfig {
	return &EncoderConfig{
		Bitrate:    64000, // 64 kbps
		Complexity: 5,     // Medium complexity
		VBR:        false,
	}
}

// NewEncoder creates a new CELT encoder
func NewEncoder(frameSize, sampleRate, channels int, config *EncoderConfig) (*Encoder, error) {
	if channels < 1 || channels > 2 {
		return nil, errors.New("celt: only mono and stereo supported")
	}

	if config == nil {
		config = DefaultEncoderConfig()
	}

	mode := NewMode(frameSize, sampleRate, channels)
	overlap := mode.Overlap
	celtMode := dsp.NewCELTMode(frameSize, overlap, celtWindow(overlap))

	e := &Encoder{
		mode:       mode,
		celtMode:   celtMode,
		overlap:    make([][]float64, channels),
		preemphMem: make([]float64, channels),
		bitrate:    config.Bitrate,
		complexity: config.Complexity,
		vbr:        config.VBR,
	}
	for c := 0; c < channels; c++ {
		e.overlap[c] = make([]float64, overlap)
	}

	// oldBandE history is zeroed by libopus OPUS_RESET_STATE.
	e.prevBandEnergies = make([]float64, channels*mode.Bands.NumBands)

	return e, nil
}

// targetBytes returns the fixed packet size (bytes) for this frame. The decoder
// uses len(packet)*8 as its bit-allocation budget, so the encoder commits to this
// size up front, runs the whole allocation against it, and pads the output to it.
func (e *Encoder) targetBytes() int {
	frameDuration := float64(e.mode.FrameSize) / float64(e.mode.SampleRate)
	tb := int(float64(e.bitrate) * frameDuration / 8.0)
	if tb < 2 {
		tb = 2
	}
	if tb > 1275 {
		tb = 1275
	}
	return tb
}

// Encode encodes one CELT frame (interleaved float64 PCM, FrameSize*Channels).
func (e *Encoder) Encode(samples []float64) ([]byte, error) {
	if len(samples) != e.mode.FrameSize*e.mode.Channels {
		return nil, errors.New("celt: invalid input size")
	}

	ch := e.mode.Channels
	numBands := e.mode.Bands.NumBands
	nbEBands := NumBands48000
	lm := e.mode.LM
	frameSize := e.mode.FrameSize
	ov := e.mode.Overlap
	start, end := 0, numBands
	M := 1 << uint(lm)
	frameLen := M * int(EBands48000[numBands])

	targetBytes := e.targetBytes()
	totalBits := targetBytes * 8
	enc := entcode.NewEncoder(targetBytes)

	// --- Analysis: pre-emphasis, forward MDCT, band energy, normalisation ---
	X := make([]float64, ch*frameLen)
	// bandE holds per-band amplitude in libopus channel-major [c*nbEBands+i]
	// layout (used by stereo intensity/split inside quant_all_bands).
	bandE := make([]float64, 2*nbEBands)
	logE := make([]float64, ch*numBands)

	for c := 0; c < ch; c++ {
		pe := make([]float64, frameSize)
		mem := e.preemphMem[c]
		for i := 0; i < frameSize; i++ {
			var s float64
			if ch == 1 {
				s = samples[i]
			} else {
				s = samples[i*ch+c]
			}
			xin := s * 32768.0
			pe[i] = xin - mem
			mem = 0.85 * xin
		}
		e.preemphMem[c] = mem

		buf := make([]float64, frameSize+ov)
		copy(buf[:ov], e.overlap[c])
		copy(buf[ov:], pe)
		coeffs := e.celtMode.CLTMDCTForward(buf)
		copy(e.overlap[c], buf[frameSize:frameSize+ov])

		base := c * frameLen
		for i := 0; i < numBands; i++ {
			lo := M * int(EBands48000[i])
			hi := M * int(EBands48000[i+1])
			sumsq := 1e-27
			for j := lo; j < hi; j++ {
				sumsq += coeffs[j] * coeffs[j]
			}
			amp := math.Sqrt(sumsq)
			bandE[c*nbEBands+i] = amp
			logE[c*numBands+i] = math.Log2(amp) - EMean(i)
			inv := 1.0 / amp
			for j := lo; j < hi; j++ {
				X[base+j] = coeffs[j] * inv
			}
		}
	}

	// === Header symbols, in decoder order (decodeCELTRange) ===

	// Silence flag (logp 15) — first symbol. No silence detection yet.
	if enc.ECTell()+1 <= totalBits {
		enc.EncodeBitLogp(false, 15)
	}
	etr(enc, "silence")

	// Post-filter (CELT-only, start==0): signalled disabled.
	if start == 0 && enc.ECTell()+16 <= totalBits {
		enc.EncodeBitLogp(false, 1)
	}

	// isTransient (logp 3, LM>0) — long blocks only for now.
	isTransient := false
	if lm > 0 && enc.ECTell()+3 <= totalBits {
		enc.EncodeBitLogp(isTransient, 3)
	}

	// intra/inter flag for coarse energy.
	intra := e.frameCount == 0
	if enc.ECTell()+3 <= totalBits {
		enc.EncodeBitLogp(intra, 3)
	}

	etr(enc, "intra")

	// Coarse band energies.
	quantLogE := QuantizeCoarseEnergy(enc, logE, e.prevBandEnergies, nil,
		intra, numBands, start, end, lm, ch, totalBits)
	etr(enc, "coarse")

	// Time-frequency allocation (flat tf_res=0).
	tfRes := make([]int, numBands)
	tfEncode(enc, start, end, isTransient, tfRes, lm, 0, totalBits)
	etr(enc, "tf_encode")

	// Spread decision (SPREAD_NORMAL).
	spread := 2
	if enc.ECTell()+4 <= totalBits {
		enc.EncodeIcdf(spread, spreadIcdf[:], 5)
	}
	etr(enc, "spread")

	// Dynamic allocation boosts (none).
	offsets := make([]int, numBands)
	dynallocEncode(enc, offsets, numBands, start, end, lm, ch, totalBits)
	etr(enc, "dynalloc")

	// Allocation trim (neutral = 5).
	allocTrim := 5
	if enc.ECTell()+6 <= totalBits {
		enc.EncodeIcdf(allocTrim, TrimICDF[:], 7)
	}
	etr(enc, "alloc_trim")

	// === Bit allocation, fine energy, PVQ, anti-collapse, final fine ===
	bitsQ3 := totalBits<<3 - enc.TellFrac() - 1
	antiCollapseRsv := 0
	if isTransient && lm >= 2 && bitsQ3 >= (lm+2)<<3 {
		antiCollapseRsv = 1 << 3
	}
	bitsQ3 -= antiCollapseRsv
	if bitsQ3 < 0 {
		bitsQ3 = 0
	}

	encIntensity := end
	encDualStereo := false
	pulses, eBits, finePriority, balance, intensity, codedBands, dualStereo :=
		computeAllocationEncode(enc, encIntensity, encDualStereo,
			numBands, start, end, lm, ch, allocTrim, bitsQ3, offsets)

	if encDebug && e.frameCount == 10 {
		fmt.Fprintf(os.Stderr, "[ENC] bitsQ3=%d codedBands=%d\n", bitsQ3, codedBands)
		for i := start; i < end; i++ {
			N := M * int(EBands48000[i+1]-EBands48000[i])
			q := celtBits2PulsesQ3(i, lm, pulses[i])
			fmt.Fprintf(os.Stderr, "[ENC] band %d N=%d bandE=%.4f pulsesQ3=%d K=%d eBits=%d\n",
				i, N, bandE[i], pulses[i], getPulses(q), eBits[i])
		}
	}

	// Fine energy — raw bits to packet end, FORWARD band order. Track the
	// residual error so the final-fine pass and the next-frame predictor match
	// what the decoder reconstructs.
	fineError := make([]float64, ch*numBands)
	for idx := range fineError {
		fineError[idx] = logE[idx] - quantLogE[idx]
	}
	for i := start; i < end; i++ {
		fb := eBits[i]
		if fb <= 0 {
			continue
		}
		frac := 1 << uint(fb)
		for c := 0; c < ch; c++ {
			idx := c*numBands + i
			q2 := int(math.Floor((fineError[idx] + 0.5) * float64(frac)))
			if q2 > frac-1 {
				q2 = frac - 1
			}
			if q2 < 0 {
				q2 = 0
			}
			enc.EncodeBits(uint32(q2), uint(fb))
			offset := (float64(q2)+0.5)/float64(frac) - 0.5
			quantLogE[idx] += offset
			fineError[idx] -= offset
		}
	}

	// PVQ for all bands.
	var Y []float64
	if ch == 2 {
		Y = X[frameLen:]
	}
	collapse := make([]byte, numBands*ch)
	totalBitsQ3 := totalBits<<3 - antiCollapseRsv
	e.foldSeed = QuantAllBandsEncode(enc, bandE, start, end, X[:frameLen], Y, collapse,
		pulses, isTransient, spread, dualStereo, intensity, tfRes,
		totalBitsQ3, balance, lm, codedBands, e.foldSeed, false)

	// Anti-collapse bit (raw) — disabled for now (only reserved on transients).
	if antiCollapseRsv > 0 {
		enc.EncodeBits(0, 1)
	}

	// Final fine energy — one extra bit per (band,channel) by priority.
	bitsLeft := totalBits - enc.ECTell()
	for prio := 0; prio < 2; prio++ {
		for i := start; i < end && bitsLeft >= ch; i++ {
			if eBits[i] >= MaxFineEnergy || finePriority[i] != prio {
				continue
			}
			for c := 0; c < ch; c++ {
				idx := c*numBands + i
				q2 := 0
				if fineError[idx] >= 0 {
					q2 = 1
				}
				enc.EncodeBits(uint32(q2), 1)
				offset := (float64(q2) - 0.5) * math.Exp2(float64(-eBits[i]-1))
				quantLogE[idx] += offset
				fineError[idx] -= offset
				bitsLeft--
			}
		}
	}

	e.finalRange = enc.GetRng()

	// Update the inter-frame predictor with the fine-corrected energies.
	for idx := range quantLogE {
		v := quantLogE[idx]
		if v < -28.0 {
			v = -28.0
		}
		e.prevBandEnergies[idx] = v
	}
	e.frameCount++

	etr(enc, "final")
	enc.Flush()
	out := enc.Bytes()
	if len(out) < targetBytes {
		padded := make([]byte, targetBytes)
		copy(padded, out)
		out = padded
	}
	return out, nil
}

// Reset resets the encoder state.
func (e *Encoder) Reset() {
	for c := range e.overlap {
		for i := range e.overlap[c] {
			e.overlap[c][i] = 0
		}
		e.preemphMem[c] = 0
	}
	for i := range e.prevBandEnergies {
		e.prevBandEnergies[i] = 0
	}
	e.foldSeed = 0
	e.frameCount = 0
}

// SetBitrate sets the target bitrate.
func (e *Encoder) SetBitrate(bitrate int) {
	if bitrate > 0 {
		e.bitrate = bitrate
	}
}

// SetComplexity sets the encoding complexity.
func (e *Encoder) SetComplexity(complexity int) {
	if complexity >= 0 && complexity <= 10 {
		e.complexity = complexity
	}
}
