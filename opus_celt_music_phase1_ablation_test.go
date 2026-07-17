//go:build opusref

package opus

import (
	"fmt"
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

func TestCELTMusicChordsPhase1Ablations(t *testing.T) {
	if testing.Short() {
		t.Skip("Phase 1 libopus ablations")
	}
	clip := phase1StereoChordsClip()
	const bitrate = 48000
	variants := []struct {
		name           string
		ablation       string
		forceBandwidth int
	}{
		{name: "baseline"},
		{name: "tf_disabled", ablation: "disable-tf"},
		{name: "short_blocks", ablation: "force-short-blocks"},
		{name: "dynalloc_disabled", ablation: "disable-dynalloc"},
		{name: "trim_neutral", ablation: "neutral-trim"},
		{name: "dynalloc_trim_neutral", ablation: "neutral-dynalloc-trim"},
		{name: "stereo_dual_no_intensity", ablation: "force-dual-no-intensity"},
		{name: "bandwidth_fullband", forceBandwidth: BandwidthFullband},
		{name: "cvbr_blend", ablation: "cvbr-blend"},
		{name: "rate_target_floor_50", ablation: "rate-target-floor-50"},
		{name: "rate_target_floor_625", ablation: "rate-target-floor-625"},
		{name: "rate_target_floor_667", ablation: "rate-target-floor-667"},
		{name: "rate_target_floor_75", ablation: "rate-target-floor-75"},
		{name: "rate_target_floor_875", ablation: "rate-target-floor-875"},
		{name: "full_rate_target", ablation: "full-rate-target"},
	}

	t.Log("variant,own_bytes,matched_bitrate,matched_bytes,byte_ratio,own_snr,matched_snr,gap_db,toc,cross_decode_snr,final_range")
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			t.Setenv("OPUS_PHASE1_CELT_ABLATION", variant.ablation)
			packets, ranges, ownBytes := phase1EncodeOwnVariant(t, clip, bitrate, variant.forceBandwidth)
			ownOut, crossDecodeSNR := phase1DecodeAndCheckRanges(t, packets, ranges, clip)
			ownSNR, _, _, _ := opusSILKABAlignedSNR(clip.pcm, ownOut, 960)

			matchedBitrate := realCorpusMatchedBitrateFor(ownBytes, clip.rate, 960, len(packets))
			matchedPackets, matchedBytes, _, err := encodeRealCorpusRef(t, clip, "music", matchedBitrate)
			if err != nil {
				t.Fatal(err)
			}
			matchedOut := decodePacketSequenceWithLoss(t, matchedPackets, clip.rate, clip.channels, 960, 0)
			matchedSNR, _, _, _ := opusSILKABAlignedSNR(clip.pcm, matchedOut, 960)
			ratio := float64(ownBytes) / float64(matchedBytes)
			gap := matchedSNR - ownSNR
			if math.IsNaN(gap) || math.IsInf(gap, 0) || ratio < 0.90 || ratio > 1.10 {
				t.Fatalf("invalid comparison: ratio=%.6f gap=%v", ratio, gap)
			}
			t.Logf("%s,%d,%d,%d,%.6f,%.3f,%.3f,%.3f,%v,%.3f,ok sizes=%s",
				variant.name, ownBytes, matchedBitrate, matchedBytes, ratio, ownSNR, matchedSNR, gap,
				phase1PacketConfigs(t, packets), crossDecodeSNR, phase1PacketSizeRuns(packets))
		})
	}
}

func phase1PacketSizeRuns(packets [][]byte) string {
	if len(packets) == 0 {
		return ""
	}
	result := ""
	start := 0
	for i := 1; i <= len(packets); i++ {
		if i < len(packets) && len(packets[i]) == len(packets[start]) {
			continue
		}
		if result != "" {
			result += " "
		}
		result += fmt.Sprintf("%dB*%d", len(packets[start]), i-start)
		start = i
	}
	return result
}

func phase1EncodeOwnVariant(t *testing.T, clip corpusClip, bitrate, forceBandwidth int) ([][]byte, []uint32, int) {
	t.Helper()
	enc, err := NewEncoder(clip.rate, clip.channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetComplexity(5); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	enc.SetSignalType(SignalMusic)
	if forceBandwidth != 0 {
		if err := enc.SetBandwidth(forceBandwidth); err != nil {
			t.Fatal(err)
		}
	}

	const frameSize = 960
	stride := frameSize * clip.channels
	frames := len(clip.pcm) / stride
	packets := make([][]byte, 0, frames)
	ranges := make([]uint32, 0, frames)
	totalBytes := 0
	for frame := 0; frame < frames; frame++ {
		packet, err := enc.EncodeFloat(clip.pcm[frame*stride:(frame+1)*stride], frameSize)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		packets = append(packets, packet)
		ranges = append(ranges, enc.FinalRange())
		totalBytes += len(packet)
	}
	return packets, ranges, totalBytes
}

func phase1DecodeAndCheckRanges(t *testing.T, packets [][]byte, ranges []uint32, clip corpusClip) ([]float64, float64) {
	t.Helper()
	goDec, err := NewDecoder(clip.rate, clip.channels)
	if err != nil {
		t.Fatal(err)
	}
	refDec, err := cgoref.NewDecoder(clip.rate, clip.channels)
	if err != nil {
		t.Fatal(err)
	}
	defer refDec.Close()

	goOut := make([]float64, 0, len(packets)*960*clip.channels)
	refOut := make([]float64, 0, cap(goOut))
	for i, packet := range packets {
		pcm, err := goDec.DecodeFloat(packet)
		if err != nil {
			t.Fatalf("Go decode frame %d: %v", i, err)
		}
		refPCM, err := refDec.DecodeFloat(packet, 960)
		if err != nil {
			t.Fatalf("libopus decode frame %d: %v", i, err)
		}
		refRange, err := refDec.FinalRange()
		if err != nil {
			t.Fatal(err)
		}
		if got := goDec.FinalRange(); got != ranges[i] || refRange != ranges[i] {
			t.Fatalf("frame %d final range encoder=%08x Go=%08x libopus=%08x", i, ranges[i], got, refRange)
		}
		goOut = append(goOut, pcm...)
		for _, sample := range refPCM {
			refOut = append(refOut, float64(sample))
		}
	}
	crossSNR, _, _, _ := opusSILKABAlignedSNR(goOut, refOut, 0)
	if math.IsNaN(crossSNR) || math.IsInf(crossSNR, 0) || crossSNR < 60 {
		t.Fatalf("Go/libopus cross-decode SNR %.3f dB", crossSNR)
	}
	return goOut, crossSNR
}
