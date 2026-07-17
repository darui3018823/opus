//go:build opusref

package opus_test

import (
	"math"
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCGODecodeLossSemantics(t *testing.T) {
	const (
		rate      = 16000
		frameSize = 320
		lost      = 4
	)

	goInitial, err := opus.NewDecoder(rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	refInitial, err := cgoref.NewDecoder(rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer refInitial.Close()
	for _, duration := range []int{rate / 400, 3 * rate / 400, 12 * rate / 400, 48 * rate / 400} {
		goPCM, err := goInitial.DecodePLCFloat(duration)
		if err != nil {
			t.Fatal(err)
		}
		refPCM, err := refInitial.DecodeFloat(nil, duration)
		if err != nil {
			t.Fatal(err)
		}
		if len(goPCM) != len(refPCM) {
			t.Fatalf("initial PLC duration %d: lengths %d != %d", duration, len(goPCM), len(refPCM))
		}
		for i := range goPCM {
			if goPCM[i] != 0 || refPCM[i] != 0 {
				t.Fatalf("initial PLC duration %d sample %d: Go=%g ref=%g", duration, i, goPCM[i], refPCM[i])
			}
		}
		refRange, err := refInitial.FinalRange()
		if err != nil {
			t.Fatal(err)
		}
		if goInitial.FinalRange() != refRange {
			t.Fatalf("initial PLC duration %d range: Go=%08x ref=%08x", duration, goInitial.FinalRange(), refRange)
		}
	}

	enc, err := opus.NewEncoder(rate, 1, opus.ApplicationVOIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatal(err)
	}
	enc.SetInbandFEC(true)
	enc.SetPacketLossPerc(20)
	packets := make([][]byte, 8)
	for p := range packets {
		pcm := make([]float64, frameSize)
		for i := range pcm {
			at := float64(p*frameSize+i) / rate
			pcm[i] = .55*math.Sin(2*math.Pi*180*at) + .2*math.Sin(2*math.Pi*360*at)
		}
		packets[p], err = enc.EncodeFloat(pcm, frameSize)
		if err != nil {
			t.Fatal(err)
		}
	}
	goDec, _ := opus.NewDecoder(rate, 1)
	refDec, err := cgoref.NewDecoder(rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer refDec.Close()
	for p := 0; p < lost; p++ {
		if _, err := goDec.DecodeFloat(packets[p]); err != nil {
			t.Fatal(err)
		}
		if _, err := refDec.DecodeFloat(packets[p], frameSize); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := goDec.DecodeFECFloat(packets[lost+1], 2*frameSize); err != nil {
		t.Fatal(err)
	}
	if _, err := refDec.DecodeFloatFEC(packets[lost+1], 2*frameSize); err != nil {
		t.Fatal(err)
	}
	refRange, err := refDec.FinalRange()
	if err != nil {
		t.Fatal(err)
	}
	if goDec.FinalRange() != refRange {
		t.Fatalf("SILK FEC range: Go=%08x ref=%08x", goDec.FinalRange(), refRange)
	}
}
