//go:build opusref

package opus_test

import (
	"testing"

	opus "github.com/darui3018823/opus"
	"github.com/darui3018823/opus/internal/cgoref"
)

// The SILK decoder's packet-loss concealment, comfort-noise generation, and
// post-loss glue are ported from libopus silk/PLC.c, silk/CNG.c,
// silk/decode_frame.c, and silk/decode_core.c. These oracles require the Go
// int16 output to equal libopus sample for sample through loss, in-band FEC
// with missing LBRR subframes, and the first good frames after a loss, at
// the SILK internal rate so no resampler sits between the two decoders.

// silkPLCExactEncode produces SILK-only packets for the oracle. 20 ms and
// longer durations come from the Go encoder; 10 ms frames come from the
// libopus encoder because the Go encoder routes sub-20 ms input to CELT.
func silkPLCExactEncode(t *testing.T, rate, channels, packetMs, nPackets int, fec bool) [][]byte {
	t.Helper()
	frameSize := rate * packetMs / 1000
	packets := make([][]byte, nPackets)
	if packetMs == 10 {
		enc, err := cgoref.NewEncoder(rate, channels, int(opus.ApplicationVOIP))
		if err != nil {
			t.Fatalf("cgoref.NewEncoder: %v", err)
		}
		defer enc.Close()
		if err := enc.SetBitrate(24000); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
		if err := enc.SetVoiceMode(); err != nil {
			t.Fatalf("SetVoiceMode: %v", err)
		}
		if err := enc.SetBandwidth(opus.BandwidthWideband); err != nil {
			t.Fatalf("SetBandwidth: %v", err)
		}
		if fec {
			_ = enc.SetPacketLossPerc(20)
			_ = enc.SetInbandFEC(true)
		}
		for p := range packets {
			in := silkRefSpeechFrame(rate, p*frameSize, frameSize, channels)
			in32 := make([]float32, len(in))
			for i, v := range in {
				in32[i] = float32(v)
			}
			packets[p], err = enc.Encode(in32, frameSize)
			if err != nil {
				t.Fatalf("packet %d: libopus Encode: %v", p, err)
			}
			if mode, _ := opus.PacketGetMode(packets[p]); mode != opus.ModeSILKOnly {
				t.Fatalf("packet %d: mode %d, want SILK-only", p, mode)
			}
		}
		return packets
	}

	enc, err := opus.NewEncoder(rate, channels, opus.ApplicationVOIP)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	// Keep the mode decision on SILK for every packet duration in the matrix.
	enc.SetSignalType(opus.SignalVoice)
	if err := enc.SetMaxBandwidth(opus.BandwidthWideband); err != nil {
		t.Fatalf("SetMaxBandwidth: %v", err)
	}
	if fec {
		enc.SetPacketLossPerc(20)
		enc.SetInbandFEC(true)
	}
	for p := range packets {
		in := silkRefSpeechFrame(rate, p*frameSize, frameSize, channels)
		packets[p], err = enc.EncodeFloat(in, frameSize)
		if err != nil {
			t.Fatalf("packet %d: EncodeFloat: %v", p, err)
		}
		if mode, _ := opus.PacketGetMode(packets[p]); mode != opus.ModeSILKOnly {
			t.Fatalf("packet %d: mode %d, want SILK-only", p, mode)
		}
	}
	return packets
}

func silkPLCExactCompare(t *testing.T, label string, got, want []int16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: Go samples=%d, libopus samples=%d", label, len(got), len(want))
	}
	mismatches := 0
	first := -1
	maxDelta := 0
	for i := range want {
		if got[i] != want[i] {
			if first < 0 {
				first = i
			}
			mismatches++
			delta := int(got[i]) - int(want[i])
			if delta < 0 {
				delta = -delta
			}
			if delta > maxDelta {
				maxDelta = delta
			}
		}
	}
	if mismatches > 0 {
		t.Fatalf("%s: %d/%d samples differ; first at %d: Go=%d libopus=%d (max |delta|=%d)",
			label, mismatches, len(want), first, got[first], want[first], maxDelta)
	}
}

