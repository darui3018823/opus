package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/darui3018823/opus/internal/entcode"
)

// TestBisectFinalRange manually decodes TV07 pkt0 step-by-step and compares
// the final range coder rng against the expected value 0x2c33ee00.
// This identifies exactly where the range coder desyncs from libopus.
func TestBisectFinalRange(t *testing.T) {
	data, err := os.ReadFile("../../testdata/opus_newvectors/testvector07.bit")
	if err != nil {
		t.Skip("testdata not found:", err)
	}

	// Read pkt0: 4-byte size, 4-byte expected final range, N bytes packet
	size := int(binary.BigEndian.Uint32(data[0:]))
	expectedFinal := binary.BigEndian.Uint32(data[4:])
	pkt := data[8 : 8+size]

	toc := pkt[0]
	config := int((toc >> 3) & 0x1f)
	stereo := (toc>>2)&1 != 0
	ch := 1
	if stereo {
		ch = 2
	}
	fmt.Printf("pkt0: TOC=0x%02x config=%d stereo=%v bytes=%d expectedFinal=0x%08x\n",
		toc, config, stereo, size, expectedFinal)

	if config < 16 {
		t.Skip("pkt0 is not CELT")
	}

	frameData := pkt[1:]
	totalBits := len(frameData) * 8

	// Mode params for CELT config 16-31: bwIdx = (config-16)/4, lmIdx = config & 3
	lmSizes := []int{120, 240, 480, 960}
	bwBands := []int{13, 17, 19, 21}
	bwIdx := (config - 16) / 4
	lmIdx := config & 3
	lm := lmIdx
	numBands := bwBands[bwIdx]

	fmt.Printf("  frameData=%d bytes totalBits=%d numBands=%d lm=%d ch=%d\n",
		len(frameData), totalBits, numBands, lm, ch)

	// Initial energy state (matches NewDecoder init: all -28.0)
	prevLogE := make([]float64, numBands*ch)
	prevLogE2 := make([]float64, numBands*ch)
	for i := range prevLogE {
		prevLogE[i] = -28.0
		prevLogE2[i] = -28.0
	}

	dec := entcode.NewDecoder(frameData)
	snap := func(label string) {
		fmt.Printf("  [%s] tell=%d rng=0x%08x dif=0x%08x\n",
			label, dec.Tell(), dec.GetRng(), dec.GetDif())
	}

	snap("init")

	// Step 1: isTransient
	isTransient := false
	if lm > 0 && dec.Tell()+3 <= totalBits {
		isTransient = dec.DecodeBitLogp(3)
	}
	snap(fmt.Sprintf("isTransient=%v", isTransient))

	// Step 2: intra
	var intra bool
	if dec.Tell()+3 <= totalBits {
		intra = dec.DecodeBitLogp(3)
	}
	snap(fmt.Sprintf("intra=%v", intra))

	// Step 3: coarse energy
	quantLogE := UnquantizeCoarseEnergy(dec, prevLogE, prevLogE2, intra, numBands, lm, ch, totalBits)
	snap("coarse_energy")
	fmt.Printf("    quantLogE[0..4]: %.3f %.3f %.3f %.3f %.3f\n",
		quantLogE[0], quantLogE[1], quantLogE[2], quantLogE[3], quantLogE[4])

	// Step 4: TF decode
	celtTFDecode(dec, totalBits, isTransient, numBands, lm)
	snap("tf_decode")

	// Step 5: post-filter
	_, _, pfEnabled := DecodePostFilterParams(dec, totalBits, lm)
	snap(fmt.Sprintf("post_filter pfEnabled=%v", pfEnabled))

	// Step 6: alloc_trim
	allocTrim := 5
	if dec.Tell()+6 <= totalBits {
		allocTrim = dec.DecodeIcdf(TrimICDF[:], 7)
	}
	snap(fmt.Sprintf("alloc_trim=%d", allocTrim))

	// Step 7: compute budget and allocation.
	// NOTE: stereo params and skip bits are now decoded inside computeAllocation.
	remaining := totalBits - dec.Tell()
	if remaining < 0 {
		remaining = 0
	}

	antiCollapseRsv := 0
	if isTransient && lm >= 2 && remaining >= lm+2 {
		antiCollapseRsv = 1
		remaining--
	}

	pulses, eBits, intensity, dualStereo := computeAllocation(dec, numBands, lm, ch, allocTrim, remaining)
	_ = intensity
	_ = dualStereo
	snap(fmt.Sprintf("after_alloc remaining=%d antiRsv=%d", remaining, antiCollapseRsv))
	fmt.Printf("    pulses all: %v\n", pulses)
	fmt.Printf("    eBits  all: %v\n", eBits)

	// Diagnostic: print allocQ3 and psumAtLevel for each level.
	// Uses the FIXED trimOff formula (/ 6*numBands) matching computeAllocation.
	// Also compares ">>2" formula (ours) vs ">>4" formula (libopus-like).
	{
		thresh := make([]int, numBands)
		cap2 := make([]int, numBands)
		for j := 0; j < numBands; j++ {
			N := int(EBands48000[j+1] - EBands48000[j])
			M := N << uint(lm)
			t1 := ch << 3
			t2 := 3 * ch * M / 2
			if t2 > t1 {
				thresh[j] = t2
			} else {
				thresh[j] = t1
			}
			capsIdx := (lm+1)*NumBands48000 + j
			if capsIdx >= 0 && capsIdx < len(CacheCaps50) {
				cap2[j] = ch * M * int(CacheCaps50[capsIdx]) >> 2
			} else {
				cap2[j] = ch * M * 255 >> 2
			}
		}
		// Formula ">>2": ch*M*alloc>>2 + trimOff  (our current formula)
		allocQ3v2 := func(j, k int) int {
			N := int(EBands48000[j+1] - EBands48000[j])
			M := N << uint(lm)
			trimOff := ch * M * (allocTrim - 5 - lm) * (numBands - 1 - j) / (6 * numBands)
			v := ch*M*int(BandAllocation[j][k])>>2 + trimOff
			if v < 0 {
				v = 0
			}
			return v
		}
		// Formula ">>4": (ch*N*alloc+8)>>4 - trimOff  (libopus-style, trim subtracted, N=M)
		allocQ3v4 := func(j, k int) int {
			N := int(EBands48000[j+1] - EBands48000[j])
			M := N << uint(lm)
			trimOff := ch * M * (allocTrim - 5 - lm) * (numBands - 1 - j) / (6 * numBands)
			raw := (ch * M * int(BandAllocation[j][k]) + 8) >> 4
			v := raw - trimOff
			if v < 0 {
				v = 0
			}
			return v
		}
		psumFn := func(allocFn func(j, k int) int, k int) int {
			psum := 0
			done := false
			for j := numBands - 1; j >= 0; j-- {
				tmp := allocFn(j, k)
				if tmp >= thresh[j] || done {
					done = true
					if tmp > cap2[j] {
						tmp = cap2[j]
					}
					if tmp < ch<<3 {
						tmp = ch << 3
					}
					psum += tmp
				} else if tmp >= ch<<3 {
					psum += ch << 3
				}
			}
			return psum
		}
		availQ3 := remaining * 8
		fmt.Printf("  psumAtLevel(>>2 vs >>4): availQ3=%d remaining=%d\n", availQ3, remaining)
		for k := 0; k <= 10; k++ {
			p2 := psumFn(allocQ3v2, k)
			p4 := psumFn(allocQ3v4, k)
			fmt.Printf("    k=%2d: >>2 psum=%5d fits=%v | >>4 psum=%5d fits=%v\n",
				k, p2, p2 <= availQ3, p4, p4 <= availQ3)
		}
		fmt.Printf("  Band allocQ3(>>2 vs >>4) and trimOff at each level k=5:\n")
		for j := 0; j < numBands; j++ {
			N := int(EBands48000[j+1] - EBands48000[j])
			M := N << uint(lm)
			trimOff := ch * M * (allocTrim - 5 - lm) * (numBands - 1 - j) / (6 * numBands)
			fmt.Printf("    band%2d(M=%3d thr=%3d cap=%4d): >>2=%3d >>4=%3d trimOff=%d alloc=%d\n",
				j, M, thresh[j], cap2[j], allocQ3v2(j, 5), allocQ3v4(j, 5), trimOff, int(BandAllocation[j][5]))
		}
	}

	// Step 9: fine energy (raw bits from END — iterate forward like libopus)
	for i := 0; i < numBands; i++ {
		fb := eBits[i]
		if fb <= 0 {
			continue
		}
		for c := 0; c < ch; c++ {
			_ = dec.DecodeBits(uint(fb))
		}
	}
	snap("fine_energy_fwd")

	// Step 10: anti-collapse bit
	if antiCollapseRsv > 0 && dec.Tell()+1 <= totalBits {
		dec.DecodeBitLogp(1)
	}
	snap("anti_collapse")

	// Step 11: spread
	if dec.Tell()+4 <= totalBits {
		_ = dec.DecodeIcdf(spreadIcdf[:], 5)
	}
	snap("spread")

	// Step 12: PVQ decode for each band — CWRS approach matching libopus decode_pulses.
	// libopus: decode_pulses → ec_dec_uint(icwrs(N,K)) → cwrsi(N,K,idx)
	for i := 0; i < numBands; i++ {
		k := pulses[i]
		N := int(EBands48000[i+1]-EBands48000[i]) << uint(lm)
		if k <= 0 || N <= 0 {
			continue
		}
		v := cwrsV(N, k)
		vCl := uint64(math.MaxUint32)
		if v < uint64(math.MaxUint32) {
			vCl = v
		}
		fmt.Printf("    PRE  band%2d tell=%d rng=0x%08x dif=0x%08x pos=%d endOffs=%d nend=%d\n",
			i, dec.Tell(), dec.GetRng(), dec.GetDif(), dec.GetPos(), dec.GetEndOffs(), dec.GetNendBits())
		if i == 14 {
			// Trace band14's DecodeUint directly
			vT := cwrsV(N, k)
			vTc := uint32(vT)
			if vT >= uint64(math.MaxUint32) {
				vTc = math.MaxUint32
			}
			fmt.Printf("    [band14 direct trace] calling DecodeUintTrace(%d)\n", vTc)
			_ = dec.DecodeUintTrace(vTc)
			fmt.Printf("    [band14 post-trace] tell=%d rng=0x%08x\n", dec.Tell(), dec.GetRng())
		} else {
			PVQDecode(dec, N, k)
		}
		fmt.Printf("    band%2d N=%3d K=%d V=%d tell=%d rng=0x%08x\n", i, N, k, vCl, dec.Tell(), dec.GetRng())
	}
	snap("after_pvq")

	// Final check
	gotFinal := dec.GetRng()
	fmt.Printf("\nFinal range after PVQ: got=0x%08x expected=0x%08x match=%v\n",
		gotFinal, expectedFinal, gotFinal == expectedFinal)

	// Also test via CELT decoder
	mode := NewModeEx(lmSizes[lmIdx], 48000, numBands, ch)
	_ = mode
	celtDec, _ := NewDecoderEx(lmSizes[lmIdx], 48000, numBands, ch)
	_, err = celtDec.Decode(frameData)
	if err != nil {
		t.Errorf("celtDec.Decode: %v", err)
	}
	fmt.Printf("CELT decoder final range: got=0x%08x expected=0x%08x match=%v\n",
		celtDec.LastFinalRange(), expectedFinal, celtDec.LastFinalRange() == expectedFinal)
}

func min5(a, b int) int {
	if a < b {
		return a
	}
	return b
}
