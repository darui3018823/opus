//go:build opusref

package opus

import (
	"errors"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGOExpertFrameDurationSemantics(t *testing.T) {
	const (
		rate      = 48000
		available = 5760
	)
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
	for _, duration := range durations {
		duration := duration
		t.Run(expertFrameDurationName(duration), func(t *testing.T) {
			goEnc, err := NewEncoder(rate, 1, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			refEnc, err := cgoref.NewEncoder(rate, 1, int(ApplicationAudio))
			if err != nil {
				t.Fatal(err)
			}
			defer refEnc.Close()

			if err := goEnc.SetExpertFrameDuration(duration); err != nil {
				t.Fatal(err)
			}
			if err := refEnc.SetExpertFrameDuration(int(duration)); err != nil {
				t.Fatal(err)
			}
			if got, err := refEnc.ExpertFrameDuration(); err != nil || got != int(duration) {
				t.Fatalf("libopus GET = %d, %v; want %d", got, err, duration)
			}

			goPacket, err := goEnc.EncodeFloat32(make([]float32, available), available)
			if err != nil {
				t.Fatal(err)
			}
			refPacket, err := refEnc.Encode(make([]float32, available), available)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := frameSizeForExpertDuration(duration, rate, 0)
			for name, packet := range map[string][]byte{"Go": goPacket, "libopus": refPacket} {
				got, err := PacketGetNumSamples(packet, rate)
				if err != nil || got != want {
					t.Fatalf("%s packet samples = %d, %v; want %d", name, got, err, want)
				}
			}
		})
	}
}

func TestCGOExpertFrameDurationRejectsShortAvailability(t *testing.T) {
	goEnc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	refEnc, err := cgoref.NewEncoder(48000, 1, int(ApplicationAudio))
	if err != nil {
		t.Fatal(err)
	}
	defer refEnc.Close()
	if err := goEnc.SetExpertFrameDuration(ExpertFrameDuration10ms); err != nil {
		t.Fatal(err)
	}
	if err := refEnc.SetExpertFrameDuration(int(ExpertFrameDuration10ms)); err != nil {
		t.Fatal(err)
	}
	if _, err := goEnc.EncodeFloat32(make([]float32, 240), 240); !errors.Is(err, ErrBadArg) {
		t.Fatalf("Go short availability error = %v, want ErrBadArg", err)
	}
	if _, err := refEnc.Encode(make([]float32, 240), 240); err == nil {
		t.Fatal("libopus accepted short availability")
	}
}

func expertFrameDurationName(duration ExpertFrameDuration) string {
	switch duration {
	case ExpertFrameDuration2_5ms:
		return "2.5ms"
	case ExpertFrameDuration5ms:
		return "5ms"
	case ExpertFrameDuration10ms:
		return "10ms"
	case ExpertFrameDuration20ms:
		return "20ms"
	case ExpertFrameDuration40ms:
		return "40ms"
	case ExpertFrameDuration60ms:
		return "60ms"
	case ExpertFrameDuration80ms:
		return "80ms"
	case ExpertFrameDuration100ms:
		return "100ms"
	case ExpertFrameDuration120ms:
		return "120ms"
	default:
		return "argument"
	}
}
