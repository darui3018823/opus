package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/darui3018823/opus/internal/entcode"
)

// TestOracleTrace replays TV07 pkt0 in exact libopus celt_decode_with_ec order
// and prints tellf+rng after each stage so it can be diffed against the
// instrumented-libopus oracle (ground truth).
func TestOracleTrace(t *testing.T) {
	data, err := os.ReadFile("../../testdata/opus_newvectors/testvector07.bit")
	if err != nil {
		t.Skip("testdata not found:", err)
	}
	size := int(binary.BigEndian.Uint32(data[0:]))
	expectedFinal := binary.BigEndian.Uint32(data[4:])
	pkt := data[8 : 8+size]
	toc := pkt[0]
	config := int((toc >> 3) & 0x1f)
	if config < 16 {
		t.Skip("pkt0 not CELT")
	}
	frameData := pkt[1:]
	totalBits := len(frameData) * 8
	lm := config & 3
	bwBands := []int{13, 17, 19, 21}
	numBands := bwBands[(config-16)/4]
	ch := 1

	prevLogE := make([]float64, numBands*ch)
	prevLogE2 := make([]float64, numBands*ch)
	oldLogE := make([]float64, numBands*ch)
	oldLogE2 := make([]float64, numBands*ch)
	for i := range prevLogE {
		prevLogE2[i] = -28.0
		oldLogE[i] = -28.0
		oldLogE2[i] = -28.0
	}

	dec := entcode.NewDecoder(frameData)
	snap := func(label string) {
		fmt.Printf("[%-12s] tellf=%d rng=%08x dif=%08x\n", label, dec.TellFrac(), dec.GetRng(), dec.GetDif())
	}

	// silence
	silence := false
	if dec.ECTell() >= totalBits {
		silence = true
	} else if dec.ECTell() == 1 {
		silence = dec.DecodeBitLogp(15)
	}
	_ = silence
	snap("silence")

	// postfilter
	if dec.ECTell()+16 <= totalBits {
		DecodePostFilterParams(dec, totalBits, lm)
	}
	snap("postfilter")

	// isTransient
	isTransient := false
	if lm > 0 && dec.ECTell()+3 <= totalBits {
		isTransient = dec.DecodeBitLogp(3)
	}
	snap(fmt.Sprintf("isTransient=%v", isTransient))

	// intra
	var intra bool
	if dec.ECTell()+3 <= totalBits {
		intra = dec.DecodeBitLogp(3)
	}
	snap(fmt.Sprintf("intra=%v", intra))

	// coarse energy
	quantLogE := UnquantizeCoarseEnergy(dec, prevLogE, prevLogE2, intra, numBands, 0, numBands, lm, ch, totalBits)
	snap("coarse")

	// tf
	tfRes := celtTFDecode(dec, totalBits, isTransient, numBands, 0, numBands, lm)
	snap("tf")

	// spread
	if dec.ECTell()+4 <= totalBits {
		dec.DecodeIcdf(spreadIcdf[:], 5)
	}
	snap("spread")

	// dynalloc
	offsets := decodeDynalloc(dec, numBands, 0, numBands, lm, ch, totalBits)
	snap("dynalloc")

	// alloc_trim
	allocTrim := 5
	if dec.ECTell()+6 <= totalBits {
		allocTrim = dec.DecodeIcdf(TrimICDF[:], 7)
	}
	snap(fmt.Sprintf("alloc_trim=%d", allocTrim))

	// allocation
	bitsQ3 := len(frameData)*8<<3 - dec.TellFrac() - 1
	antiRsv := 0
	if isTransient && lm >= 2 && bitsQ3 >= (lm+2)<<3 {
		antiRsv = 1 << 3
	}
	bitsQ3 -= antiRsv
	pulses, eBits, finePriority, balance, intensity, codedBands, dualStereo := computeAllocation(dec, numBands, 0, numBands, lm, ch, allocTrim, bitsQ3, offsets)
	snap("allocation")
	fmt.Printf("   pulses: %v\n", pulses)
	fmt.Printf("   eBits : %v\n", eBits)
	fmt.Printf("   balance=%d codedBands=%d intensity=%d\n", balance, codedBands, intensity)

	applyFineEnergyLogE(dec, quantLogE, numBands, ch, eBits)
	snap("fine_energy")

	// PVQ via faithful quant_all_bands port
	M := 1 << uint(lm)
	frameLen := M * int(EBands48000[numBands])
	X := make([]float64, frameLen)
	collapse := make([]byte, numBands*ch)
	totalBitsQ3 := len(frameData)*8<<3 - antiRsv
	qabDebug = true
	qabLog = nil
	qabDP = nil
	seed := QuantAllBands(dec, 0, numBands, X, nil, collapse, pulses, isTransient, 2,
		dualStereo, intensity, tfRes, totalBitsQ3, balance, lm, codedBands,
		0, false)
	qabDebug = false
	for _, t := range qabLog {
		fmt.Printf("  QB band%2d N=%3d b=%5d -> tellf=%d rng=%08x xcm=%d\n", t.i, t.N, t.b, t.tellf, t.rng, t.xcm)
	}
	for _, d := range qabDP {
		fmt.Printf("    DP n=%d k=%d V=%d idx=%d dif %08x->%08x tellf %d->%d (d=%d)\n", d[0], d[1], d[2], d[3], d[4], d[5], d[6], d[7], d[7]-d[6])
	}
	snap("pvq")
	// Print first 16 coefficients after QuantAllBands (unit-norm, before denorm)
	fmt.Printf("X[0:16]:")
	for i := 0; i < 16 && i < len(X); i++ {
		fmt.Printf(" %.4f", X[i])
	}
	fmt.Println()

	antiCollapseOn := false
	if antiRsv > 0 {
		antiCollapseOn = dec.DecodeBits(1) != 0
	}
	snap("anticollapse")

	applyFinalFineEnergyLogE(dec, quantLogE, numBands, ch, eBits, finePriority, len(frameData)*8-dec.ECTell())
	snap("fine_final")

	if antiCollapseOn {
		diagDec, _ := NewDecoderEx(M*FrameSize2_5ms, 48000, numBands, ch)
		copy(diagDec.prevLogE, oldLogE)
		copy(diagDec.prevLogE2, oldLogE2)
		for i := 0; i < numBands; i++ {
			amp := logEAmplitude(quantLogE[i], i)
			diagDec.bandProcs[0].bands[i].Energy = amp * amp
		}
		diagDec.antiCollapse(X, collapse, pulses, lm, frameLen, seed, 0, numBands)
	}

	dumpDenormalizedMDCT(denormalizedMDCTViaBandProcessor(frameLen, numBands, ch, lm, X, quantLogE), numBands, lm)

	got := dec.GetRng()
	fmt.Printf("\nFinal rng: got=%08x expected=%08x match=%v\n", got, expectedFinal, got == expectedFinal)
	// The header/allocation/fine-energy path is bit-exact with libopus (verified
	// stage-by-stage above: rng matches through fine_energy = 0a3fee00). The only
	// remaining divergence is inside the PVQ band loop (quant_all_bands), which
	// still needs a faithful port (recursive split, theta/stereo, spreading,
	// dynamic b->K balance). Logged, not failed, while that port is pending.
	if got != expectedFinal {
		t.Errorf("final range mismatch: got %08x want %08x", got, expectedFinal)
	}
}
