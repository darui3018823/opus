package opus

import (
	"math"
	"math/rand"
	"testing"

	framing "github.com/darui3018823/opus/internal"
)

// genTone fills a mono []float64 of n samples with a sine at freq Hz.
func genTone(n int, freq, sampleRate float64) []float64 {
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
	}
	return out
}

// TestDetectSignalBandwidthTones checks that pure tones map to the narrowest
// framing bandwidth whose audio range covers them: a low tone narrows to NB while
// a high tone keeps fullband. With no history (prev<0) there is no hysteresis.
func TestDetectSignalBandwidthTones(t *testing.T) {
	const sr = 48000
	const n = 960
	cases := []struct {
		freq float64
		want int
	}{
		{200, framing.BandwidthNarrowband},
		{1000, framing.BandwidthNarrowband},
		{3000, framing.BandwidthNarrowband},
		{6000, framing.BandwidthWideband},
		{10000, framing.BandwidthSuperwideband},
		{16000, framing.BandwidthFullband},
	}
	for _, tc := range cases {
		pcm := genTone(n, tc.freq, sr)
		got := detectSignalBandwidth(pcm, 1, sr, -1)
		if got != tc.want {
			t.Errorf("%.0f Hz: detected bandwidth %d, want %d", tc.freq, got, tc.want)
		}
	}
}

// TestDetectSignalBandwidthBroadband checks that a broadband (white-noise) signal,
// which has energy up to Nyquist, detects as fullband and is therefore not
// narrowed at all.
func TestDetectSignalBandwidthBroadband(t *testing.T) {
	const sr = 48000
	const n = 960
	rng := rand.New(rand.NewSource(1))
	pcm := make([]float64, n)
	for i := range pcm {
		pcm[i] = rng.Float64()*2 - 1
	}
	if got := detectSignalBandwidth(pcm, 1, sr, -1); got != framing.BandwidthFullband {
		t.Errorf("white noise: detected bandwidth %d, want fullband", got)
	}
}

// TestDetectSignalBandwidthSilence checks that silence does not narrow the
// bandwidth (returns fullband; the CELT silence/DTX path handles silent frames).
func TestDetectSignalBandwidthSilence(t *testing.T) {
	pcm := make([]float64, 960)
	if got := detectSignalBandwidth(pcm, 1, 48000, -1); got != framing.BandwidthFullband {
		t.Errorf("silence: detected bandwidth %d, want fullband", got)
	}
}

func TestHybridSpectralSparsity(t *testing.T) {
	const (
		sr = 48000
		n  = 960
	)
	if !isSpectrallySparse(genTone(n, 1000, sr), 1) {
		t.Fatal("pure tone should be spectrally sparse")
	}
	if !isSpectrallySparse(strictSpeechLikeFrame(sr, 1, 0, n), 1) {
		t.Fatal("speech-like harmonics should be spectrally sparse")
	}

	lowpassNoise := make([]float64, n)
	for i := range lowpassNoise {
		for k := 3; k <= 67; k++ {
			phase := float64((k*37)%101) * 0.061
			lowpassNoise[i] += 0.01 * math.Sin(2*math.Pi*float64(k*i)/1024+phase)
		}
	}
	if isSpectrallySparse(lowpassNoise, 1) {
		t.Fatal("low-passed noise should not be spectrally sparse")
	}
}

// TestDetectBandwidthHysteresis verifies the asymmetric hysteresis in tierForFreq:
// a frequency just below a tier edge widens immediately from a narrower previous
// decision but holds the wider previous decision rather than narrowing.
func TestDetectBandwidthHysteresis(t *testing.T) {
	// 7900 Hz sits just under the WB edge (8000 Hz), within the hysteresis band
	// of the SWB edge it would otherwise drop from.
	const topHz = 7900

	// Rising: previously NB, now clearly WB-level -> widen to WB immediately.
	if got := tierForFreq(topHz, framing.BandwidthNarrowband); got != framing.BandwidthWideband {
		t.Errorf("rising from NB at %d Hz: got %d, want WB", topHz, got)
	}
	// Holding: previously SWB, 7900 Hz is within 90%% of the WB edge (7200..8000),
	// so it should hold SWB rather than narrowing to WB.
	if got := tierForFreq(topHz, framing.BandwidthSuperwideband); got != framing.BandwidthSuperwideband {
		t.Errorf("holding from SWB at %d Hz: got %d, want SWB (hysteresis)", topHz, got)
	}
	// Clearly below: 6000 Hz is well under the WB edge, so narrow to WB even from FB.
	if got := tierForFreq(6000, framing.BandwidthFullband); got != framing.BandwidthWideband {
		t.Errorf("narrowing from FB at 6000 Hz: got %d, want WB", got)
	}
}

// TestEncoderAutoBandwidthConfig confirms the full encoder narrows the emitted TOC
// config for a band-limited source: a 1 kHz tone at 48 kHz auto-selects narrowband
// (CELT configs 16-19) instead of fullband, while a broadband source stays
// fullband (configs 28-31). Forcing a bandwidth bypasses detection.
func TestEncoderAutoBandwidthConfig(t *testing.T) {
	const sr = 48000
	const frameSize = 960

	enc, err := NewEncoder(sr, 1, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	tone := genTone(frameSize, 1000, sr)
	pkt, err := enc.EncodeFloat(tone, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if config := int(pkt[0] >> 3); config < 16 || config > 19 {
		t.Errorf("1kHz tone: config %d, want narrowband CELT (16-19)", config)
	}

	encN, _ := NewEncoder(sr, 1, ApplicationAudio)
	rng := rand.New(rand.NewSource(2))
	noise := make([]float64, frameSize)
	for i := range noise {
		noise[i] = rng.Float64()*2 - 1
	}
	pktN, err := encN.EncodeFloat(noise, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if config := int(pktN[0] >> 3); config < 28 || config > 31 {
		t.Errorf("broadband: config %d, want fullband CELT (28-31)", config)
	}
}