// TestCGOSILKPLCExact loses whole packets and requires every concealed frame
// and every subsequent good frame to be sample-exact against libopus.
func TestCGOSILKPLCExact(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())
	const nPackets = 16
	for _, tc := range []struct {
		name     string
		rate     int
		packetMs int
		lost     map[int]bool
	}{
		{name: "16k-20ms-single-loss", rate: 16000, packetMs: 20, lost: map[int]bool{5: true}},
		{name: "16k-20ms-double-loss", rate: 16000, packetMs: 20, lost: map[int]bool{6: true, 7: true}},
		{name: "16k-20ms-triple-loss", rate: 16000, packetMs: 20, lost: map[int]bool{4: true, 5: true, 6: true}},
		{name: "8k-20ms-double-loss", rate: 8000, packetMs: 20, lost: map[int]bool{6: true, 7: true}},
		{name: "12k-20ms-double-loss", rate: 12000, packetMs: 20, lost: map[int]bool{6: true, 7: true}},
		{name: "16k-10ms-double-loss", rate: 16000, packetMs: 10, lost: map[int]bool{6: true, 7: true}},
		{name: "16k-40ms-single-loss", rate: 16000, packetMs: 40, lost: map[int]bool{5: true}},
		{name: "16k-60ms-single-loss", rate: 16000, packetMs: 60, lost: map[int]bool{5: true}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			frameSize := tc.rate * tc.packetMs / 1000
			packets := silkPLCExactEncode(t, tc.rate, 1, tc.packetMs, nPackets, false)

			goDec, err := opus.NewDecoder(tc.rate, 1)
			if err != nil {
				t.Fatal(err)
			}
			refDec, err := cgoref.NewDecoder(tc.rate, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer refDec.Close()

			for p := 0; p < nPackets; p++ {
				goPCM := make([]int16, frameSize)
				var refPCM []int16
				var n int
				if tc.lost[p] {
					n, err = goDec.DecodePLC(goPCM, frameSize)
					if err != nil {
						t.Fatalf("packet %d: Go DecodePLC: %v", p, err)
					}
					refPCM, err = refDec.Decode(nil, frameSize)
					if err != nil {
						t.Fatalf("packet %d: libopus PLC: %v", p, err)
					}
				} else {
					n, err = goDec.Decode(packets[p], goPCM)
					if err != nil {
						t.Fatalf("packet %d: Go Decode: %v", p, err)
					}
					refPCM, err = refDec.Decode(packets[p], frameSize)
					if err != nil {
						t.Fatalf("packet %d: libopus Decode: %v", p, err)
					}
				}
				if n != frameSize {
					t.Fatalf("packet %d: Go samples=%d, want %d", p, n, frameSize)
				}
				label := "packet " + itoa(p)
				if tc.lost[p] {
					label += " (concealed)"
				}
				silkPLCExactCompare(t, label, goPCM[:n], refPCM)
			}
		})
	}
}

