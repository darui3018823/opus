package celt

import "github.com/darui3018823/opus/internal/entcode"

// This file holds the encoder-side counterparts of the small CELT header coders
// in decoder.go (celtTFDecode, decodeDynalloc). They are faithful ports of the
// libopus encode paths (celt/celt_encoder.c tf_encode and the dynalloc loop in
// celt_encode_with_ec) and write the exact symbol sequence the decoder reads.

// tfEncode is the encoder counterpart of celtTFDecode. tfRes[] holds the desired
// per-band time-frequency resolution (0/1) on entry; on return it holds the
// resolved tf_res (after tf_select mapping), matching what the decoder produces.
func tfEncode(enc *entcode.Encoder, start, end int, isTransient bool, tfRes []int, lm, tfSelect, totalBits int) {
	isT := 0
	if isTransient {
		isT = 1
	}
	budget := totalBits
	tell := enc.ECTell()
	logp := 4
	if isTransient {
		logp = 2
	}
	tfSelectRsv := 0
	if lm > 0 && tell+logp+1 <= budget {
		tfSelectRsv = 1
		budget--
	}
	curr, tfChanged := 0, 0
	for i := start; i < end; i++ {
		if tell+logp <= budget {
			enc.EncodeBitLogp(tfRes[i]^curr != 0, uint(logp))
			tell = enc.ECTell()
			curr = tfRes[i]
			tfChanged |= curr
		} else {
			tfRes[i] = curr
		}
		if isTransient {
			logp = 4
		} else {
			logp = 5
		}
	}

	lmC := lm
	if lmC < 0 {
		lmC = 0
	}
	if lmC > 3 {
		lmC = 3
	}
	if tfSelectRsv != 0 &&
		tfSelectTable[lmC][4*isT+0+tfChanged] != tfSelectTable[lmC][4*isT+2+tfChanged] {
		enc.EncodeBitLogp(tfSelect != 0, 1)
	} else {
		tfSelect = 0
	}
	for i := start; i < end; i++ {
		tfRes[i] = tfSelectTable[lmC][4*isT+2*tfSelect+tfRes[i]]
	}
}

// dynallocEncode is the encoder counterpart of decodeDynalloc. offsets[] holds
// the desired per-band Q3 boost on entry; the matching flag bits are written and
// offsets[] is replaced with the actually-coded boost (clamped by cap/budget),
// mirroring decodeDynalloc exactly.
func dynallocEncode(enc *entcode.Encoder, offsets []int, numBands, start, end, lm, ch, totalBits int) {
	dynallocLogp := 6
	totalQ3 := totalBits << 3
	tell := enc.TellFrac()

	for j := start; j < end; j++ {
		N0 := int(EBands48000[j+1] - EBands48000[j])
		M := N0 << uint(lm)
		width := ch * M
		quanta := width << 3
		hi := width
		if hi < 48 {
			hi = 48
		}
		if quanta > hi {
			quanta = hi
		}
		capsIdx := NumBands48000*(2*lm+ch-1) + j
		capVal := 255
		if capsIdx >= 0 && capsIdx < len(CacheCaps50) {
			capVal = int(CacheCaps50[capsIdx])
		}
		capj := (capVal + 64) * width >> 2

		want := offsets[j]
		loopLogp := dynallocLogp
		boost := 0
		inner := 0
		for tell+(loopLogp<<3) < totalQ3 && boost < capj {
			flag := inner < want
			enc.EncodeBitLogp(flag, uint(loopLogp))
			tell = enc.TellFrac()
			if !flag {
				break
			}
			boost += quanta
			totalQ3 -= quanta
			loopLogp = 1
			inner++
		}
		offsets[j] = boost
		if boost > 0 {
			dynallocLogp--
			if dynallocLogp < 2 {
				dynallocLogp = 2
			}
		}
	}
}
