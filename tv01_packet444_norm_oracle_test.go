package opus

import "testing"

func TestTV01Packet444NormalizationAgainstLibopus(t *testing.T) {
	packets := readOpusDemoPackets(t, "testvector01.bit")
	decoder, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatal(err)
	}
	currentPacket := -1
	var got [2][]float64
	hook := func(stage string, channel int, coeffs, _ []float64) {
		if currentPacket == 444 && stage == "normalized" && got[channel] == nil {
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
	for packet := 0; packet <= 444; packet++ {
		currentPacket = packet
		if _, err := decoder.DecodeFloat(packets[packet].packet); err != nil {
			t.Fatal(err)
		}
	}

	want := [...]uint64{0xdcf9470416e28666, 0x9148e872858a1cc3}
	const normalizedLength = 200
	for channel := range got {
		if len(got[channel]) < normalizedLength {
			t.Fatalf("channel %d captured %d normalized coefficients, need %d",
				channel, len(got[channel]), normalizedLength)
		}
		if hash := hashFloat32Slice(got[channel][:normalizedLength]); hash != want[channel] {
			t.Errorf("channel=%d normalized hash=%016x, want=%016x",
				channel, hash, want[channel])
		}
	}
}
