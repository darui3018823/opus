package opus

import "testing"

func TestTV01Packet31PostfilterAgainstLibopus(t *testing.T) {
	packets := readOpusDemoPackets(t, "testvector01.bit")
	const targetPacket = 31
	if len(packets) <= targetPacket {
		t.Fatalf("tv01 has %d packets, need packet %d", len(packets), targetPacket)
	}

	decoder, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	stages := map[string]int{"synthesis": 0, "postfilter": 1, "pcm": 2}
	var got [4][3][2]uint64
	var counts [3][2]int
	currentPacket := -1
	hook := func(stage string, channel int, samples []float64) {
		if currentPacket != targetPacket {
			return
		}
		stageIndex, ok := stages[stage]
		if !ok {
			t.Fatalf("unexpected synthesis stage %q", stage)
		}
		frame := counts[stageIndex][channel]
		if frame >= len(got) {
			t.Fatalf("too many %s callbacks for channel %d", stage, channel)
		}
		values := samples
		if stage == "pcm" {
			values = make([]float64, len(samples))
			for i, sample := range samples {
				values[i] = sample * (1.0 / 32768.0)
			}
		}
		got[frame][stageIndex][channel] = hashFloat32Slice(values)
		counts[stageIndex][channel]++
	}
	for bandwidth := range decoder.celtDecoders {
		for lm := range decoder.celtDecoders[bandwidth] {
			for channel := range decoder.celtDecoders[bandwidth][lm] {
				decoder.celtDecoders[bandwidth][lm][channel].SetSynthesisStageHook(hook)
			}
		}
	}
	for packet := 0; packet <= targetPacket; packet++ {
		currentPacket = packet
		if _, err := decoder.DecodeFloat(packets[packet].packet); err != nil {
			t.Fatalf("decode packet %d: %v", packet, err)
		}
	}
	for stage := range counts {
		for channel := 0; channel < 2; channel++ {
			if counts[stage][channel] != len(got) {
				t.Fatalf("stage %d channel %d callback count=%d, want %d",
					stage, channel, counts[stage][channel], len(got))
			}
		}
	}

	want := [3][2]uint64{
		{0x65c52ca670b94bab, 0x4c99331e8dc37383},
		{0x97c9b154480b6563, 0x67a4133601ecb7c0},
		{0xb7f656ea90a04cda, 0xdea5bc830c4e471d},
	}
	const targetFrame = 2
	stageNames := [...]string{"synthesis", "postfilter", "pcm"}
	for stage, name := range stageNames {
		for channel := 0; channel < 2; channel++ {
			if got[targetFrame][stage][channel] != want[stage][channel] {
				t.Errorf("packet=%d frame=%d stage=%s channel=%d hash=%016x, want=%016x",
					targetPacket, targetFrame, name, channel,
					got[targetFrame][stage][channel], want[stage][channel])
			}
		}
	}
}
