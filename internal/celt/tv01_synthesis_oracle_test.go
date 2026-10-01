package celt

import "testing"

func TestTV01SynthesisStagesAgainstLibopus(t *testing.T) {
	packet, _ := findPacket(t, "../../testdata/opus_newvectors/testvector01.bit", 0)
	frames := splitTV01CELTFrames(t, packet)
	if len(frames) != 3 {
		t.Fatalf("frame count=%d, want 3", len(frames))
	}

	decoder, err := NewDecoderEx(FrameSize20ms, 48000, 21, 2)
	if err != nil {
		t.Fatalf("NewDecoderEx: %v", err)
	}

	stages := map[string]int{
		"synthesis":  0,
		"postfilter": 1,
		"pcm":        2,
	}
	want := [3][3][2]uint64{
		{
			{0xfff939355819a693, 0x23f623a85c4cc43c},
			{0xfff939355819a693, 0x23f623a85c4cc43c},
			{0x06e05cab097c725b, 0x0702261def3c72fc},
		},
		{
			{0x85648628be2a674b, 0xd045414e91b81375},
			{0x85648628be2a674b, 0xd045414e91b81375},
			{0xcf598a6ab3a0e566, 0x1e550afe811f3282},
		},
		{
			{0x680b7238973d8a61, 0xf0d26bd4e2495b68},
			{0x680b7238973d8a61, 0xf0d26bd4e2495b68},
			{0xd02cd29517b67ed3, 0x3fe3c60b96568d72},
		},
	}
	var got [3][3][2]uint64
	frameIndex := 0
	decoder.synthesisStageHook = func(stage string, channel int, samples []float64) {
		stageIndex, ok := stages[stage]
		if !ok {
			t.Fatalf("unexpected synthesis stage %q", stage)
		}
		if stage == "pcm" {
			scaled := make([]float64, len(samples))
			for i, sample := range samples {
				scaled[i] = sample * celtFloatScale
			}
			got[frameIndex][stageIndex][channel] = hashLibopusFloatSlice(scaled)
			return
		}
		got[frameIndex][stageIndex][channel] = hashLibopusFloatSlice(samples)
	}

	for frame := range frames {
		frameIndex = frame
		if _, err := decoder.Decode(frames[frame]); err != nil {
			t.Fatalf("decode frame %d: %v", frame, err)
		}
	}
	decoder.synthesisStageHook = nil

	stageNames := [...]string{"synthesis", "postfilter", "pcm"}
	for frame := range frames {
		for stage, name := range stageNames {
			for channel := 0; channel < 2; channel++ {
				t.Logf("frame=%d stage=%s channel=%d got=%016x libopus=%016x",
					frame, name, channel, got[frame][stage][channel], want[frame][stage][channel])
				if got[frame][stage][channel] != want[frame][stage][channel] {
					t.Errorf("frame %d %s channel %d hash=%016x, want %016x",
						frame, name, channel, got[frame][stage][channel], want[frame][stage][channel])
				}
			}
		}
	}
}
