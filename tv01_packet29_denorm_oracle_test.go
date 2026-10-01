package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal/celt"
)

func TestTV01Packet29DenormalizedBandsAgainstLibopus(t *testing.T) {
	packets := readOpusDemoPackets(t, "testvector01.bit")
	const targetPacket = 29
	if len(packets) <= targetPacket {
		t.Fatalf("tv01 has %d packets, need packet %d", len(packets), targetPacket)
	}

	decoder, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	currentPacket := -1
	var got [2][]float64
	hook := func(stage string, channel int, coeffs, _ []float64) {
		if currentPacket == targetPacket && stage == "denormalized" && got[channel] == nil {
			got[channel] = append([]float64(nil), coeffs...)
		}
	}
	for bandwidth := range decoder.celtDecoders {
		for lm := range decoder.celtDecoders[bandwidth] {
			for channel := range decoder.celtDecoders[bandwidth][lm] {
				decoder.celtDecoders[bandwidth][lm][channel].SetCoefficientStageHook(hook)
			}
		}
	}
	for packet := 0; packet <= targetPacket; packet++ {
		currentPacket = packet
		if _, err := decoder.DecodeFloat(packets[packet].packet); err != nil {
			t.Fatalf("decode packet %d: %v", packet, err)
		}
	}

	want := [2][celt.NumBands48000]uint64{
		{
			0xac6ee7280dfaf4d7, 0x6ff9aea4def91540, 0x8e2815ef71fc03a4,
			0xde268b438f5a94f5, 0x227c47e0b254bd03, 0x9ba646a5bbf8e1a1,
			0x450e8256b47774a5, 0xe0d6778a0333ad0a, 0xffebace8d00b4bab,
			0x84e8db8e380712ff, 0xad1a68a9f95f3575, 0x0bc7de22172b2069,
			0x01c7945276c2226b, 0x837ca045a52d3c4d, 0x1b622c70041cd9d4,
			0x53a25f514c28ae9f, 0xe6d462fe7d5c806a, 0xee960a49f2262432,
			0x2c56c6ef3f0ee119, 0x61718c1fba3f4d82, 0xc3d5fa3ce1455f45,
		},
		{
			0xa6bafadae16e275a, 0x034ae93189fcec98, 0xd02d3ee920790872,
			0x899ae29ca9a0e8c5, 0x1e07c5bd08ff74f6, 0xd2f2705aa40bc92a,
			0xc9ff7c87c95d8142, 0xd973a21479ffe54d, 0xebf6160456a87895,
			0x5c1fa6d17a03a3fb, 0x167dad3481280695, 0x1628bec63d954155,
			0xf969e9fcdd671636, 0xf9a29e3c2948a876, 0x00157594652a3ed2,
			0x3096427ce8df81b3, 0x580e34b0f9e00c5f, 0x36350e5c3d5d4b85,
			0x8d96e880565969a2, 0xf367480c36114b6c, 0x954dfa78968e366e,
		},
	}
	const m = 8
	for channel := range got {
		if got[channel] == nil {
			t.Fatalf("channel %d denormalized coefficients were not captured", channel)
		}
		for band := 0; band < celt.NumBands48000; band++ {
			start := m * int(celt.EBands48000[band])
			end := m * int(celt.EBands48000[band+1])
			hash := hashFloat32Slice(got[channel][start:end])
			if hash != want[channel][band] {
				t.Errorf("channel=%d band=%d hash=%016x, want=%016x",
					channel, band, hash, want[channel][band])
			}
		}
	}
}
