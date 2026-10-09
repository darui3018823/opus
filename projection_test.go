package opus

import (
	"errors"
	"math"
	"testing"
)

func TestAmbisonicsLayouts(t *testing.T) {
	family2, err := NewProjectionEncoder(48000, 6, MappingFamilyAmbisonics, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if family2.Streams() != 5 || family2.CoupledStreams() != 1 {
		t.Fatalf("family 2 streams=%d coupled=%d", family2.Streams(), family2.CoupledStreams())
	}
	wantMapping := []byte{2, 3, 4, 5, 0, 1}
	for i, got := range family2.Mapping() {
		if got != wantMapping[i] {
			t.Fatalf("family 2 mapping[%d]=%d, want %d", i, got, wantMapping[i])
		}
	}
	if family2.DemixingMatrix() != nil {
		t.Fatal("family 2 unexpectedly has a demixing matrix")
	}

	family3, err := NewProjectionEncoder(48000, 6, MappingFamilyProjection, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if family3.Streams() != 3 || family3.CoupledStreams() != 3 {
		t.Fatalf("family 3 streams=%d coupled=%d", family3.Streams(), family3.CoupledStreams())
	}
	if family3.Mapping() != nil {
		t.Fatal("family 3 unexpectedly exposes a channel mapping")
	}
	if got := len(family3.DemixingMatrixBytes()); got != 6*6*2 {
		t.Fatalf("family 3 matrix size=%d, want %d", got, 6*6*2)
	}
}

func TestProjectionRoundTripFamily2And3(t *testing.T) {
	for _, family := range []int{MappingFamilyAmbisonics, MappingFamilyProjection} {
		t.Run(map[int]string{MappingFamilyAmbisonics: "family2", MappingFamilyProjection: "family3"}[family], func(t *testing.T) {
			const (
				rate      = 48000
				channels  = 4
				frameSize = 960
			)
			enc, err := NewAmbisonicsEncoder(rate, channels, family, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			enc.SetVBR(true)
			if err := enc.SetBitrate(256000); err != nil {
				t.Fatal(err)
			}
			pcm := projectionFixture(frameSize, channels, rate)
			packet, err := enc.EncodeFloat(pcm, frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if _, duration, err := splitMultistreamPackets(packet, enc.Streams(), rate); err != nil || duration != frameSize {
				t.Fatalf("multistream packet split duration=%d error=%v", duration, err)
			}

			dec, err := NewAmbisonicsDecoder(rate, channels, family, enc.Streams(), enc.CoupledStreams(), enc.Mapping(), enc.DemixingMatrixBytes())
			if err != nil {
				t.Fatal(err)
			}
			out, err := dec.DecodeFloat(packet)
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != len(pcm) {
				t.Fatalf("decoded %d samples, want %d", len(out), len(pcm))
			}
			for channel := 0; channel < channels; channel++ {
				var energy float64
				for i := 0; i < frameSize; i++ {
					sample := out[i*channels+channel]
					energy += sample * sample
				}
				if energy < 1e-8 {
					t.Fatalf("channel %d decoded energy=%g", channel, energy)
				}
			}
			if enc.FinalRange() != dec.FinalRange() {
				t.Fatalf("final range encoder=%08x decoder=%08x", enc.FinalRange(), dec.FinalRange())
			}
		})
	}
}

func TestProjectionPCMAPIs(t *testing.T) {
	const (
		rate      = 48000
		channels  = 4
		frameSize = 960
	)
	floatPCM := projectionFixture(frameSize, channels, rate)
	tests := []struct {
		name   string
		encode func(*ProjectionEncoder) ([]byte, error)
		decode func(*ProjectionDecoder, []byte) (int, error)
	}{
		{
			name: "int16",
			encode: func(enc *ProjectionEncoder) ([]byte, error) {
				pcm := make([]int16, len(floatPCM))
				for i, sample := range floatPCM {
					pcm[i] = int16(math.Round(sample * 32767))
				}
				return enc.Encode(pcm, frameSize)
			},
			decode: func(dec *ProjectionDecoder, packet []byte) (int, error) {
				return dec.Decode(packet, make([]int16, frameSize*channels))
			},
		},
		{
			name: "int24",
			encode: func(enc *ProjectionEncoder) ([]byte, error) {
				pcm := make([]int32, len(floatPCM))
				for i, sample := range floatPCM {
					pcm[i] = int32(math.Round(sample * 8388607))
				}
				return enc.Encode24(pcm, frameSize)
			},
			decode: func(dec *ProjectionDecoder, packet []byte) (int, error) {
				return dec.Decode24(packet, make([]int32, frameSize*channels))
			},
		},
		{
			name: "float32",
			encode: func(enc *ProjectionEncoder) ([]byte, error) {
				pcm := make([]float32, len(floatPCM))
				for i := range pcm {
					pcm[i] = float32(floatPCM[i])
				}
				return enc.EncodeFloat32(pcm, frameSize)
			},
			decode: func(dec *ProjectionDecoder, packet []byte) (int, error) {
				pcm, err := dec.DecodeFloat32(packet)
				return len(pcm) / channels, err
			},
		},
		{
			name: "float64",
			encode: func(enc *ProjectionEncoder) ([]byte, error) {
				return enc.EncodeFloat(floatPCM, frameSize)
			},
			decode: func(dec *ProjectionDecoder, packet []byte) (int, error) {
				pcm, err := dec.DecodeFloat(packet)
				return len(pcm) / channels, err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewProjectionEncoder(rate, channels, MappingFamilyProjection, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			packet, err := tc.encode(enc)
			if err != nil {
				t.Fatal(err)
			}
			dec, err := NewProjectionDecoder(rate, channels, enc.Streams(), enc.CoupledStreams(), enc.DemixingMatrixBytes())
			if err != nil {
				t.Fatal(err)
			}
			got, err := tc.decode(dec, packet)
			if err != nil {
				t.Fatal(err)
			}
			if got != frameSize {
				t.Fatalf("decoded duration=%d, want %d", got, frameSize)
			}
		})
	}
}

func TestProjectionRejectsInvalidLayoutAndMatrix(t *testing.T) {
	for _, tc := range []struct {
		channels, family int
	}{
		{0, MappingFamilyAmbisonics},
		{5, MappingFamilyAmbisonics},
		{39, MappingFamilyProjection},
		{49, MappingFamilyProjection},
		{4, MappingFamilyVorbis},
	} {
		if _, err := NewProjectionEncoder(48000, tc.channels, tc.family, ApplicationAudio); !errors.Is(err, ErrBadArg) {
			t.Fatalf("channels=%d family=%d error=%v", tc.channels, tc.family, err)
		}
	}
	if _, err := NewProjectionDecoder(48000, 4, 2, 2, make([]byte, 30)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("short matrix error=%v", err)
	}
	if _, err := NewAmbisonicsDecoder(48000, 4, MappingFamilyAmbisonics, 4, 0, []byte{0, 1}, nil); !errors.Is(err, ErrBadArg) {
		t.Fatalf("short family 2 mapping error=%v", err)
	}
}

func projectionFixture(frameSize, channels, rate int) []float64 {
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for channel := 0; channel < channels; channel++ {
			pcm[i*channels+channel] = 0.18 * math.Sin(2*math.Pi*float64(173+79*channel)*float64(i)/float64(rate))
		}
	}
	return pcm
}

func TestProjectionEncoderForwardsControls(t *testing.T) {
	enc, err := NewProjectionEncoder(48000, 4, MappingFamilyProjection, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetDTX(true)
	enc.SetPacketLossPerc(12)
	if err := enc.SetInbandFECMode(InbandFECNoModeSwitch); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Fatal(err)
	}
	for s := 0; s < enc.Streams(); s++ {
		st, err := enc.StreamEncoder(s)
		if err != nil {
			t.Fatal(err)
		}
		if !st.DTX() || st.PacketLossPerc() != 12 || st.InbandFECMode() != InbandFECNoModeSwitch || st.MaxBandwidth() != BandwidthWideband {
			t.Fatalf("stream %d did not receive the controls", s)
		}
	}
	if !enc.DTX() || enc.PacketLossPerc() != 12 || enc.InbandFECMode() != InbandFECNoModeSwitch {
		t.Fatal("projection getters do not report the forwarded controls")
	}
}

func TestProjectionEncoderForwardsEveryControl(t *testing.T) {
	enc, err := NewProjectionEncoder(48000, 4, MappingFamilyProjection, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(false)
	enc.SetVBRConstraint(false)
	if err := enc.SetComplexity(4); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetApplication(ApplicationVOIP); err != nil {
		t.Fatal(err)
	}
	enc.SetSignalType(SignalMusic)
	if err := enc.SetLSBDepth(16); err != nil {
		t.Fatal(err)
	}
	enc.SetPredictionDisabled(true)
	enc.SetPhaseInversionDisabled(true)
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetMaxPacketSize(321); err != nil {
		t.Fatal(err)
	}
	if enc.VBR() || enc.VBRConstraint() || enc.Complexity() != 4 || enc.Application() != ApplicationVOIP ||
		enc.SignalType() != SignalMusic || enc.LSBDepth() != 16 || !enc.PredictionDisabled() ||
		!enc.PhaseInversionDisabled() || enc.Bandwidth() != BandwidthWideband || enc.MaxPacketSize() != 321 {
		t.Fatal("projection getters do not report the forwarded controls")
	}
	for s := 0; s < enc.Streams(); s++ {
		st, err := enc.StreamEncoder(s)
		if err != nil {
			t.Fatal(err)
		}
		if st.VBR() || st.VBRConstraint() || st.Complexity() != 4 || st.Application() != ApplicationVOIP ||
			st.SignalType() != SignalMusic || st.LSBDepth() != 16 || !st.PredictionDisabled() ||
			!st.PhaseInversionDisabled() || st.Bandwidth() != BandwidthWideband {
			t.Fatalf("stream %d did not receive every control", s)
		}
	}
}

func TestProjectionDecoderRejectsMoreChannelsThanCodedChannels(t *testing.T) {
	// 5 output channels from 2+2 coded channels (opus_projection_decoder_init).
	if _, err := NewProjectionDecoder(48000, 5, 2, 2, make([]byte, 5*4*2)); !errors.Is(err, ErrBadArg) {
		t.Fatalf("error=%v", err)
	}
}
