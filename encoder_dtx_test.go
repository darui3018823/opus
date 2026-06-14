package opus

import (
	"math"
	"testing"
)

// TestEncoderDTXSilencePackets verifies that with DTX enabled, silent input
// produces tiny packets while a complex signal still fills the target, and that
// silent packets decode back to digital silence.
func TestEncoderDTXSilencePackets(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 64000
		nFrames    = 8
	)

	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	_ = enc.SetBitrate(bitrate)
	enc.SetDTX(true)
	if !enc.DTX() {
		t.Fatal("DTX() should report true after SetDTX(true)")
	}

	dec, err := NewDecoder(sampleRate, channels)
	if err != nil {
		t.Fatal(err)
	}

	silent := make([]float64, frameSize*channels)
	for f := 0; f < nFrames; f++ {
		pkt, err := enc.EncodeFloat(silent, frameSize)
		if err != nil {
			t.Fatalf("silent frame %d: %v", f, err)
		}
		if len(pkt) > 4 {
			t.Errorf("DTX silent packet too large: frame %d got %d bytes", f, len(pkt))
		}
		out, err := dec.DecodeFloat(pkt)
		if err != nil {
			t.Fatalf("decode silent frame %d (%d bytes): %v", f, len(pkt), err)
		}
		var peak float64
		for _, v := range out {
			if a := math.Abs(v); a > peak {
				peak = a
			}
		}
		if peak > 1e-6 {
			t.Errorf("silent frame %d decoded to non-silence (peak=%g)", f, peak)
		}
	}

	// A complex frame should still use the full budget.
	complexPCM := make([]float64, frameSize*channels)
	for i := range complexPCM {
		ts := float64(i) / float64(sampleRate)
		complexPCM[i] = 0.3*math.Sin(2*math.Pi*440*ts) +
			0.3*math.Sin(2*math.Pi*1000*ts) +
			0.2*math.Sin(2*math.Pi*4000*ts)
	}
	pkt, err := enc.EncodeFloat(complexPCM, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) < 100 {
		t.Errorf("complex DTX packet unexpectedly small: %d bytes", len(pkt))
	}
}

// TestEncoderDTXOffCBRFixedSize verifies that without DTX, CBR keeps a fixed
// packet size even for silent input.
func TestEncoderDTXOffCBRFixedSize(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 64000
	)
	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	_ = enc.SetBitrate(bitrate)
	enc.SetVBR(false) // CBR, DTX off (default)

	want := bitrate*20/1000/8 + 1 // CELT payload + TOC byte
	silent := make([]float64, frameSize*channels)
	for f := 0; f < 4; f++ {
		pkt, err := enc.EncodeFloat(silent, frameSize)
		if err != nil {
			t.Fatal(err)
		}
		if len(pkt) != want {
			t.Errorf("frame %d: CBR (DTX off) silent packet got %d bytes, want %d", f, len(pkt), want)
		}
	}
}

// TestEncoderDTXMultiFrame verifies that DTX works with multi-frame packets:
// a 40 ms silent request packs two silent CELT frames into one small packet
// that decodes to silence.
func TestEncoderDTXMultiFrame(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		base       = 960
		frameSize  = 2 * base // 40 ms
		bitrate    = 64000
	)
	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	_ = enc.SetBitrate(bitrate)
	enc.SetDTX(true)

	dec, err := NewDecoder(sampleRate, channels)
	if err != nil {
		t.Fatal(err)
	}

	silent := make([]float64, frameSize*channels)
	pkt, err := enc.EncodeFloat(silent, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) > 12 {
		t.Errorf("DTX 40ms silent packet too large: %d bytes", len(pkt))
	}
	out, err := dec.DecodeFloat(pkt)
	if err != nil {
		t.Fatalf("decode 40ms silent packet (%d bytes): %v", len(pkt), err)
	}
	if len(out) != frameSize {
		t.Fatalf("decoded %d samples, want %d", len(out), frameSize)
	}
	var peak float64
	for _, v := range out {
		if a := math.Abs(v); a > peak {
			peak = a
		}
	}
	if peak > 1e-6 {
		t.Errorf("40ms silent packet decoded to non-silence (peak=%g)", peak)
	}
}
