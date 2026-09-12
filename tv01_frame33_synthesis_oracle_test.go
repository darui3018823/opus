package opus

import (
	"math"
	"testing"
)

func TestTV01Packet33SynthesisStagesAgainstLibopus(t *testing.T) {
	packets := readOpusDemoPackets(t, "testvector01.bit")
	const targetPacket = 33
	if len(packets) <= targetPacket {
		t.Fatalf("tv01 has %d packets, need packet %d", len(packets), targetPacket)
	}

	decoder, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	stages := map[string]int{"synthesis": 0, "postfilter": 1, "pcm": 2}
	var got [5][3][2]uint64
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
		if stage == "pcm" {
			scaled := make([]float64, len(samples))
			for i, sample := range samples {
				scaled[i] = sample * (1.0 / 32768.0)
			}
			got[frame][stageIndex][channel] = hashFloat32Slice(scaled)
		} else {
			got[frame][stageIndex][channel] = hashFloat32Slice(samples)
		}
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
		{0x2dde9cdaf162bbe8, 0x3e7baf6fc12ba9e1},
		{0x2dde9cdaf162bbe8, 0x3e7baf6fc12ba9e1},
		{0xa3ca0fa4120181e9, 0x007f0611ed2b19cb},
	}
	const targetFrame = 3
	stageNames := [...]string{"synthesis", "postfilter", "pcm"}
	for stage, name := range stageNames {
		for channel := 0; channel < 2; channel++ {
			t.Logf("packet=%d frame=%d stage=%s channel=%d got=%016x libopus=%016x",
				targetPacket, targetFrame, name, channel,
				got[targetFrame][stage][channel], want[stage][channel])
			if got[targetFrame][stage][channel] != want[stage][channel] {
				t.Errorf("packet %d frame %d %s channel %d hash=%016x, want %016x",
					targetPacket, targetFrame, name, channel,
					got[targetFrame][stage][channel], want[stage][channel])
			}
		}
	}
}

func hashFloat32Slice(values []float64) uint64 {
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
