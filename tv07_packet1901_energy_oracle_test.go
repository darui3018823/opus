package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal/celt"
)

func TestTV07Packet1901FineEnergyAgainstLibopus(t *testing.T) {
	packets := readOpusDemoPackets(t, "testvector07.bit")
	decoder, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatal(err)
	}
	currentPacket := -1
	var got []float64
	hook := func(stage string, channel int, _, energies []float64) {
		if currentPacket == 1901 && stage == "normalized" && channel == 0 && got == nil {
			got = append([]float64(nil), energies...)
		}
	}
	forEachCELTDecoder(decoder, func(c *celt.Decoder) {
		c.SetCoefficientStageHook(hook)
	})
	for packet := 0; packet <= 1901; packet++ {
		currentPacket = packet
		if _, err := decoder.DecodeFloat(packets[packet].packet); err != nil {
			t.Fatal(err)
		}
	}

	const energyCount = 13
	if len(got) < energyCount {
		t.Fatalf("captured %d fine-corrected energies, need %d", len(got), energyCount)
	}
	const want uint64 = 0xe04975f51c42568b
	if hash := hashFloat32Slice(got[:energyCount]); hash != want {
		t.Fatalf("fine-corrected energy hash=%016x, want=%016x", hash, want)
	}
}
