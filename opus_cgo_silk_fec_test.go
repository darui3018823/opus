//go:build opusref

package opus_test

import (
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCGOEncodeRefSILKFEC validates the SILK inband-FEC (LBRR) encoder against
// libopus: the redundant frames must be grammar-correct (libopus normal-decodes
// the whole stream without desync) and genuinely recoverable (libopus'
// decode_fec path reconstructs a dropped frame from the following packet).
func TestCGOEncodeRefSILKFEC(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())

	const (
		rate     = 16000
		channels = 1
		bitrate  = 24000
		nPackets = 12
	)
	frameSize := rate * 20 / 1000
	maxSPC := rate * 120 / 1000

	newEnc := func(fec bool) *opus.Encoder {
		enc, err := opus.NewEncoder(rate, channels, opus.ApplicationVOIP)
		if err != nil {
			t.Fatalf("NewEncoder: %v", err)
		}
		if err := enc.SetBitrate(bitrate); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
		if fec {
			enc.SetPacketLossPerc(20)
			enc.SetInbandFEC(true)
			if !enc.InbandFEC() {
				t.Fatalf("InbandFEC() = false after SetInbandFEC(true)")
			}
		}
		return enc
	}

	encFEC := newEnc(true)
	encNo := newEnc(false)

	pktsFEC := make([][]byte, nPackets)
	pktsNo := make([][]byte, nPackets)
	var bytesFEC, bytesNo int
	for p := 0; p < nPackets; p++ {
		in := silkRefSpeechFrame(rate, p*frameSize, frameSize, channels)
		a, err := encFEC.EncodeFloat(in, frameSize)
		if err != nil {
			t.Fatalf("packet %d: FEC EncodeFloat: %v", p, err)
		}
		b, err := encNo.EncodeFloat(in, frameSize)
		if err != nil {
			t.Fatalf("packet %d: no-FEC EncodeFloat: %v", p, err)
		}
		pktsFEC[p] = a
		pktsNo[p] = b
		bytesFEC += len(a)
		bytesNo += len(b)

		// Both must remain SILK-only WB 20 ms mono packets (config 9).
		for _, pk := range [][]byte{a, b} {
			if config := int((pk[0] >> 3) & 0x1f); config != 9 {
				t.Fatalf("packet %d: TOC config=%d, want SILK-only WB 20ms (9)", p, config)
			}
		}
	}

	// The redundancy must cost real bytes: the FEC stream is larger than the
	// identical no-FEC stream (the LBRR frames are genuinely present).
	if bytesFEC <= bytesNo {
		t.Fatalf("FEC stream not larger than no-FEC stream: fec=%d no=%d (LBRR absent?)", bytesFEC, bytesNo)
	}
	t.Logf("stream bytes: fec=%d no-fec=%d (+%d for LBRR)", bytesFEC, bytesNo, bytesFEC-bytesNo)

	// 1) libopus must normal-decode the entire FEC stream without desync.
	refDec, err := cgoref.NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("cgoref.NewDecoder: %v", err)
	}
	defer refDec.Close()
	refFrames := make([][]float64, nPackets)
	for p := 0; p < nPackets; p++ {
		out, err := refDec.DecodeFloat(pktsFEC[p], maxSPC)
		if err != nil {
			t.Fatalf("packet %d: libopus normal decode of FEC stream failed (grammar desync): %v", p, err)
		}
		if len(out) != frameSize*channels {
			t.Fatalf("packet %d: libopus samples=%d, want %d", p, len(out), frameSize*channels)
		}
		refFrames[p] = toFloat64(out)
	}

	// The Go decoder must also consume the LBRR bodies during normal decode,
	// without treating them as current audio or losing range alignment.
	goDec, err := opus.NewDecoder(rate, channels)
	if err != nil {
		t.Fatalf("opus.NewDecoder: %v", err)
	}
	for p := 0; p < nPackets; p++ {
		pcm := make([]int16, frameSize*channels)
		n, err := goDec.Decode(pktsFEC[p], pcm)
		if err != nil {
			t.Fatalf("packet %d: Go normal decode of FEC stream: %v", p, err)
		}
		if n != frameSize {
			t.Fatalf("packet %d: Go decoded samples=%d, want %d", p, n, frameSize)
		}
		got := make([]float64, len(pcm))
		for i, v := range pcm {
			got[i] = float64(v) / 32768
		}
		snr, _, _, _ := silkRefAlignedSNR(refFrames[p], got, frameSize/2)
		if snr < 10 {
			t.Fatalf("packet %d: Go/libopus FEC-stream normal decode diverged: %.2f dB", p, snr)
		}
	}

	// 2) FEC recovery: drop frame N, reconstruct it from packet N+1 via decode_fec.
	const N = 6
	maxDelay := frameSize / 2

	recoverFrame := func(pkts [][]byte) []float64 {
		d, err := cgoref.NewDecoder(rate, channels)
		if err != nil {
			t.Fatalf("cgoref.NewDecoder: %v", err)
		}
		defer d.Close()
		for p := 0; p < N; p++ {
			if _, err := d.DecodeFloat(pkts[p], maxSPC); err != nil {
				t.Fatalf("priming decode %d: %v", p, err)
			}
		}
		out, err := d.DecodeFloatFEC(pkts[N+1], frameSize)
		if err != nil {
			t.Fatalf("DecodeFloatFEC: %v", err)
		}
		return toFloat64(out)
	}

	recFEC := recoverFrame(pktsFEC)
	recPLC := recoverFrame(pktsNo)

	snrFEC, rmseFEC, _, _ := silkRefAlignedSNR(refFrames[N], recFEC, maxDelay)
	snrPLC, rmsePLC, _, _ := silkRefAlignedSNR(refFrames[N], recPLC, maxDelay)
	t.Logf("frame %d recovery: FEC alignedSNR=%.2fdB rmse=%.4f | PLC(no-FEC) alignedSNR=%.2fdB rmse=%.4f",
		N, snrFEC, rmseFEC, snrPLC, rmsePLC)

	// The LBRR reconstruction must be a real match to the lost frame, and clearly
	// better than the PLC extrapolation libopus falls back to with no redundancy.
	if snrFEC < 3.0 {
		t.Fatalf("FEC reconstruction too poor: alignedSNR=%.2fdB rmse=%.4f", snrFEC, rmseFEC)
	}
	if snrFEC < snrPLC+3.0 {
		t.Fatalf("FEC did not improve over PLC baseline: FEC=%.2fdB PLC=%.2fdB", snrFEC, snrPLC)
	}
}

