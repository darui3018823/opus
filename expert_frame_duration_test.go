package opus

import (
	"bytes"
	"errors"
	"testing"
)

func TestExpertFrameDurationAllRatesAndDurations(t *testing.T) {
	durations := []ExpertFrameDuration{
		ExpertFrameDuration2_5ms,
		ExpertFrameDuration5ms,
		ExpertFrameDuration10ms,
		ExpertFrameDuration20ms,
		ExpertFrameDuration40ms,
		ExpertFrameDuration60ms,
		ExpertFrameDuration80ms,
		ExpertFrameDuration100ms,
		ExpertFrameDuration120ms,
	}
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, duration := range durations {
			selected, ok := frameSizeForExpertDuration(duration, rate, 0)
			if !ok {
				t.Fatalf("duration %d was rejected", duration)
			}
			enc, err := NewEncoder(rate, 1, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetExpertFrameDuration(duration); err != nil {
				t.Fatal(err)
			}
			packet, err := enc.Encode(make([]int16, selected), selected)
			if err != nil {
				t.Fatalf("rate=%d duration=%d selected=%d: %v", rate, duration, selected, err)
			}
			got, err := PacketGetNumSamples(packet, rate)
			if err != nil || got != selected {
				t.Fatalf("rate=%d duration=%d packet samples = %d, %v; want %d", rate, duration, got, err, selected)
			}
		}
	}
}

func TestExpertFrameDurationAllPCMInputs(t *testing.T) {
	const (
		rate      = 48000
		available = 960
		selected  = 480
	)
	tests := []struct {
		name   string
		encode func(*Encoder) ([]byte, error)
	}{
		{"int16", func(e *Encoder) ([]byte, error) { return e.Encode(make([]int16, available), available) }},
		{"int24", func(e *Encoder) ([]byte, error) { return e.Encode24(make([]int32, available), available) }},
		{"float32", func(e *Encoder) ([]byte, error) { return e.EncodeFloat32(make([]float32, available), available) }},
		{"float64", func(e *Encoder) ([]byte, error) { return e.EncodeFloat(make([]float64, available), available) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(rate, 1, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.SetExpertFrameDuration(ExpertFrameDuration10ms); err != nil {
				t.Fatal(err)
			}
			packet, err := tc.encode(enc)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := PacketGetNumSamples(packet, rate); err != nil || got != selected {
				t.Fatalf("packet samples = %d, %v; want %d", got, err, selected)
			}
		})
	}
}

func TestExpertFrameDurationAvailabilityValidation(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration10ms); err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, 700), 700)
	if err != nil {
		t.Fatalf("arbitrary larger availability: %v", err)
	}
	if got, err := PacketGetNumSamples(packet, 48000); err != nil || got != 480 {
		t.Fatalf("packet samples = %d, %v; want 480", got, err)
	}
	if _, err := enc.Encode(make([]int16, 479), 479); !errors.Is(err, ErrBadArg) {
		t.Fatalf("short availability error = %v, want ErrBadArg", err)
	}
	if _, err := enc.Encode(make([]int16, 480), 960); !errors.Is(err, ErrBadArg) {
		t.Fatalf("short input buffer error = %v, want ErrBadArg", err)
	}
}

func TestExpertFrameDurationConfigurationAndReset(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArgument {
		t.Fatalf("default duration = %d", got)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration10ms); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration(6000)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("invalid setter error = %v", err)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDuration10ms {
		t.Fatalf("invalid setter changed duration to %d", got)
	}
	if err := enc.Reset(); err != nil {
		t.Fatal(err)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDuration10ms {
		t.Fatalf("Reset changed duration to %d", got)
	}
}

func TestExpertFrameDurationArgumentPreservesPacketBytes(t *testing.T) {
	plain, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := explicit.SetExpertFrameDuration(ExpertFrameDurationArgument); err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16((i*197)%20000 - 10000)
	}
	a, err := plain.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	b, err := explicit.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) || plain.FinalRange() != explicit.FinalRange() {
		t.Fatal("explicit Argument changed packet bytes or final range")
	}
}

func TestExpertFrameDurationTailResendContinuity(t *testing.T) {
	newEncoder := func() *Encoder {
		enc, err := NewEncoder(48000, 1, ApplicationAudio)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.SetExpertFrameDuration(ExpertFrameDuration10ms); err != nil {
			t.Fatal(err)
		}
		return enc
	}
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16((i*131)%24000 - 12000)
	}
	available := newEncoder()
	first, err := available.Encode(pcm, 960)
	if err != nil {
		t.Fatal(err)
	}
	second, err := available.Encode(pcm[480:], 480)
	if err != nil {
		t.Fatal(err)
	}
	control := newEncoder()
	wantFirst, err := control.Encode(pcm[:480], 480)
	if err != nil {
		t.Fatal(err)
	}
	wantSecond, err := control.Encode(pcm[480:], 480)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, wantFirst) || !bytes.Equal(second, wantSecond) || available.FinalRange() != control.FinalRange() {
		t.Fatal("resending the unconsumed tail changed encoder continuity")
	}
}

func TestExpertFrameDurationForcedMono(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetForceChannels(ChannelsMono); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration5ms); err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, 960*2), 960)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := PacketGetNumSamples(packet, 48000); err != nil || got != 240 {
		t.Fatalf("forced mono packet samples = %d, %v; want 240", got, err)
	}
	if channels, err := PacketGetNumChannels(packet); err != nil || channels != 1 {
		t.Fatalf("forced mono channels = %d, %v", channels, err)
	}
}
