package celt

import (
	"math"
	"os"
	"testing"
)

func hashLibopusFloatSlice(values []float64) uint64 {
	hash := uint64(14695981039346656037)
	for _, value := range values {
		bits := math.Float32bits(float32(value))
		for shift := uint(0); shift < 32; shift += 8 {
			hash ^= uint64(byte(bits >> shift))
			hash *= 1099511628211
		}
	}
	return hash
}

func splitTV01CELTFrames(t *testing.T, packet []byte) [][]byte {
	t.Helper()
	if len(packet) < 2 || packet[0]&3 != 3 {
		t.Fatalf("expected code-3 packet")
	}
	control := packet[1]
	count := int(control & 0x3f)
	cursor := 2
	padding := 0
	if control&0x40 != 0 {
		for {
			if cursor >= len(packet) {
				t.Fatal("truncated packet padding")
			}
			value := int(packet[cursor])
			cursor++
			if value == 255 {
				padding += 254
				continue
			}
			padding += value
			break
		}
	}
	end := len(packet) - padding
	sizes := make([]int, count)
	if control&0x80 != 0 {
		total := 0
		for i := 0; i < count-1; i++ {
			if cursor >= end {
				t.Fatal("truncated VBR size")
			}
			first := int(packet[cursor])
			cursor++
			sizes[i] = first
			if first >= 252 {
				if cursor >= end {
					t.Fatal("truncated extended VBR size")
				}
				sizes[i] += 4 * int(packet[cursor])
				cursor++
			}
			total += sizes[i]
		}
		sizes[count-1] = end - cursor - total
	} else {
		if (end-cursor)%count != 0 {
			t.Fatal("invalid CBR frame sizes")
		}
		for i := range sizes {
			sizes[i] = (end - cursor) / count
		}
	}
	frames := make([][]byte, count)
	for i, size := range sizes {
		if size < 0 || cursor+size > end {
			t.Fatalf("invalid frame %d size %d", i, size)
		}
		frames[i] = packet[cursor : cursor+size]
		cursor += size
	}
	return frames
}

