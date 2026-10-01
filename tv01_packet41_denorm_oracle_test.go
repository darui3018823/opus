package opus

import "testing"

func TestTV01Packet41DenormalizationAgainstLibopus(t *testing.T) {
	packets := readOpusDemoPackets(t, "testvector01.bit")
	decoder, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatal(err)
	}
	currentPacket := -1
	var got [2][]float64
	hook := func(stage string, channel int, coeffs, _ []float64) {
		if currentPacket == 41 && stage == "denormalized" && got[channel] == nil {
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
	for packet := 0; packet <= 41; packet++ {
		currentPacket = packet
		if _, err := decoder.DecodeFloat(packets[packet].packet); err != nil {
			t.Fatal(err)
		}
	}

	want := [...]uint64{0xeda6c5425c568137, 0xf51846e4c04a3a94}
	for channel := range got {
		if got[channel] == nil {
			t.Fatalf("channel %d denormalized coefficients were not captured", channel)
		}
		if hash := hashFloat32Slice(got[channel]); hash != want[channel] {
			t.Errorf("channel=%d denormalized hash=%016x, want=%016x",
				channel, hash, want[channel])
		}
	}
}
