//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGOProjectionMatricesMatchLibopus(t *testing.T) {
	for _, channels := range []int{4, 6, 9, 11, 16, 18, 25, 27, 36, 38} {
		goEnc, err := NewProjectionEncoder(48000, channels, MappingFamilyProjection, ApplicationAudio)
		if err != nil {
			t.Fatalf("%d channels Go encoder: %v", channels, err)
		}
		refEnc, err := cgoref.NewProjectionEncoder(48000, channels, MappingFamilyProjection, ApplicationAudio)
		if err != nil {
			t.Fatalf("%d channels libopus encoder: %v", channels, err)
		}
		matrix, gain, err := refEnc.DemixingMatrix()
		refEnc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if gain != goEnc.DemixingMatrixGain() {
			t.Fatalf("%d channels gain=%d, want %d", channels, goEnc.DemixingMatrixGain(), gain)
		}
		got := goEnc.DemixingMatrixBytes()
		if len(got) != len(matrix) {
			t.Fatalf("%d channels matrix bytes=%d, want %d", channels, len(got), len(matrix))
		}
		for i := range got {
			if got[i] != matrix[i] {
				t.Fatalf("%d channels matrix byte %d=%d, want %d", channels, i, got[i], matrix[i])
			}
		}
	}
}

func TestCGOProjectionFamily3Interoperability(t *testing.T) {
	const (
		rate      = 48000
		channels  = 4
		frameSize = 960
	)
	pcm := projectionFixture32(frameSize, channels, rate)

	goEnc, err := NewProjectionEncoder(rate, channels, MappingFamilyProjection, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	goEnc.SetVBR(true)
	if err := goEnc.SetBitrate(256000); err != nil {
		t.Fatal(err)
	}
	goPacket, err := goEnc.EncodeFloat32(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	refDec, err := cgoref.NewProjectionDecoder(rate, channels, goEnc.Streams(), goEnc.CoupledStreams(), goEnc.DemixingMatrixBytes())
	if err != nil {
		t.Fatal(err)
	}
	refOut, err := refDec.DecodeFloat(goPacket, frameSize)
	refDec.Close()
	if err != nil {
		t.Fatalf("libopus rejected Go projection packet: %v", err)
	}
	if len(refOut) != len(pcm) {
		t.Fatalf("libopus decoded %d samples, want %d", len(refOut), len(pcm))
	}

	refEnc, err := cgoref.NewProjectionEncoder(rate, channels, MappingFamilyProjection, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	defer refEnc.Close()
	if err := refEnc.SetVBR(true); err != nil {
		t.Fatal(err)
	}
	if err := refEnc.SetBitrate(256000); err != nil {
		t.Fatal(err)
	}
	refPacket, err := refEnc.EncodeFloat(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	matrix, _, err := refEnc.DemixingMatrix()
	if err != nil {
		t.Fatal(err)
	}
	goDec, err := NewProjectionDecoder(rate, channels, refEnc.Streams(), refEnc.CoupledStreams(), matrix)
	if err != nil {
		t.Fatal(err)
	}
	goOut, err := goDec.DecodeFloat32(refPacket)
	if err != nil {
		t.Fatalf("Go rejected libopus projection packet: %v", err)
	}
	if len(goOut) != len(pcm) {
		t.Fatalf("Go decoded %d samples, want %d", len(goOut), len(pcm))
	}
}

func TestCGOProjectionFamily2Interoperability(t *testing.T) {
	const (
		rate      = 48000
		channels  = 6
		frameSize = 960
	)
	pcm := projectionFixture32(frameSize, channels, rate)
	goEnc, err := NewProjectionEncoder(rate, channels, MappingFamilyAmbisonics, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	goPacket, err := goEnc.EncodeFloat32(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	refDec, err := cgoref.NewMultistreamDecoder(rate, channels, goEnc.Streams(), goEnc.CoupledStreams(), goEnc.Mapping())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refDec.DecodeFloat(goPacket, frameSize); err != nil {
		t.Fatalf("libopus rejected Go family-2 packet: %v", err)
	}
	refDec.Close()

	refEnc, err := cgoref.NewAmbisonicsMultistreamEncoder(rate, channels, MappingFamilyAmbisonics, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	defer refEnc.Close()
	if err := refEnc.SetBitrate(256000); err != nil {
		t.Fatal(err)
	}
	refPacket, err := refEnc.EncodeFloat(pcm, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	goDec, err := NewAmbisonicsDecoder(rate, channels, MappingFamilyAmbisonics,
		refEnc.Streams(), refEnc.CoupledStreams(), refEnc.Mapping(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := goDec.DecodeFloat32(refPacket); err != nil {
		t.Fatalf("Go rejected libopus family-2 packet: %v", err)
	}
}

func projectionFixture32(frameSize, channels, rate int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for channel := 0; channel < channels; channel++ {
			pcm[i*channels+channel] = float32(0.16 * math.Sin(2*math.Pi*float64(191+61*channel)*float64(i)/float64(rate)))
		}
	}
	return pcm
}
