//go:build opusref

package opus

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"
	"testing"

	"github.com/darui3018823/opus/internal/cgoref"
)

// TestCELTMusicChordsMatchedBitrateReproducer preserves the deterministic
// stereo-chords cell that motivated post-audit Phase 1. The signal is generated
// in code so this diagnostic does not depend on the ignored real-corpus WAV.
func TestCELTMusicChordsMatchedBitrateReproducer(t *testing.T) {
	// Operating point: 48 kHz stereo, 20 ms, ApplicationAudio + SignalMusic,
	// complexity 5, VBR enabled and constrained (CVBR), zero packet loss.
	clip := phase1StereoChordsClip()
	const frameSize = 960 // 20 ms at 48 kHz

	for _, bitrate := range []int{24000, 32000, 48000, 64000} {
		t.Run(phase1BitrateName(bitrate), func(t *testing.T) {
			ownPackets, ownRanges, ownBytes := phase1EncodeOwnWithRanges(t, clip, bitrate)
			repeated, repeatedRanges, repeatedBytes := phase1EncodeOwnWithRanges(t, clip, bitrate)
			if ownBytes != repeatedBytes || !phase1PacketsEqual(ownPackets, repeated) || !phase1RangesEqual(ownRanges, repeatedRanges) {
				t.Fatal("Go encoding is not stable across repeated runs")
			}

			refPackets, refBytes, _, err := encodeRealCorpusRef(t, clip, "music", bitrate)
			if err != nil {
				t.Fatal(err)
			}
			matchedBitrate := realCorpusMatchedBitrateFor(ownBytes, clip.rate, frameSize, len(ownPackets))
			matchedPackets, matchedBytes, _, err := encodeRealCorpusRef(t, clip, "music", matchedBitrate)
			if err != nil {
				t.Fatal(err)
			}

			ownOut, crossDecodeSNR := phase1DecodeAndCheckRanges(t, ownPackets, ownRanges, clip)
			refOut := decodePacketSequenceWithLoss(t, refPackets, clip.rate, clip.channels, frameSize, 0)
			matchedOut := decodePacketSequenceWithLoss(t, matchedPackets, clip.rate, clip.channels, frameSize, 0)
			ownSNR, _, _, _ := opusSILKABAlignedSNR(clip.pcm, ownOut, frameSize)
			refSNR, _, _, _ := opusSILKABAlignedSNR(clip.pcm, refOut, frameSize)
			matchedSNR, _, _, _ := opusSILKABAlignedSNR(clip.pcm, matchedOut, frameSize)
			gap := matchedSNR - ownSNR
			ratio := float64(ownBytes) / float64(matchedBytes)
			ownConfigs := phase1PacketConfigs(t, ownPackets)
			refConfigs := phase1PacketConfigs(t, refPackets)
			matchedConfigs := phase1PacketConfigs(t, matchedPackets)
			ownHash := phase1PacketHash(ownPackets)

			t.Logf("rate=48000 channels=2 frame=20ms CVBR=true complexity=5 signal=music bitrate=%d frames=%d own=%dB ref=%dB matched_rate=%d matched=%dB ratio=%.6f SNR own=%.3f ref=%.3f matched=%.3f gap=%.3f dB cross_decode=%.3f dB final_range=ok TOC own=%v ref=%v matched=%v own_sha256=%x",
				bitrate, len(ownPackets), ownBytes, refBytes, matchedBitrate, matchedBytes, ratio,
				ownSNR, refSNR, matchedSNR, gap, crossDecodeSNR, ownConfigs, refConfigs, matchedConfigs, ownHash)

			if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0.95 || ratio > 1.05 {
				t.Fatalf("matched-byte ratio %.6f is not comparable", ratio)
			}
			for label, snr := range map[string]float64{"own": ownSNR, "ref": refSNR, "matched": matchedSNR} {
				if math.IsNaN(snr) || math.IsInf(snr, 0) {
					t.Fatalf("%s SNR is not finite: %v", label, snr)
				}
			}
			baselineBytes := map[int]int{24000: 2960, 32000: 3930, 48000: 5870, 64000: 7810}[bitrate]
			if ownBytes > baselineBytes*105/100 {
				t.Fatalf("own bytes %d exceed the 5%% budget over baseline %d", ownBytes, baselineBytes)
			}
			maxGap := map[int]float64{24000: 5.8, 32000: 5.1, 48000: 2, 64000: 2}[bitrate]
			if gap > maxGap {
				t.Fatalf("matched CELT/music gap %.3f dB exceeds %.1f dB regression limit", gap, maxGap)
			}
		})
	}
}

func phase1EncodeOwnWithRanges(t *testing.T, clip corpusClip, bitrate int) ([][]byte, []uint32, int) {
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

	const frameSize = 960
	stride := frameSize * clip.channels
	frames := len(clip.pcm) / stride
	packets := make([][]byte, 0, frames)
	ranges := make([]uint32, 0, frames)
	totalBytes := 0
	for frame := 0; frame < frames; frame++ {
		packet, err := enc.EncodeFloat(clip.pcm[frame*stride:(frame+1)*stride], frameSize)
		if err != nil {
			t.Fatalf("frame %d encode: %v", frame, err)
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

func phase1StereoChordsClip() corpusClip {
	const (
		rate     = 48000
		channels = 2
		seconds  = 1
	)
	pcm := make([]float64, rate*seconds*channels)
	for i := 0; i < rate*seconds; i++ {
		t := float64(i) / rate
		// FFmpeg's sine source uses a 1/8 peak before the volume filters in
		// scripts/fetch_real_corpus.ps1. Quantize to signed 16-bit like the WAV.
		mono := (0.05*math.Sin(2*math.Pi*220*t) +
			0.04*math.Sin(2*math.Pi*277.18*t) +
			0.03*math.Sin(2*math.Pi*329.63*t)) / 8
		left := math.Round(mono*32768) / 32768
		right := math.Round(0.7*mono*32768) / 32768
		pcm[2*i] = left
		pcm[2*i+1] = right
	}
	return corpusClip{rate: rate, channels: channels, pcm: pcm}
}

func phase1PacketsEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func phase1RangesEqual(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func phase1PacketConfigs(t *testing.T, packets [][]byte) []int {
	t.Helper()
	var configs []int
	for i, packet := range packets {
		mode, err := PacketGetMode(packet)
		if err != nil {
			t.Fatalf("packet %d mode: %v", i, err)
		}
		if mode != ModeCELTOnly {
			t.Fatalf("packet %d mode=%d, want CELT-only", i, mode)
		}
		cfg, err := PacketGetConfig(packet)
		if err != nil {
			t.Fatalf("packet %d config: %v", i, err)
		}
		if len(configs) == 0 || configs[len(configs)-1] != cfg {
			configs = append(configs, cfg)
		}
	}
	return configs
}

func phase1PacketHash(packets [][]byte) [32]byte {
	h := sha256.New()
	for _, packet := range packets {
		_, _ = h.Write(packet)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func phase1BitrateName(bitrate int) string {
	return fmt.Sprintf("%dk", bitrate/1000)
}