// TestCGOSILKOneBytePayloadExact feeds one-byte SILK payloads (the encoder's
// "run the PLC" frames) followed by explicit loss. libopus treats a payload
// of at most one byte as a lost frame, so concealment must continue the
// pre-silence state exactly rather than switching to digital silence.
func TestCGOSILKOneBytePayloadExact(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())
	const (
		rate     = 16000
		nPackets = 6
	)
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(itoa(channels)+"ch", func(t *testing.T) {
			frameSize := rate / 50
			packets := silkPLCExactEncode(t, rate, channels, 20, nPackets, false)
			silence := []byte{packets[0][0] &^ 0x03, 0x00}

			goDec, err := opus.NewDecoder(rate, channels)
			if err != nil {
				t.Fatal(err)
			}
			refDec, err := cgoref.NewDecoder(rate, channels)
			if err != nil {
				t.Fatal(err)
			}
			defer refDec.Close()

			sequence := append(append([][]byte{}, packets...), silence, silence, nil, silence)
			for p, pkt := range sequence {
				goPCM := make([]int16, frameSize*channels)
				var n int
				var refPCM []int16
				label := "packet " + itoa(p)
				switch {
				case pkt == nil:
					label += " (lost)"
					n, err = goDec.DecodePLC(goPCM, frameSize)
					if err != nil {
						t.Fatalf("%s: Go DecodePLC: %v", label, err)
					}
					refPCM, err = refDec.Decode(nil, frameSize)
				default:
					if len(pkt) == 2 {
						label += " (one-byte payload)"
					}
					n, err = goDec.Decode(pkt, goPCM)
					if err != nil {
						t.Fatalf("%s: Go Decode: %v", label, err)
					}
					refPCM, err = refDec.Decode(pkt, frameSize)
				}
				if err != nil {
					t.Fatalf("%s: libopus decode: %v", label, err)
				}
				if n != frameSize {
					t.Fatalf("%s: Go samples=%d, want %d", label, n, frameSize)
				}
				if len(pkt) == 2 {
					if goDec.FinalRange() != 0 {
						t.Fatalf("%s: Go final range %08x, want 0 for a one-byte payload", label, goDec.FinalRange())
					}
					refRange, err := refDec.FinalRange()
					if err != nil {
						t.Fatal(err)
					}
					if refRange != 0 {
						t.Fatalf("%s: libopus final range %08x, want 0", label, refRange)
					}
				}
				silkPLCExactCompare(t, label, goPCM[:n*channels], refPCM)
			}
		})
	}
}

// TestCGOSILKFECGapExact decodes in-band FEC for multi-frame packets whose
// LBRR mask leaves some subframes to the PLC, and requires the reconstruction
// and the following normal frames to be sample-exact against libopus.
func TestCGOSILKFECGapExact(t *testing.T) {
	t.Logf("libopus version: %s", cgoref.Version())
	const (
		rate     = 16000
		nPackets = 12
		lost     = 5
	)
	for _, packetMs := range []int{20, 40, 60} {
		packetMs := packetMs
		t.Run(silkRefPacketName(packetMs), func(t *testing.T) {
			frameSize := rate * packetMs / 1000
			packets := silkPLCExactEncode(t, rate, 1, packetMs, nPackets, true)

			goDec, err := opus.NewDecoder(rate, 1)
			if err != nil {
				t.Fatal(err)
			}
			refDec, err := cgoref.NewDecoder(rate, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer refDec.Close()

			gaps := 0
			for p := 0; p < nPackets; p++ {
				if p == lost {
					continue
				}
				goPCM := make([]int16, frameSize)
				var refPCM []int16
				var n int
				label := "packet " + itoa(p)
				if p == lost+1 {
					n, err = goDec.DecodeFEC(packets[p], goPCM)
					if err != nil {
						t.Fatalf("packet %d: Go DecodeFEC: %v", p, err)
					}
					refPCM, err = refDec.DecodeFEC(packets[p], frameSize)
					if err != nil {
						t.Fatalf("packet %d: libopus decode_fec: %v", p, err)
					}
					if n != frameSize {
						t.Fatalf("packet %d: Go FEC samples=%d, want %d", p, n, frameSize)
					}
					present, err := silkMonoLBRRPresent(packets[p], packetMs/20)
					if err != nil {
						t.Fatalf("parse LBRR mask: %v", err)
					}
					for _, ok := range present {
						if !ok {
							gaps++
						}
					}
					t.Logf("%dms: LBRR present per subframe %v", packetMs, present)
					silkPLCExactCompare(t, label+" (FEC reconstruction)", goPCM[:n], refPCM)
					goPCM = make([]int16, frameSize)
				}
				n, err = goDec.Decode(packets[p], goPCM)
				if err != nil {
					t.Fatalf("packet %d: Go Decode: %v", p, err)
				}
				refPCM, err = refDec.Decode(packets[p], frameSize)
				if err != nil {
					t.Fatalf("packet %d: libopus Decode: %v", p, err)
				}
				silkPLCExactCompare(t, label, goPCM[:n], refPCM)
			}
			if packetMs == 60 && gaps == 0 {
				t.Fatalf("60ms fixture no longer exercises an LBRR gap; adjust the fixture")
			}
		})
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
