//go:build opusref

package opus_test

import (
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCGOLibopusSelfFEC measures libopus' OWN encoder+decode_fec recovery in this
// harness, to distinguish a libopus multi-frame decode_fec behaviour from an
// encoder bug under test. Diagnostic only.
func TestCGOLibopusSelfFEC(t *testing.T) {
	const (
		rate     = 16000
		channels = 1
		bitrate  = 24000
		nPackets = 10
		N        = 5
	)
	for _, packetMs := range []int{20, 40, 60} {
		frameSize := rate * packetMs / 1000
		maxSPC := rate * 120 / 1000

		enc, err := cgoref.NewEncoder(rate, channels, 2048 /*OPUS_APPLICATION_VOIP*/)
		if err != nil {
			t.Fatalf("cgoref.NewEncoder: %v", err)
		}
		_ = enc.SetBitrate(bitrate)
		_ = enc.SetVoiceMode()
		if err := enc.SetInbandFEC(true); err != nil {
			t.Fatalf("SetInbandFEC: %v", err)
		}
		if err := enc.SetPacketLossPerc(20); err != nil {
			t.Fatalf("SetPacketLossPerc: %v", err)
		}

		pkts := make([][]byte, nPackets)
		for p := 0; p < nPackets; p++ {
			in64 := silkRefSpeechFrame(rate, p*frameSize, frameSize, channels)
			in := make([]float32, len(in64))
			for i, v := range in64 {
				in[i] = float32(v)
			}
			pkt, err := enc.Encode(in, frameSize)
			if err != nil {
				t.Fatalf("%dms packet %d: Encode: %v", packetMs, p, err)
			}
			pkts[p] = pkt
		}
		enc.Close()

		refDec, _ := cgoref.NewDecoder(rate, channels)
		refFrames := make([][]float64, nPackets)
		for p := 0; p < nPackets; p++ {
			out, err := refDec.DecodeFloat(pkts[p], maxSPC)
			if err != nil {
				t.Fatalf("%dms packet %d: normal decode: %v", packetMs, p, err)
			}
			refFrames[p] = toFloat64(out)
		}
		refDec.Close()

		d, _ := cgoref.NewDecoder(rate, channels)
		for p := 0; p < N; p++ {
			d.DecodeFloat(pkts[p], maxSPC)
		}
		rec, err := d.DecodeFloatFEC(pkts[N+1], frameSize)
		d.Close()
		if err != nil {
			t.Fatalf("%dms DecodeFloatFEC: %v", packetMs, err)
		}
		snr, rmse, dl, _ := silkRefAlignedSNR(refFrames[N], toFloat64(rec), frameSize/2)
		t.Logf("libopus-self %dms: FEC recovery alignedSNR=%.2fdB rmse=%.4f delay=%d", packetMs, snr, rmse, dl)
	}
}
