package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/entcode"
	"github.com/darui3018823/opus/internal/silk"
)

// TestTV10HybridRange checks the per-Opus-frame final range of selected tv10
// hybrid packets (multi-frame, high bitrate — NOT covered by TestHybridRangeExact)
// against the opus_demo stored value, to confirm whether our hybrid entropy path
// is bit-exact for these packets. The .bit stores the final range after the LAST
// Opus frame of the packet.
func TestTV10HybridRange(t *testing.T) {
	pkts := readOpusDemoPackets(t, "testvector10.bit")
	for _, idx := range []int{985, 986, 987} {
		pk := pkts[idx]
		toc := pk.packet[0]
		config := int((toc >> 3) & 0x1f)
		stereo := (toc >> 2) & 1
		code := toc & 3
		channels := 1
		if stereo == 1 {
			channels = 2
		}
		frameMs := 20
		if config&1 == 0 {
			frameMs = 10
		}
		celtEnd := 21
		if config < 14 {
			celtEnd = 19
		}
		fs := 960
		if frameMs == 10 {
			fs = 480
		}

		streams, err := splitOpusFrames(pk.packet[1:], int(code))
		if err != nil {
			t.Logf("pkt%d split err: %v", idx, err)
			continue
		}

		sd, _ := silk.NewDecoderWithFrameMs(16000, channels, frameMs)
		cd, _ := celt.NewDecoderEx(fs, 48000, 21, channels)

		var lastRng uint32
		var redCount int
		for _, stream := range streams {
			dec := entcode.NewDecoder(stream)
			if _, err := sd.DecodeMultiWithDecoder(dec, 1); err != nil {
				t.Logf("pkt%d SILK err: %v", idx, err)
			}
			redundancy := false
			celtToSilk := false
			redundancyBytes := 0
			celtLen := len(stream)
			if dec.ECTell()+37 <= len(stream)*8 {
				redundancy = dec.DecodeBitLogp(12)
			}
			if redundancy {
				redCount++
				celtToSilk = dec.DecodeBitLogp(1)
				redundancyBytes = int(dec.DecodeUint(256)) + 2
				celtLen = len(stream) - redundancyBytes
				if celtLen*8 < dec.ECTell() {
					celtLen = len(stream)
					redundancyBytes = 0
					redundancy = false
				} else {
					dec.ShrinkStorage(redundancyBytes)
				}
			}
			if _, err := cd.DecodeHybrid(dec, celtLen, 17, celtEnd); err != nil {
				t.Logf("pkt%d CELT err: %v", idx, err)
			}
			mainRng := dec.GetRng()
			var redRng uint32
			if redundancy && !celtToSilk && redundancyBytes >= 2 && celtLen+redundancyBytes <= len(stream) {
				rd, _ := celt.NewDecoderEx(240, 48000, 21, channels)
				if _, err := rd.Decode(stream[celtLen : celtLen+redundancyBytes]); err != nil {
					t.Logf("pkt%d redundant err: %v", idx, err)
				}
				redRng = rd.LastFinalRange()
			}
			lastRng = mainRng ^ redRng
			_ = celtToSilk
		}
		t.Logf("pkt%d cfg=%d code=%d frames=%d redundancy=%d lastRng=%08x want=%08x match=%v",
			idx, config, code, len(streams), redCount, lastRng, pk.finalRange, lastRng == pk.finalRange)
		if lastRng != pk.finalRange {
			t.Errorf("pkt%d hybrid+redundancy final range %08x != want %08x", idx, lastRng, pk.finalRange)
		}
	}
}
