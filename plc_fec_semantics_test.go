package opus

import (
	"errors"
	"reflect"
	"testing"
)

func TestDecodePLCBeforeFirstPacket(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		dec, err := NewDecoder(rate, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, quanta := range []int{1, 3, 5, 12, 48} {
			frameSize := quanta * rate / 400
			pcm := make([]int16, frameSize*2)
			n, err := dec.DecodePLC(pcm, frameSize)
			if err != nil || n != frameSize {
				t.Fatalf("rate=%d quanta=%d: DecodePLC=(%d,%v)", rate, quanta, n, err)
			}
			for i, sample := range pcm {
				if sample != 0 {
					t.Fatalf("rate=%d quanta=%d: pcm[%d]=%d, want zero", rate, quanta, i, sample)
				}
			}
			if dec.FinalRange() != 0 || dec.GetLastPacketDuration() != frameSize {
				t.Fatalf("rate=%d quanta=%d: range=%08x duration=%d", rate, quanta, dec.FinalRange(), dec.GetLastPacketDuration())
			}
		}
	}
}

func TestDecodePLCFrameSizeSet(t *testing.T) {
	const rate = 48000
	enc, err := NewEncoder(rate, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(make([]int16, 960), 960)
	if err != nil {
		t.Fatal(err)
	}
	for quanta := 1; quanta <= 48; quanta++ {
		dec, err := NewDecoder(rate, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dec.Decode(packet, make([]int16, 960)); err != nil {
			t.Fatal(err)
		}
		frameSize := quanta * rate / 400
		if n, err := dec.DecodePLC(make([]int16, frameSize), frameSize); err != nil || n != frameSize {
			t.Fatalf("quanta=%d: DecodePLC=(%d,%v), want (%d,nil)", quanta, n, err, frameSize)
		}
		if dec.FinalRange() != 0 {
			t.Fatalf("quanta=%d: FinalRange=%08x, want 0", quanta, dec.FinalRange())
		}
	}
}

func TestDecoderLossPCMVariants(t *testing.T) {
	const (
		rate      = 48000
		frameSize = 360 // 7.5 ms, intentionally not a packet duration.
	)
	intDec, _ := NewDecoder(rate, 1)
	floatDec, _ := NewDecoder(rate, 1)
	float32Dec, _ := NewDecoder(rate, 1)
	int24Dec, _ := NewDecoder(rate, 1)

	i16 := make([]int16, frameSize)
	i24 := make([]int32, frameSize)
	if _, err := intDec.DecodePLC(i16, frameSize); err != nil {
		t.Fatal(err)
	}
	f64, err := floatDec.DecodePLCFloat(frameSize)
	if err != nil {
		t.Fatal(err)
	}
	f32, err := float32Dec.DecodePLCFloat32(frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := int24Dec.DecodePLC24(i24, frameSize); err != nil {
		t.Fatal(err)
	}
	for i := range i16 {
		if i16[i] != 0 || i24[i] != 0 || f64[i] != 0 || f32[i] != 0 {
			t.Fatalf("sample %d differs: %d %d %g %g", i, i16[i], i24[i], f64[i], f32[i])
		}
	}

	for _, bad := range []int{0, rate/400 + 1, rate*120/1000 + rate/400} {
		if _, err := intDec.DecodePLC(make([]int16, frameSize), bad); !errors.Is(err, ErrUnsupportedFrameSize) {
			t.Fatalf("frameSize=%d error=%v, want ErrUnsupportedFrameSize", bad, err)
		}
	}
}

func TestDecodeFECPackedUsesFirstOpusFrame(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
		lost      = 4
	)
	packets := makeFECPackets(t, rate, frameSize, 8)
	carrier := packets[lost+1]
	if has, err := PacketHasLBRR(carrier); err != nil || !has {
		t.Fatalf("carrier LBRR=(%v,%v), want true", has, err)
	}
	rp := NewRepacketizer()
	if err := rp.Cat(carrier); err != nil {
		t.Fatal(err)
	}
	if err := rp.Cat(packets[lost+2]); err != nil {
		t.Fatal(err)
	}
	packed, err := rp.Out()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := PacketGetNumFrames(packed); err != nil || n != 2 {
		t.Fatalf("packed frame count=(%d,%v), want 2", n, err)
	}

	single := primedLossDecoder(t, rate, packets[:lost])
	combined := primedLossDecoder(t, rate, packets[:lost])
	want, err := single.DecodeFECFloat(carrier, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	got, err := combined.DecodeFECFloat(packed, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("packed FEC differs from its first Opus frame")
	}
	if combined.FinalRange() != single.FinalRange() {
		t.Fatalf("packed range=%08x, single=%08x", combined.FinalRange(), single.FinalRange())
	}

	legacy := primedLossDecoder(t, rate, packets[:lost])
	legacyPCM := make([]int16, 2*frameSize)
	if n, err := legacy.DecodeFEC(packed, legacyPCM); err != nil || n != 2*frameSize {
		t.Fatalf("legacy packed DecodeFEC=(%d,%v), want (%d,nil)", n, err, 2*frameSize)
	}
}

func TestDecodeFECExplicitDurationPrependsPLC(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
		lost      = 4
	)
	packets := makeFECPackets(t, rate, frameSize, 8)
	combined := primedLossDecoder(t, rate, packets[:lost])
	manual := primedLossDecoder(t, rate, packets[:lost])

	got, err := combined.DecodeFECFloat(packets[lost+1], 2*frameSize)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := manual.DecodePLCFloat(frameSize)
	if err != nil {
		t.Fatal(err)
	}
	suffix, err := manual.DecodeFECFloat(packets[lost+1], frameSize)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]float64(nil), prefix...), suffix...)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("explicit longer loss does not equal PLC prefix plus FEC suffix")
	}
	if combined.GetLastPacketDuration() != 2*frameSize {
		t.Fatalf("last duration=%d, want %d", combined.GetLastPacketDuration(), 2*frameSize)
	}
}

