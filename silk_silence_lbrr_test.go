package opus

import (
	"fmt"
	"math"
	"testing"
)

func TestSILKSilenceCarriesPendingLBRR(t *testing.T) {
	const rate = 16000
	for _, channels := range []int{1, 2} {
		for _, durationMs := range []int{20, 40, 60} {
			for _, dtx := range []bool{false, true} {
				name := fmt.Sprintf("%dch/%dms/dtx=%v", channels, durationMs, dtx)
				t.Run(name, func(t *testing.T) {
					frameSize := rate * durationMs / 1000
					enc, err := NewEncoder(rate, channels, ApplicationVOIP)
					if err != nil {
						t.Fatal(err)
					}
					bitrate := 24000
					if channels == 2 {
						bitrate = 40000
					}
					if err := enc.SetBitrate(bitrate); err != nil {
						t.Fatal(err)
					}
					enc.SetInbandFEC(true)
					enc.SetPacketLossPerc(20)
					enc.SetDTX(dtx)

					active := strictSpeechLikeFrame(rate, channels, 0, frameSize)
					first, err := enc.EncodeFloat(active, frameSize)
					if err != nil {
						t.Fatalf("active packet: %v", err)
					}
					if _, err := PacketHasLBRR(first); err != nil {
						t.Fatalf("first LBRR parse: %v", err)
					}

					carrier, err := enc.EncodeFloat(make([]float64, frameSize*channels), frameSize)
					if err != nil {
						t.Fatalf("silent carrier: %v", err)
					}
					if has, err := PacketHasLBRR(carrier); err != nil || !has {
						t.Fatalf("silent carrier LBRR=%v err=%v, want true", has, err)
					}
					if dtx && enc.InDTX() {
						t.Fatal("silent LBRR carrier reported DTX")
					}
					if enc.silkEncoder.HasAnyPendingLBRR() {
						t.Fatal("silent carrier retained newly generated LBRR")
					}

					normalDec, err := NewDecoder(rate, channels)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := normalDec.DecodeFloat(carrier); err != nil {
						t.Fatalf("normal carrier decode: %v", err)
					}
					if normalDec.FinalRange() != enc.FinalRange() {
						t.Fatalf("carrier final range encoder=%08x decoder=%08x", enc.FinalRange(), normalDec.FinalRange())
					}

					fecDec, err := NewDecoder(rate, channels)
					if err != nil {
						t.Fatal(err)
					}
					recovered, err := fecDec.DecodeFECFloat(carrier, frameSize)
					if err != nil {
						t.Fatalf("carrier FEC decode: %v", err)
					}
					var recoveredEnergy float64
					for _, sample := range recovered {
						recoveredEnergy += sample * sample
					}
					if math.IsNaN(recoveredEnergy) || recoveredEnergy <= 1e-8 {
						t.Fatalf("recovered FEC energy=%g, want active audio", recoveredEnergy)
					}

				})
			}
		}
	}
}

func TestSILKSilenceWithoutPendingLBRRStaysMinimal(t *testing.T) {
	for _, channels := range []int{1, 2} {
		for _, dtx := range []bool{false, true} {
			name := fmt.Sprintf("%dch/dtx=%v", channels, dtx)
			t.Run(name, func(t *testing.T) {
				enc, err := NewEncoder(16000, channels, ApplicationVOIP)
				if err != nil {
					t.Fatal(err)
				}
				if err := enc.SetBitrate(24000 + (channels-1)*16000); err != nil {
					t.Fatal(err)
				}
				enc.SetInbandFEC(true)
				enc.SetPacketLossPerc(20)
				enc.SetDTX(dtx)
				packet, err := enc.EncodeFloat(make([]float64, 320*channels), 320)
				if err != nil {
					t.Fatal(err)
				}
				if len(packet) != 2 || packet[1] != 0 {
					t.Fatalf("minimal silence packet=%x, want TOC plus one zero byte", packet)
				}
			})
		}
	}
}

func TestSILKSilenceExpiresIncompatiblePendingLBRR(t *testing.T) {
	const rate = 16000
	enc, err := NewEncoder(rate, 1, ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatal(err)
	}
	enc.SetInbandFEC(true)
	enc.SetPacketLossPerc(20)

	if _, err := enc.EncodeFloat(strictSpeechLikeFrame(rate, 1, 0, 320), 320); err != nil {
		t.Fatal(err)
	}
	silent40, err := enc.EncodeFloat(make([]float64, 640), 640)
	if err != nil {
		t.Fatal(err)
	}
	if has, err := PacketHasLBRR(silent40); err != nil || has {
		t.Fatalf("duration-mismatch silence LBRR=%v err=%v, want false", has, err)
	}
	active20, err := enc.EncodeFloat(strictSpeechLikeFrame(rate, 1, 960, 320), 320)
	if err != nil {
		t.Fatal(err)
	}
	if has, err := PacketHasLBRR(active20); err != nil || has {
		t.Fatalf("stale LBRR reappeared after duration change: %v err=%v", has, err)
	}
}
