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

// RateMode selects the bitrate control strategy for the CELT encoder.
type RateMode int

const (
	// RateModeCBR produces fixed-size packets equal to targetBytes.
	RateModeCBR RateMode = iota
	// RateModeVBR produces variable-size packets — the encoder shrinks the
	// packet to the actual bytes used after encoding (unconstrained VBR).
	RateModeVBR
	// RateModeCVBR is constrained VBR: like VBR but the output size is
	// clamped so the average bitrate stays close to the target. A per-frame
	// reservoir (vbrOffset) accumulates surplus/deficit.
	RateModeCVBR
)

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
	rateMode   RateMode

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

	// CVBR reservoir: accumulated bit surplus/deficit in Q8 bits. Positive means
	// the encoder has used fewer bits than the target and can afford to spend
	// more; negative means it has overspent. Clamped to [-maxReservoir, maxReservoir].
	vbrOffset    int
	vbrCount     int // number of VBR frames encoded (for average computation)
	vbrDriftComp int // drift compensation accumulator
}

// FinalRange returns the range coder rng after the most recent Encode (before
// flush). For a correctly paired packet it equals the decoder's LastFinalRange.
func (e *Encoder) FinalRange() uint32 { return e.finalRange }

// EncoderConfig holds encoder configuration
type EncoderConfig struct {
	Bitrate    int      // Target bitrate in bps
	Complexity int      // Complexity level (0-10)
	RateMode   RateMode // Rate control mode (CBR/VBR/CVBR)
}

// DefaultEncoderConfig returns default encoder configuration
func DefaultEncoderConfig() *EncoderConfig {
	return &EncoderConfig{
		Bitrate:    64000,       // 64 kbps
		Complexity: 5,           // Medium complexity
		RateMode:   RateModeCBR, // CBR (backward compatible)
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
		rateMode:   config.RateMode,
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

	maxTargetBytes := e.targetBytes()

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

	// --- VBR target computation ---
	// In VBR/CVBR mode, adjust the allocation budget based on signal activity.
	// This mirrors libopus celt_encode_with_ec VBR logic (simplified):
	//   - Compute total band energy as a proxy for signal activity.
	//   - Scale targetBytes between a floor (minBytes) and the full CBR target.
	//   - Silent or near-silent frames get a smaller budget; complex frames
	//     get the full budget.
	targetBytes := maxTargetBytes

	if e.rateMode != RateModeCBR {
		// Sum log-domain band energies to estimate activity.
		// EMean-subtracted logE is ~0 for a typical signal; very negative = quiet.
		var activity float64
		for c := 0; c < ch; c++ {
			for i := start; i < end; i++ {
				v := logE[c*numBands+i]
				if v > 0 {
					activity += v
				} else if v > -10.0 {
					// Only lightly penalize moderately quiet bands
					activity += v * 0.05
				}
			}
		}
		// Normalise to [0, 1] range. A fully active signal with numBands
		// bands at logE~+3 each gives activity ~63; we saturate at that.
		maxActivity := float64(ch*numBands) * 3.0
		if maxActivity < 1 {
			maxActivity = 1
		}
		frac := activity / maxActivity
		if frac > 1.0 {
			frac = 1.0
		}
		if frac < 0.0 {
			frac = 0.0
		}

		// Minimum packet size: enough for header symbols + some coarse energy.
		minBytes := maxTargetBytes / 4
		if minBytes < 2 {
			minBytes = 2
		}

		// Scale target: frac=1 → full budget; frac=0 → minBytes.
		// Use a sqrt curve so that moderate signals still get most of the budget.
		scaledFrac := math.Sqrt(frac)
		targetBytes = minBytes + int(scaledFrac*float64(maxTargetBytes-minBytes)+0.5)
		if targetBytes > maxTargetBytes {
			targetBytes = maxTargetBytes
		}
		if targetBytes < minBytes {
			targetBytes = minBytes
		}

		// CVBR reservoir adjustment: use accumulated surplus to boost budget
		// when the signal needs it.
		if e.rateMode == RateModeCVBR && e.vbrOffset > 0 {
			// Allow spending up to half the surplus on this frame.
			boostBytes := (e.vbrOffset >> (3 + 3)) / 2 // Q8 bits → bytes, halved
			targetBytes += boostBytes
			if targetBytes > maxTargetBytes {
				targetBytes = maxTargetBytes
			}
		}
	}

	totalBits := targetBytes * 8

	// Allocate the entropy coder at the full (CBR) budget, then shrink it to the
	// chosen VBR target before any symbols are written (libopus ec_enc_shrink).
	//
	// Shrinking BEFORE the header/coarse-energy symbols — rather than after, as
	// libopus celt_encode_with_ec does — is deliberate. The coarse-energy path
	// selection (QuantizeCoarseEnergy) and clt_compute_allocation both read the
	// budget as packet_length*8, and the decoder derives that from the FINAL
	// (shrunk) packet length. Encoding against the shrunk budget here keeps those
	// decisions bit-symmetric with the decoder. libopus can defer the shrink to
	// after coarse energy because its budget guards never bind that early at its
	// bitrates; doing the same unconditionally here would risk a low-bitrate
	// stereo desync (the bitsLeft<30 qi-clamp in QuantizeCoarseEnergy can trip on
	// the smaller decoder-side budget but not on the larger pre-shrink budget).
	enc := entcode.NewEncoder(maxTargetBytes)
	if targetBytes < maxTargetBytes {
		enc.Shrink(targetBytes)
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

	// --- Rate mode: determine final packet size ---
	switch e.rateMode {
	case RateModeVBR:
		// VBR packet size is exactly the chosen targetBytes.
		// The variance comes from targetBytes being adjusted based on
		// signal activity before allocation.

	case RateModeCVBR:
		// Update CVBR reservoir.
		// targetBytes is the actual chosen size for this frame.
		// maxTargetBytes is the nominal CBR target.
		targetBitsQ8 := maxTargetBytes << (3 + 3)
		usedBitsQ8 := targetBytes << (3 + 3)

		// maxReservoir limits how much the average can drift from the target.
		maxReservoir := 3 * targetBitsQ8
		if maxReservoir < 16<<3 {
			maxReservoir = 16 << 3
		}

		delta := targetBitsQ8 - usedBitsQ8 // positive = underspend (surplus)

		newOffset := e.vbrOffset + delta
		if newOffset > maxReservoir {
			newOffset = maxReservoir
		}
		if newOffset < -maxReservoir {
			newOffset = -maxReservoir
		}
		e.vbrOffset = newOffset

	default:
		// CBR
	}

	out := enc.Bytes()
	// Bytes() already returns max(capacity, range_front+raw_tail), so a genuine
	// over-budget frame is reflected in len(out); the merge byte is shared between
	// the range front and the raw tail, so the physical size is NOT (ec_tell+7)/8
	// (that would double-count the shared byte). Just pad up to the committed
	// target — for VBR this is the shrunk size, for CBR the full budget.
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
	e.vbrOffset = 0
	e.vbrCount = 0
	e.vbrDriftComp = 0
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

// SetRateMode sets the rate control mode.
func (e *Encoder) SetRateMode(mode RateMode) {
	e.rateMode = mode
}

// RateMode returns the current rate control mode.
func (e *Encoder) GetRateMode() RateMode { return e.rateMode }
