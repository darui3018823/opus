//go:build opusref

package opus

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"
	"testing"
)

// TestCELTMusicChordsMatchedBitrateReproducer preserves the deterministic
// stereo-chords cell that motivated post-audit Phase 1. The signal is generated
// in code so this diagnostic does not depend on the ignored real-corpus WAV.
func TestCELTMusicChordsMatchedBitrateReproducer(t *testing.T) {
	// Operating point: 48 kHz stereo, 20 ms, ApplicationAudio + SignalMusic,
	// complexity 5, VBR enabled and constrained (CVBR), zero packet loss.
	clip := phase1StereoChordsClip()
	const frameSize = 960 // 20 ms at 48 kHz

	for _, bitrate := range []int{24000, 48000, 64000} {
		t.Run(phase1BitrateName(bitrate), func(t *testing.T) {
			ownPackets, ownBytes, ownFirst, err := encodeRealCorpusOwn(clip, "music", bitrate)
			if err != nil {
				t.Fatal(err)
			}
			repeated, repeatedBytes, repeatedFirst, err := encodeRealCorpusOwn(clip, "music", bitrate)
			if err != nil {
				t.Fatal(err)
			}
			if ownBytes != repeatedBytes || ownFirst != repeatedFirst || !phase1PacketsEqual(ownPackets, repeated) {
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

			ownOut := decodePacketSequenceWithLoss(t, ownPackets, clip.rate, clip.channels, frameSize, 0)
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

			t.Logf("rate=48000 channels=2 frame=20ms CVBR=true complexity=5 signal=music bitrate=%d frames=%d own=%dB ref=%dB matched_rate=%d matched=%dB ratio=%.6f SNR own=%.3f ref=%.3f matched=%.3f gap=%.3f dB TOC own=%v ref=%v matched=%v own_sha256=%x",
				bitrate, len(ownPackets), ownBytes, refBytes, matchedBitrate, matchedBytes, ratio,
				ownSNR, refSNR, matchedSNR, gap, ownConfigs, refConfigs, matchedConfigs, ownHash)

			if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0.95 || ratio > 1.05 {
				t.Fatalf("matched-byte ratio %.6f is not comparable", ratio)
			}
			for label, snr := range map[string]float64{"own": ownSNR, "ref": refSNR, "matched": matchedSNR} {
				if math.IsNaN(snr) || math.IsInf(snr, 0) {
					t.Fatalf("%s SNR is not finite: %v", label, snr)
				}
			}
			// Slice 1-1 baseline guard. Slice 1-5 replaces this with the adopted
			// non-regression threshold after the root-cause decision.
			if gap < 6 {
				t.Fatalf("baseline gap %.3f dB no longer reproduces the material CELT/music loss", gap)
			}
		})
	}
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
