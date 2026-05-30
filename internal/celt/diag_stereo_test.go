package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/darui3018823/opus/internal/entcode"
)

// findPacket returns the payload (including TOC) of the 0-based packet index in a
// .bit file, plus the expected final range.
func findPacket(t *testing.T, path string, idx int) (pkt []byte, rexp uint32) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("testdata not found:", err)
	}
	off, i := 0, 0
	for off+8 <= len(data) {
		size := int(binary.BigEndian.Uint32(data[off:]))
		expected := binary.BigEndian.Uint32(data[off+4:])
		if off+8+size > len(data) || size < 1 {
			break
		}
		p := data[off+8 : off+8+size]
		off += 8 + size
		if i == idx {
			return p, expected
		}
		i++
	}
	t.Fatalf("packet %d not found", idx)
	return nil, 0
}

// TestOracleTraceStereo replays a single stereo CELT packet stage-by-stage to
// diff against the instrumented-libopus oracle and locate the first divergence
// in the stereo decode path.
func TestOracleTraceStereo(t *testing.T) {
	pktIdx := 2128 // tv07 stereo packet (oracle 0-based index)
	if v := os.Getenv("PKTIDX"); v != "" {
		fmt.Sscanf(v, "%d", &pktIdx)
	}
	tv := "07"
	if v := os.Getenv("TV"); v != "" {
		tv = v
	}
	pkt, expectedFinal := findPacket(t, "../../testdata/opus_newvectors/testvector"+tv+".bit", pktIdx)
	toc := pkt[0]
	config := int((toc >> 3) & 0x1f)
	stereo := (toc>>2)&1 != 0
	frameData := pkt[1:]
	totalBits := len(frameData) * 8
	lm := config & 3
	bwBands := []int{13, 17, 19, 21}
	numBands := bwBands[(config-16)/4]
	ch := 1
	if stereo {
		ch = 2
	}
	fmt.Printf("pkt%d config=%d stereo=%v ch=%d size=%d rexp=%08x\n", pktIdx, config, stereo, ch, len(frameData), expectedFinal)

	prevLogE := make([]float64, numBands*ch)
	prevLogE2 := make([]float64, numBands*ch)
	for i := range prevLogE {
		prevLogE[i] = -28.0
		prevLogE2[i] = -28.0
	}

	dec := entcode.NewDecoder(frameData)
	snap := func(label string) {
		fmt.Printf("[%-12s] tell=%d tellf=%d rng=%08x dif=%08x\n", label, dec.ECTell(), dec.TellFrac(), dec.GetRng(), dec.GetDif())
	}

	silence := false
	if dec.ECTell() >= totalBits {
		silence = true
	} else if dec.ECTell() == 1 {
		silence = dec.DecodeBitLogp(15)
	}
	_ = silence
	snap("silence")

	if dec.ECTell()+16 <= totalBits {
		DecodePostFilterParams(dec, totalBits, lm)
	}
	snap("postfilter")

	isTransient := false
	if lm > 0 && dec.ECTell()+3 <= totalBits {
		isTransient = dec.DecodeBitLogp(3)
	}
	snap(fmt.Sprintf("isTransient=%v", isTransient))

	var intra bool
	if dec.ECTell()+3 <= totalBits {
		intra = dec.DecodeBitLogp(3)
	}
	snap(fmt.Sprintf("intra=%v", intra))

	UnquantizeCoarseEnergy(dec, prevLogE, prevLogE2, intra, numBands, lm, ch, totalBits)
	snap("coarse")

	tfRes := celtTFDecode(dec, totalBits, isTransient, numBands, lm)
	snap("tf")

	if dec.ECTell()+4 <= totalBits {
		dec.DecodeIcdf(spreadIcdf[:], 5)
	}
	snap("spread")

	offsets := decodeDynalloc(dec, numBands, lm, ch, totalBits)
	snap("dynalloc")
	fmt.Printf("   offsets: %v\n", offsets)

	allocTrim := 5
	if dec.ECTell()+6 <= totalBits {
		allocTrim = dec.DecodeIcdf(TrimICDF[:], 7)
	}
	snap(fmt.Sprintf("alloc_trim=%d", allocTrim))

	bitsQ3 := len(frameData)*8<<3 - dec.TellFrac() - 1
	antiRsv := 0
	if isTransient && lm >= 2 && bitsQ3 >= (lm+2)<<3 {
		antiRsv = 1 << 3
	}
	bitsQ3 -= antiRsv
	allocDebug = os.Getenv("ALLOCDBG") != ""
	pulses, eBits, _, balance, intensity, codedBands, dualStereo := computeAllocation(dec, numBands, lm, ch, allocTrim, bitsQ3, offsets)
	allocDebug = false
	snap("allocation")
	fmt.Printf("   codedBands=%d balance=%d intensity=%d dual_stereo=%v antiRsv=%d\n", codedBands, balance, intensity, dualStereo, antiRsv)
	fmt.Printf("   pulses: %v\n", pulses)
	fmt.Printf("   eBits : %v\n", eBits)

	for i := 0; i < numBands; i++ {
		fb := eBits[i]
		if fb <= 0 {
			continue
		}
		for c := 0; c < ch; c++ {
			dec.DecodeBits(uint(fb))
		}
	}
	snap("fine_energy")

	M := 1 << uint(lm)
	frameLen := M * int(EBands48000[numBands])
	X := make([]float64, ch*frameLen)
	var Y []float64
	if ch == 2 {
		Y = X[frameLen:]
	}
	collapse := make([]byte, numBands*ch)
	totalBitsQ3 := len(frameData)*8<<3 - antiRsv
	qabDebug = true
	qabLog = nil
	qabDP = nil
	qabTheta = nil
	QuantAllBands(dec, 0, numBands, X[:frameLen], Y, collapse, pulses, isTransient, 2,
		dualStereo, intensity, tfRes, totalBitsQ3, balance, lm, codedBands,
		0, false)
	qabDebug = false
	for _, tr := range qabLog {
		fmt.Printf("  QB band%2d N=%3d b=%5d -> tellf=%d rng=%08x xcm=%d\n", tr.i, tr.N, tr.b, tr.tellf, tr.rng, tr.xcm)
	}
	for _, th := range qabTheta {
		fmt.Printf("    TH band=%d n=%d qn=%d itheta=%d qalloc=%d tellf=%d\n", th[0], th[1], th[2], th[3], th[4], th[5])
	}
	for _, d := range qabDP {
		fmt.Printf("    DP n=%d k=%d V=%d idx=%d dif %08x->%08x tellf %d->%d (d=%d)\n", d[0], d[1], d[2], d[3], d[4], d[5], d[6], d[7], d[7]-d[6])
	}
	snap("pvq")

	if antiRsv > 0 {
		dec.DecodeBits(1)
	}
	snap("anticollapse")

	got := dec.GetRng()
	fmt.Printf("\nFinal rng: got=%08x expected=%08x match=%v\n", got, expectedFinal, got == expectedFinal)
}