func TestTV01Frame1DenormalizedBandsAgainstLibopus(t *testing.T) {
	packet, _ := findPacket(t, "../../testdata/opus_newvectors/testvector01.bit", 0)
	frames := splitTV01CELTFrames(t, packet)
	if len(frames) != 3 {
		t.Fatalf("frame count=%d, want 3", len(frames))
	}
	decoder, err := NewDecoderEx(FrameSize20ms, 48000, 21, 2)
	if err != nil {
		t.Fatalf("NewDecoderEx: %v", err)
	}
	if _, err := decoder.Decode(frames[0]); err != nil {
		t.Fatalf("decode frame 0: %v", err)
	}
	var normalized [2][]float64
	decoder.normalizedCoeffHook = func(channel int, coeffs []float64) {
		normalized[channel] = append([]float64(nil), coeffs...)
	}
	if _, err := decoder.Decode(frames[1]); err != nil {
		t.Fatalf("decode frame 1: %v", err)
	}
	decoder.normalizedCoeffHook = nil

	wantNormalized := [...][21]uint64{
		{
			0x222e26997e31c544, 0xaee90df0c867d751, 0x48e98a8fe9872d68,
			0x4a8bd9da39b054c0, 0x2c35b2ec0b9e2b8b, 0xc69f957c77eee73e,
			0xdbe607987039ee1a, 0x4bc6f93df863bbde, 0x11ebbe0e95f081d5,
			0x234072859c42a192, 0xad205393e95f8425, 0x95faf45e2e050eb8,
			0xd38ba6e760abbf73, 0x37466f6c1d55200a, 0xc42d3cabb05f9809,
			0x8c3b951d000db898, 0x75f3c72f1d9ed360, 0x0bc4791c2a9362a6,
			0x7908ff7d017d02a3, 0x76230d74486551d5, 0xf3e1f50e61a21781,
		},
		{
			0xb0235df99347ef62, 0x3ba255553bac1de9, 0x6bc0a9f42eace63c,
			0x796257076c3d7ff0, 0xf8dd983e80adfdab, 0x0e1a39bc0ca28ab6,
			0xf5f3cb6d4626e007, 0x329bb9184984228a, 0xad9323fd07dc9a65,
			0x61114e84a67445b0, 0x229eb2800a652e8e, 0x4149dad55c103e67,
			0xa4b711d7514c1838, 0x242198845883e842, 0xbf448516014b0777,
			0x0163513ecf107ba0, 0x2fdfb2d10158dbb0, 0xa9eb6f5818fa5b52,
			0x7908ff7d017d02a3, 0xab21540e1c6c51d5, 0xf3e1f50e61a21781,
		},
	}
	wantEnergyBits := [...][21]uint32{
		{
			0xc1670000, 0xc160ff80, 0xc15f9900, 0xc14f9900, 0xc14acc40,
			0xc149ff80, 0xc145ff80, 0xc141ff80, 0xc145ff80, 0xc145ff80,
			0xc145ff80, 0xc149ff80, 0xc14cff80, 0xc139ff80, 0xc12d32c0,
			0xc1236600, 0xc11ae630, 0xc10c1960, 0xc1047fd0, 0xc0ab6600,
			0xbfb1fe80,
		},
		{
			0xc1630000, 0xc162ff80, 0xc1579900, 0xc14ecc40, 0xc146cc40,
			0xc14dff80, 0xc14dff80, 0xc145ff80, 0xc145ff80, 0xc145ff80,
			0xc149ff80, 0xc159ff80, 0xc14dcc40, 0xc13acc40, 0xc12932c0,
			0xc1216600, 0xc117e630, 0xc1101960, 0xc1064c90, 0xc0ab6600,
			0xbf9dfe80,
		},
	}

	wantFull := [...]uint64{0x42fcf07e86b2407a, 0x4a340d4e3fbcb202}
	wantBands := [...][21]uint64{
		{
			0x1da8e9b9dc1446f4, 0xcb1afc3da76779b5, 0x0ea04d6a847323f3,
			0x4d40346e5ff9ce41, 0x28f4d9d4d235ceaf, 0xba68e3e8a7bf586a,
			0x896771e252d08ec9, 0x709c9671cedd95ea, 0xf03cb31a21f668a5,
			0xe16e206a80246362, 0xc926209c3ae17425, 0xaf5a0ca2977ff7a3,
			0x28aad7b22951901e, 0xd75b419646a53a20, 0xbc51b6ca2802b79a,
			0x2a8cb1c3def1e8fe, 0x9491420edcd28eb5, 0xf6403dd9045884af,
			0x3e41b4098ac87895, 0xb4eb67ce52abb87b, 0x1c03f51af55fc168,
		},
		{
			0x780c96793abea3f9, 0x32ba11a806e9408c, 0x394c3149f8d6a689,
			0x99644da90d3a5b00, 0x88f6a9318c423464, 0x87cb9c1974999c2b,
			0x3e59677af5f4fdb2, 0x58466657e8550146, 0xd71b5b7ab80389a5,
			0xa652b78d1d1e259d, 0xc52044b63dd461c3, 0x5f798b17fabd8c20,
			0x22f3769d5ee168e3, 0xd5ab8e4c1f75e900, 0x023a7c2fde682977,
			0x49c9101e8443420e, 0x48ba9fba8745db37, 0x2c75abe191360b05,
			0xb561659e95dc1837, 0x2c603203f033ae7b, 0x47106173d3222422,
		},
	}

	for ch := 0; ch < 2; ch++ {
		coeffs := decoder.bandProcs[ch].AssembleMDCT()
		if len(normalized[ch]) != len(coeffs) {
			t.Fatalf("channel %d normalized coefficient count=%d, want %d", ch, len(normalized[ch]), len(coeffs))
		}
		gotFull := hashLibopusFloatSlice(coeffs)
		matchingBands := 0
		firstMismatch := -1
		matchingNormalized := 0
		firstNormalizedMismatch := -1
		var normalizedMismatches []int
		matchingEnergy := 0
		firstEnergyMismatch := -1
		for band, state := range decoder.bandProcs[ch].bands {
			got := hashLibopusFloatSlice(coeffs[state.Start : state.Start+state.Size])
			if got == wantBands[ch][band] {
				matchingBands++
			} else if firstMismatch < 0 {
				firstMismatch = band
			}
			normalizedHash := hashLibopusFloatSlice(normalized[ch][state.Start : state.Start+state.Size])
			if normalizedHash == wantNormalized[ch][band] {
				matchingNormalized++
			} else {
				if firstNormalizedMismatch < 0 {
					firstNormalizedMismatch = band
				}
				normalizedMismatches = append(normalizedMismatches, band)
				if os.Getenv("OPUS_GO_COEFFS") != "" {
					for index, value := range normalized[ch][state.Start : state.Start+state.Size] {
						t.Logf("[GO_NORM_COEFF] ch=%d band=%d index=%d value=%.9g bits=%08x",
							ch, band, index, value, math.Float32bits(float32(value)))
					}
				}
			}
			energyBits := math.Float32bits(float32(decoder.prevEnergies[ch*21+band]))
			if energyBits == wantEnergyBits[ch][band] {
				matchingEnergy++
			} else if firstEnergyMismatch < 0 {
				firstEnergyMismatch = band
			}
		}
		t.Logf("channel=%d full=%016x libopus=%016x denorm=%d/21 firstDenormMismatch=%d normalized=%d/21 firstNormalizedMismatch=%d energy=%d/21 firstEnergyMismatch=%d",
			ch, gotFull, wantFull[ch], matchingBands, firstMismatch,
			matchingNormalized, firstNormalizedMismatch, matchingEnergy, firstEnergyMismatch)
		t.Logf("channel=%d normalizedMismatchBands=%v", ch, normalizedMismatches)
		if matchingEnergy != 21 {
			t.Errorf("channel %d energy matches=%d/21, first mismatch=%d", ch, matchingEnergy, firstEnergyMismatch)
		}
		minimumNormalized := [...]int{18, 17}
		if matchingNormalized < minimumNormalized[ch] {
			t.Errorf("channel %d normalized matches=%d/21, want at least %d", ch, matchingNormalized, minimumNormalized[ch])
		}
		minimumDenormalized := [...]int{9, 8}
		if matchingBands < minimumDenormalized[ch] {
			t.Errorf("channel %d denormalized matches=%d/21, want at least %d", ch, matchingBands, minimumDenormalized[ch])
		}
	}
}