func TestDecodeFECPackedErrorPreservesState(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
		lost      = 4
	)
	packets := makeFECPackets(t, rate, frameSize, 8)
	rp := NewRepacketizer()
	if err := rp.Cat(packets[lost+1]); err != nil {
		t.Fatal(err)
	}
	if err := rp.Cat(packets[lost+2]); err != nil {
		t.Fatal(err)
	}
	packed, err := rp.Out()
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), packed[:len(packed)-1]...)

	candidate := primedLossDecoder(t, rate, packets[:lost])
	control := primedLossDecoder(t, rate, packets[:lost])
	dst := make([]int16, 2*frameSize)
	for i := range dst {
		dst[i] = 1234
	}
	if _, err := candidate.DecodeFECWithDuration(corrupt, dst, 2*frameSize); err == nil {
		t.Fatal("corrupt packed FEC unexpectedly succeeded")
	}
	for i, sample := range dst {
		if sample != 1234 {
			t.Fatalf("error modified destination[%d]=%d", i, sample)
		}
	}
	got, err := candidate.DecodePLCFloat(frameSize)
	if err != nil {
		t.Fatal(err)
	}
	want, err := control.DecodePLCFloat(frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("corrupt packed FEC advanced decoder state")
	}
	if candidate.FinalRange() != control.FinalRange() || candidate.Pitch() != control.Pitch() ||
		candidate.Bandwidth() != control.Bandwidth() || candidate.GetLastPacketDuration() != control.GetLastPacketDuration() {
		t.Fatal("corrupt packed FEC changed observable decoder state")
	}
}

func makeFECPackets(t *testing.T, rate, frameSize, count int) [][]byte {
	t.Helper()
	enc, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatal(err)
	}
	enc.SetInbandFEC(true)
	enc.SetPacketLossPerc(20)
	packets := make([][]byte, count)
	for i := range packets {
		packets[i], err = enc.EncodeFloat(strictSpeechLikeFrame(rate, 1, i*frameSize, frameSize), frameSize)
		if err != nil {
			t.Fatalf("encode packet %d: %v", i, err)
		}
	}
	return packets
}

func primedLossDecoder(t *testing.T, rate int, packets [][]byte) *Decoder {
	t.Helper()
	dec, err := NewDecoder(rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i, packet := range packets {
		if _, err := dec.DecodeFloat(packet); err != nil {
			t.Fatalf("prime packet %d: %v", i, err)
		}
	}
	return dec
}
