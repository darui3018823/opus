//go:build opusref

package opus

import (
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/celt"
	"github.com/darui3018823/opus/internal/cgoref"
	"github.com/darui3018823/opus/internal/entcode"
)

func TestTV01Packet0FrameRangesMatchLibopus(t *testing.T) {
	minimumSequentialExact := [...]int{1920, 1920, 1920}
	minimumIsolatedExact := [...]int{1920, 1920, 1920}

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
	refFloatDec, err := cgoref.NewDecoder(SampleRate48kHz, ChannelsStereo)
	if err != nil {
		t.Fatalf("cgoref.NewDecoder for float output: %v", err)
	}
	defer refFloatDec.Close()

	var goLast, refLast uint32
	for i, frame := range frames {
		probe := entcode.NewDecoder(frame)
		if probe.ECTell() == 1 {
			_ = probe.DecodeBitLogp(15)
		}
		pfPeriod, pfGain, pfTapset, pfEnabled := celt.DecodePostFilterParams(probe, len(frame)*8, 3)
		isTransient := probe.DecodeBitLogp(3)
		goFloat, err := goDec.Decode(frame)
		if err != nil {
			t.Fatalf("frame %d Go CELT decode: %v", i, err)
		}
		goPCM := make([]int16, len(goFloat))
		floatToInt16(goPCM, goFloat)
		goRange := goDec.LastFinalRange()

		singlePacket := make([]byte, 1, len(frame)+1)
		singlePacket[0] = toc &^ 3
		singlePacket = append(singlePacket, frame...)
		refPCM, err := refDec.Decode(singlePacket, FrameSize20ms)
		if err != nil {
			t.Fatalf("frame %d libopus decode: %v", i, err)
		}
		refRange, err := refDec.FinalRange()
		if err != nil {
			t.Fatalf("frame %d libopus final range: %v", i, err)
		}
		refFloat, err := refFloatDec.DecodeFloat(singlePacket, FrameSize20ms)
		if err != nil {
			t.Fatalf("frame %d libopus float decode: %v", i, err)
		}

		goLast = goRange
		refLast = refRange
		t.Logf("frame=%d bytes=%d postfilter=%v/%d/%.6f/%d transient=%v pitch=%d go=%08x libopus=%08x",
			i, len(frame), pfEnabled, pfPeriod, pfGain, pfTapset, isTransient, goDec.Pitch(), goRange, refRange)
		if goRange != refRange {
			t.Errorf("frame %d final range=%08x, libopus=%08x", i, goRange, refRange)
		}
		firstDiff := -1
		firstFloatDiff := -1
		exact := 0
		floatExact := 0
		maxDelta := 0
		floatAbsLSB := 0.0
		floatMaxLSB := 0.0
		floatMaxIndex := -1
		var dot, refEnergy float64
		for sample := range goPCM {
			goSample := float32(goFloat[sample])
			dot += float64(goSample) * float64(refFloat[sample])
			refEnergy += float64(refFloat[sample]) * float64(refFloat[sample])
			floatDeltaLSB := math.Abs(float64(goSample-refFloat[sample])) * 32768
			floatAbsLSB += floatDeltaLSB
			if floatDeltaLSB > floatMaxLSB {
				floatMaxLSB = floatDeltaLSB
				floatMaxIndex = sample
			}
			if goSample == refFloat[sample] {
				floatExact++
			} else if firstFloatDiff < 0 {
				firstFloatDiff = sample
			}
			delta := int(goPCM[sample]) - int(refPCM[sample])
			if delta == 0 {
				exact++
				continue
			}
			if firstDiff < 0 {
				firstDiff = sample
			}
			if delta < 0 {
				delta = -delta
			}
			if delta > maxDelta {
				maxDelta = delta
			}
		}
		t.Logf("frame=%d PCM exact=%d/%d firstDiff=%d maxDelta=%d", i, exact, len(goPCM), firstDiff, maxDelta)
		if exact < minimumSequentialExact[i] || maxDelta != 0 {
			t.Errorf("frame %d sequential PCM regressed: exact=%d/%d maxDelta=%d", i, exact, len(goPCM), maxDelta)
		}
		t.Logf("frame=%d float32 exact=%d/%d firstDiff=%d meanAbsLSB=%.6f maxAbsLSB=%.6f@%d",
			i, floatExact, len(goFloat), firstFloatDiff, floatAbsLSB/float64(len(goFloat)), floatMaxLSB, floatMaxIndex)
		scale := dot / refEnergy
		var scaledAbsLSB, scaledMaxLSB float64
		for sample := range goFloat {
			deltaLSB := math.Abs(float64(float32(goFloat[sample]))-scale*float64(refFloat[sample])) * 32768
			scaledAbsLSB += deltaLSB
			if deltaLSB > scaledMaxLSB {
				scaledMaxLSB = deltaLSB
			}
		}
		t.Logf("frame=%d fittedScale=%.12f residualMeanAbsLSB=%.6f residualMaxAbsLSB=%.6f",
			i, scale, scaledAbsLSB/float64(len(goFloat)), scaledMaxLSB)
		if firstDiff >= 0 {
			t.Logf("frame=%d sample=%d Go=%g (%08x) libopus=%g (%08x)", i, firstDiff,
				float32(goFloat[firstDiff]), math.Float32bits(float32(goFloat[firstDiff])),
				refFloat[firstDiff], math.Float32bits(refFloat[firstDiff]))
		}

		isolatedGo, err := celt.NewDecoderEx(FrameSize20ms, SampleRate48kHz, 21, ChannelsStereo)
		if err != nil {
			t.Fatalf("frame %d isolated Go decoder: %v", i, err)
		}
		isolatedFloat, err := isolatedGo.Decode(frame)
		if err != nil {
			t.Fatalf("frame %d isolated Go decode: %v", i, err)
		}
		isolatedPCM := make([]int16, len(isolatedFloat))
		floatToInt16(isolatedPCM, isolatedFloat)
		isolatedRef, err := cgoref.NewDecoder(SampleRate48kHz, ChannelsStereo)
		if err != nil {
			t.Fatalf("frame %d isolated libopus decoder: %v", i, err)
		}
		isolatedRefPCM, err := isolatedRef.Decode(singlePacket, FrameSize20ms)
		isolatedRef.Close()
		if err != nil {
			t.Fatalf("frame %d isolated libopus decode: %v", i, err)
		}
		isolatedExact := 0
		for sample := range isolatedPCM {
			if isolatedPCM[sample] == isolatedRefPCM[sample] {
				isolatedExact++
			}
		}
		t.Logf("frame=%d isolated PCM exact=%d/%d", i, isolatedExact, len(isolatedPCM))
		if isolatedExact < minimumIsolatedExact[i] {
			t.Errorf("frame %d isolated PCM regressed: exact=%d/%d", i, isolatedExact, len(isolatedPCM))
		}
		if i == 0 && firstDiff >= 0 {
			t.Fatalf("frame 0 unexpectedly differs first at sample %d", firstDiff)
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
