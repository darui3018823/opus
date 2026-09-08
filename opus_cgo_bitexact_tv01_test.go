//go:build opusref

package opus

import (
	"testing"

	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/cgoref"
)

func TestTV01Packet0FrameRangesMatchLibopus(t *testing.T) {
	packet := readOpusDemoPackets(t, "testvector01.bit")[0]
	toc := packet.packet[0]
	config := int(toc >> 3)
	stereo := toc&4 != 0
	code := int(toc & 3)
	if config != 31 || !stereo || code != 3 {
		t.Fatalf("unexpected TOC: config=%d stereo=%v code=%d", config, stereo, code)
	}
	frames, err := splitOpusFrames(packet.packet[1:], code)
	if err != nil {
		t.Fatalf("split packet: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("frame count=%d, want 3", len(frames))
	}

	goDec, err := celt.NewDecoderEx(FrameSize20ms, SampleRate48kHz, 21, ChannelsStereo)
	if err != nil {
		t.Fatalf("celt.NewDecoderEx: %v", err)
	}
	refDec, err := cgoref.NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("cgoref.NewDecoder: %v", err)
	}
	defer refDec.Close()

	var goLast, refLast uint32
	for i, frame := range frames {
		if _, err := goDec.Decode(frame); err != nil {
			t.Fatalf("frame %d Go CELT decode: %v", i, err)
		}
		goRange := goDec.LastFinalRange()

		singlePacket := make([]byte, 1, len(frame)+1)
		singlePacket[0] = toc &^ 3
		singlePacket = append(singlePacket, frame...)
		if _, err := refDec.Decode(singlePacket, FrameSize20ms); err != nil {
			t.Fatalf("frame %d libopus decode: %v", i, err)
		}
		refRange, err := refDec.FinalRange()
		if err != nil {
			t.Fatalf("frame %d libopus final range: %v", i, err)
		}

		goLast = goRange
		refLast = refRange
		t.Logf("frame=%d bytes=%d go=%08x libopus=%08x", i, len(frame), goRange, refRange)
		if goRange != refRange {
			t.Errorf("frame %d final range=%08x, libopus=%08x", i, goRange, refRange)
		}
	}

	if refLast != packet.finalRange {
		t.Fatalf("libopus last frame range=%08x, bitstream=%08x", refLast, packet.finalRange)
	}
	if goLast != packet.finalRange {
		t.Fatalf("Go last frame range=%08x, bitstream=%08x", goLast, packet.finalRange)
	}

	packetDec, err := NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]int16, MaxFrameSize*ChannelsStereo)
	if _, err := packetDec.Decode(packet.packet, pcm); err != nil {
		t.Fatalf("Decode packet: %v", err)
	}
	if got := packetDec.FinalRange(); got != packet.finalRange {
		t.Fatalf("packet final range=%08x, want last frame range=%08x", got, packet.finalRange)
	}
}