// TestCGOEncodeRefSILKFECMultiFrame exercises the per-frame LBRR flag grammar for
// multi-frame (2 and 3 SILK frame) packets: libopus must normal-decode the whole
// FEC stream without desync, and decode_fec must reconstruct the previous packet.
func TestCGOEncodeRefSILKFECMultiFrame(t *testing.T) {
	const (
		rate     = 16000
		channels = 1
		bitrate  = 24000
		nPackets = 10
	)
	maxSPC := rate * 120 / 1000

	for _, packetMs := range []int{40, 60} {
		packetMs := packetMs
		wantConfig := 8 + packetMs/20 // WB: 40ms→10, 60ms→11
		t.Run(silkRefPacketName(packetMs), func(t *testing.T) {
			frameSize := rate * packetMs / 1000

			mkEnc := func(fec bool) *opus.Encoder {
				enc, err := opus.NewEncoder(rate, channels, opus.ApplicationVOIP)
				if err != nil {
					t.Fatalf("NewEncoder: %v", err)
				}
				if err := enc.SetBitrate(bitrate); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				if fec {
					enc.SetPacketLossPerc(20)
					enc.SetInbandFEC(true)
				}
				return enc
			}
			encFEC, encNo := mkEnc(true), mkEnc(false)

			pktsFEC := make([][]byte, nPackets)
			pktsNo := make([][]byte, nPackets)
			var bytesFEC, bytesNo int
			for p := 0; p < nPackets; p++ {
				in := silkRefSpeechFrame(rate, p*frameSize, frameSize, channels)
				a, err := encFEC.EncodeFloat(in, frameSize)
				if err != nil {
					t.Fatalf("packet %d: FEC EncodeFloat: %v", p, err)
				}
				b, err := encNo.EncodeFloat(in, frameSize)
				if err != nil {
					t.Fatalf("packet %d: no-FEC EncodeFloat: %v", p, err)
				}
				if config := int((a[0] >> 3) & 0x1f); config != wantConfig {
					t.Fatalf("packet %d: TOC config=%d, want %d", p, config, wantConfig)
				}
				pktsFEC[p] = a
				pktsNo[p] = b
				bytesFEC += len(a)
				bytesNo += len(b)
			}
			if bytesFEC <= bytesNo {
				t.Fatalf("FEC stream not larger: fec=%d no=%d", bytesFEC, bytesNo)
			}

			// Grammar: libopus must normal-decode every packet (the per-frame LBRR
			// flag symbols and conditional LBRR coding must be exactly placed).
			refDec, err := cgoref.NewDecoder(rate, channels)
			if err != nil {
				t.Fatalf("cgoref.NewDecoder: %v", err)
			}
			defer refDec.Close()
			refFrames := make([][]float64, nPackets)
			for p := 0; p < nPackets; p++ {
				out, err := refDec.DecodeFloat(pktsFEC[p], maxSPC)
				if err != nil {
					t.Fatalf("packet %d: libopus normal decode failed (grammar desync): %v", p, err)
				}
				refFrames[p] = toFloat64(out)
			}

			// Cross-check the same FEC packets with the Go decoder. This verifies
			// that its normal path consumes all LBRR bodies before decoding the
			// current packet's regular frames.
			goDec, err := opus.NewDecoder(rate, channels)
			if err != nil {
				t.Fatalf("opus.NewDecoder: %v", err)
			}
			for p := 0; p < nPackets; p++ {
				pcm := make([]int16, frameSize*channels)
				n, err := goDec.Decode(pktsFEC[p], pcm)
				if err != nil {
					t.Fatalf("packet %d: Go normal decode of FEC stream: %v", p, err)
				}
				if n != frameSize {
					t.Fatalf("packet %d: Go decoded samples=%d, want %d", p, n, frameSize)
				}
				got := make([]float64, len(pcm))
				for i, v := range pcm {
					got[i] = float64(v) / 32768
				}
				s, _, _, _ := silkRefAlignedSNR(refFrames[p], got, frameSize/2)
				if s < 8 {
					t.Fatalf("packet %d: Go/libopus FEC-stream normal decode diverged: %.2f dB", p, s)
				}
			}

			// Recovery of the dropped multi-frame packet via decode_fec.
			const N = 5
			d, err := cgoref.NewDecoder(rate, channels)
			if err != nil {
				t.Fatalf("cgoref.NewDecoder: %v", err)
			}
			defer d.Close()
			for p := 0; p < N; p++ {
				if _, err := d.DecodeFloat(pktsFEC[p], maxSPC); err != nil {
					t.Fatalf("priming decode %d: %v", p, err)
				}
			}
			rec, err := d.DecodeFloatFEC(pktsFEC[N+1], frameSize)
			if err != nil {
				t.Fatalf("DecodeFloatFEC: %v", err)
			}
			snr, rmse, dl, _ := silkRefAlignedSNR(refFrames[N], toFloat64(rec), frameSize/2)
			t.Logf("%dms: bytes fec=%d no=%d (+%d) | frame %d FEC recovery alignedSNR=%.2fdB rmse=%.4f delay=%d len(rec)=%d len(ref)=%d",
				packetMs, bytesFEC, bytesNo, bytesFEC-bytesNo, N, snr, rmse, dl, len(rec), len(refFrames[N]))
			if snr < 3.0 {
				t.Fatalf("multi-frame FEC reconstruction too poor: alignedSNR=%.2fdB", snr)
			}
		})
	}
}

func toFloat64(x []float32) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = float64(v)
	}
	return out
}
