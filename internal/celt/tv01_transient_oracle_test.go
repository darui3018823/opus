package celt

import (
	"math"
	"testing"
)

func TestTV01Frame0TransientBandsAgainstLibopus(t *testing.T) {
	packet, _ := findPacket(t, "../../testdata/opus_newvectors/testvector01.bit", 0)
	frames := splitTV01CELTFrames(t, packet)
	if len(frames) != 3 {
		t.Fatalf("frame count=%d, want 3", len(frames))
	}
	decoder, err := NewDecoderEx(FrameSize20ms, 48000, 21, 2)
	if err != nil {
		t.Fatalf("NewDecoderEx: %v", err)
	}
	var normalized [2][]float64
	decoder.normalizedCoeffHook = func(channel int, coeffs []float64) {
		normalized[channel] = append([]float64(nil), coeffs...)
	}
	if _, err := decoder.Decode(frames[0]); err != nil {
		t.Fatalf("decode frame 0: %v", err)
	}
	decoder.normalizedCoeffHook = nil

	wantNormalized := [...][21]uint64{
		{
			0x169060c50c575e36, 0x77386ac59020cd88, 0x9e7f5119d3fd9ad4,
			0xd2d6be66b78566cd, 0x1b8e7c33066cb72a, 0xce425e9243b25f75,
			0x40402b355069afc6, 0x8a677d8e7e53c6d2, 0x70a650fef680bd8e,
			0x223825e00e2091b0, 0xf69501f4efc7704a, 0x037f4ddac8847d2e,
			0xc1b46dda99a1e0a0, 0xeb2104dbcf45d4eb, 0xa2ba015cb10bb7fd,
			0xf2fc1aca45edc6d5, 0x47eaa35fcf86e079, 0x4ab4c6c44657009c,
			0x4a26420b9bbb4b6e, 0x6cd65f9c2ba0000e, 0xe403edc109f0b232,
		},
		{
			0x9082e7d194610cfe, 0x93eb10ce8fde2be3, 0xcbb8502979a428e8,
			0x61acf80df8821892, 0x11057d1d538694ca, 0xa9a7cbad24c0829d,
			0x93282422a4309465, 0x6c97432066e6533a, 0xdc940c031777242b,
			0x4a630c281e10530d, 0x9a8cbccc9e7788dd, 0x2cb28c5b3fa21015,
			0x9a3d18659515b9cd, 0x95d31b6106e9da91, 0xb015b01c0f04ba48,
			0xe3c222688dc6e4e5, 0x1415f28b593aff07, 0xce118dff5165b34b,
			0xae375e7ff86247c6, 0xeadf851d6c2bae82, 0xc4e731f7f65646aa,
		},
	}
	wantEnergyBits := [...][21]uint32{
		{
			0xc1618000, 0xc14966c0, 0xc14f0060, 0xc1470060, 0xc13b0060,
			0xc13f66c0, 0xc13b66c0, 0xc12966c0, 0xc12bcd20, 0xc12fcd20,
			0xc139cd20, 0xc12f66c0, 0xc137cd20, 0xc127cd20, 0xc1163380,
			0xc11499e0, 0xc10899e0, 0xc0f60080, 0xc0e2cd40, 0xc07f3400,
			0xbe900400,
		},
		{
			0xc1458000, 0xc15e3380, 0xc1569a00, 0xc13e9a00, 0xc14b66c0,
			0xc1350060, 0xc12d66c0, 0xc125cd20, 0xc12c3380, 0xc12bcd20,
			0xc13fcd20, 0xc13d66c0, 0xc14166c0, 0xc12766c0, 0xc11bcd20,
			0xc1123380, 0xc10299e0, 0xc0f60080, 0xc0decd40, 0xc06f3400,
			0x3cffc000,
		},
	}
	wantDenormalized := [...][21]uint64{
		{
			0x9d2ae5061bdce0f2, 0x44abe4782ed5c83c, 0xa4821df1ed031089,
			0x734669332fbff21d, 0x4533d5ab803f87ed, 0xaf0e034d94bf7dc2,
			0x1cfeb6ce85e96bd8, 0x97b34cbf623fb044, 0xf5320b43cc658c84,
			0xd188007f84764599, 0x77441b0a99b9f742, 0x3af2261e9b70d967,
			0xb3629a65d0fafa9a, 0x16e80bcf86230d39, 0x4dcc8d35d6c04fa3,
			0xfd10bf309d181d15, 0x2551af0b00c4eb29, 0x3bd8eda7042c06e4,
			0x0648ce777ea3bf3f, 0x33b07f528fa04c06, 0x4beed54e5814dcd0,
		},
		{
			0x6b00c4100acacdc0, 0x80d44e7583e6ffaf, 0x8a0b41088e5fe099,
			0x3bc31e8434460982, 0x69ab18c28c6ca912, 0x6ffc4a0465cf82e8,
			0xb2d7ffa09666a3d6, 0x42377b5aa68b5278, 0x8faa9a0e511d9483,
			0xb251269c2e272cc6, 0xc1c630f13a5a2f73, 0xa113192e5a076fb3,
			0x03c1c6ad0ba31ce5, 0x7c75dc3d3921b21d, 0xc48250bcafc7221d,
			0x47cc6cd354ccd54e, 0x4411880383b41f19, 0xfb0b1a2ea8d1719d,
			0x381cd2edebb2e7ee, 0x8fa25928cfcaf690, 0xd56573be2f581fc1,
		},
	}
	wantFull := [...]uint64{0x16e0bca83a2b2010, 0x95624a12cc25a0b1}

	for ch := 0; ch < 2; ch++ {
		coeffs := decoder.bandProcs[ch].AssembleMDCT()
		if len(normalized[ch]) != len(coeffs) {
			t.Fatalf("channel %d normalized coefficient count=%d, want %d", ch, len(normalized[ch]), len(coeffs))
		}
		normalizedMatches := 0
		energyMatches := 0
		denormalizedMatches := 0
		var normalizedMismatches []int
		var denormalizedMismatches []int
		firstNormalizedMismatch := -1
		firstEnergyMismatch := -1
		firstDenormalizedMismatch := -1
		for band, state := range decoder.bandProcs[ch].bands {
			start, end := state.Start, state.Start+state.Size
			if hashLibopusFloatSlice(normalized[ch][start:end]) == wantNormalized[ch][band] {
				normalizedMatches++
			} else if firstNormalizedMismatch < 0 {
				firstNormalizedMismatch = band
				normalizedMismatches = append(normalizedMismatches, band)
			} else {
				normalizedMismatches = append(normalizedMismatches, band)
			}
			if math.Float32bits(float32(decoder.prevEnergies[ch*21+band])) == wantEnergyBits[ch][band] {
				energyMatches++
			} else if firstEnergyMismatch < 0 {
				firstEnergyMismatch = band
			}
			if hashLibopusFloatSlice(coeffs[start:end]) == wantDenormalized[ch][band] {
				denormalizedMatches++
			} else if firstDenormalizedMismatch < 0 {
				firstDenormalizedMismatch = band
				denormalizedMismatches = append(denormalizedMismatches, band)
			} else {
				denormalizedMismatches = append(denormalizedMismatches, band)
			}
		}
		gotFull := hashLibopusFloatSlice(coeffs)
		t.Logf("channel=%d normalized=%d/21 first=%d energy=%d/21 first=%d denormalized=%d/21 first=%d full=%016x libopus=%016x",
			ch, normalizedMatches, firstNormalizedMismatch, energyMatches, firstEnergyMismatch,
			denormalizedMatches, firstDenormalizedMismatch, gotFull, wantFull[ch])
		t.Logf("channel=%d normalizedMismatchBands=%v denormalizedMismatchBands=%v",
			ch, normalizedMismatches, denormalizedMismatches)
		minimumNormalized := [...]int{12, 17}
		minimumDenormalized := [...]int{14, 18}
		if normalizedMatches < minimumNormalized[ch] {
			t.Errorf("channel %d normalized matches=%d/21, want at least %d", ch, normalizedMatches, minimumNormalized[ch])
		}
		if energyMatches != 21 {
			t.Errorf("channel %d energy matches=%d/21, first mismatch=%d", ch, energyMatches, firstEnergyMismatch)
		}
		if denormalizedMatches < minimumDenormalized[ch] {
			t.Errorf("channel %d denormalized matches=%d/21, want at least %d", ch, denormalizedMatches, minimumDenormalized[ch])
		}
	}
}
