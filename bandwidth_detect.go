package opus

import (
	"math"

	framing "github.com/darui3018823/opus/internal"
	"github.com/darui3018823/opus/internal/dsp"
)

// bandwidthTiers lists the CELT coded-bandwidth tiers in ascending order with the
// audio-frequency upper edge (Hz) each one covers. The edges match the CELT
// end-band frequencies used by the encoder/decoder (EBands48000[end]*200 Hz for
// end = 13/17/19/21) and, equivalently, the RFC 6716 NB/WB/SWB/FB audio bandwidth
// definitions (4/8/12/20 kHz). A signal whose highest active frequency falls at or
// below a tier's edge can be coded in that tier without discarding real content.
var bandwidthTiers = []struct {
	edgeHz float64
	bw     int
}{
	{4000, framing.BandwidthNarrowband},
	{8000, framing.BandwidthWideband},
	{12000, framing.BandwidthSuperwideband},
	{20000, framing.BandwidthFullband},
}

// bwDetectThreshold is the per-bin power threshold, relative to the strongest bin,
// above which a frequency is treated as carrying real signal energy (-50 dB).
// Weaker bins are assumed to be noise or analysis-window leakage and do not extend
// the detected bandwidth. A Hann window keeps a strong low tone's leakage well
// below this level beyond ~one octave, so pure low tones detect as narrowband.
const bwDetectThreshold = 1e-5 // -50 dB

// bwDetectHysteresis guards the decision near a tier boundary: the encoder widens
// the bandwidth immediately when new high-frequency energy appears (so HF is never
// clipped), but only narrows to a lower tier once the active top frequency drops
// comfortably below that tier's edge (to this fraction of it). This keeps the
// per-packet decision from flapping when a signal hovers right at a boundary.
const bwDetectHysteresis = 0.9

// hybridSparseBinLimit is the maximum effective number of occupied FFT bins for
// a signal to be treated as tonal/harmonic by hybrid mode selection. The
// participation ratio is close to 2 for a Hann-windowed pure tone and remains
// small for a few harmonics, while low-passed noise occupies tens or hundreds
// of effective bins.
const hybridSparseBinLimit = 24

// detectSignalBandwidth analyses a block of interleaved PCM at the caller's sample
// rate and returns the narrowest internal framing bandwidth (framing.Bandwidth*:
// NB/WB/SWB/FB) whose audio-frequency range still contains the signal's active
// high-frequency energy. It operates purely on the input samples (mono downmix,
// Hann-windowed FFT) and is independent of the resampler and CELT encoder state,
// so it can run before encoding to choose a single bandwidth for the whole packet.
//
// The decoder reconstructs whatever bandwidth is signalled, so the result is only
// ever used to narrow the config-driven selection (never to widen it): spending no
// bits on bands the source has no energy in. prev is the previously detected
// framing bandwidth (or a negative value if there is no history) and drives a
// small hysteresis margin via tierForFreq.
func detectSignalBandwidth(pcm []float64, channels, sampleRate, prev int) int {
	bw, _ := detectSignalBandwidthAndSparsity(pcm, channels, sampleRate, prev)
	return bw
}

// detectSignalBandwidthAndSparsity performs the shared FFT analysis needed by
// hybrid mode selection and returns both the detected bandwidth and whether the
// spectrum is tonal/harmonic.
func detectSignalBandwidthAndSparsity(pcm []float64, channels, sampleRate, prev int) (int, bool) {
	power, nfft, ok := signalSpectrumPower(pcm, channels)
	if !ok {
		return framing.BandwidthFullband, false
	}

	// Per-bin power, ignoring DC (bin 0) so a constant offset cannot dominate.
	var emax, sum, sumSquares float64
	for k := 1; k < len(power); k++ {
		sum += power[k]
		sumSquares += power[k] * power[k]
		if power[k] > emax {
			emax = power[k]
		}
	}
	if emax == 0 {
		// Silence: nothing to narrow (the CELT silence/DTX path handles it anyway).
		return framing.BandwidthFullband, false
	}
	sparse := sumSquares > 0 && sum*sum/sumSquares <= hybridSparseBinLimit

	// Highest bin whose power exceeds the noise/leakage threshold.
	thresh := emax * bwDetectThreshold
	topBin := 0
	for k := len(power) - 1; k >= 1; k-- {
		if power[k] > thresh {
			topBin = k
			break
		}
	}
	topHz := float64(topBin) * float64(sampleRate) / float64(nfft)

	return tierForFreq(topHz, prev), sparse
}

// isSpectrallySparse reports whether the signal energy is concentrated in a
// small number of FFT bins. Hybrid mode uses this to distinguish steady
// voiced/harmonic input from genuinely low-bandwidth broadband input: the
// former still benefits from retaining the SILK low band and must not fall out
// of hybrid merely because its highest harmonic is below 4 kHz.
func isSpectrallySparse(pcm []float64, channels int) bool {
	power, _, ok := signalSpectrumPower(pcm, channels)
	if !ok {
		return false
	}
	var sum, sumSquares float64
	for k := 1; k < len(power); k++ {
		sum += power[k]
		sumSquares += power[k] * power[k]
	}
	if sum == 0 || sumSquares == 0 {
		return false
	}
	effectiveBins := sum * sum / sumSquares
	return effectiveBins <= hybridSparseBinLimit
}

// signalSpectrumPower returns the Hann-windowed mono-downmix power spectrum and
// FFT size shared by bandwidth and hybrid-tonality analysis.
func signalSpectrumPower(pcm []float64, channels int) ([]float64, int, bool) {
	if channels <= 0 {
		return nil, 0, false
	}
	n := len(pcm) / channels
	if n <= 1 {
		return nil, 0, false
	}

	// Mono downmix for the analysis.
	mono := make([]float64, n)
	if channels == 1 {
		copy(mono, pcm[:n])
	} else {
		for i := 0; i < n; i++ {
			var s float64
			for c := 0; c < channels; c++ {
				s += pcm[i*channels+c]
			}
			mono[i] = s / float64(channels)
		}
	}

	// Hann window so a strong low tone does not leak enough into high bins to look
	// like real high-frequency content, then zero-pad to a power-of-two FFT size.
	for i := 0; i < n; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		mono[i] *= w
	}
	nfft := dsp.NextPowerOf2(n)
	buf := make([]float64, nfft)
	copy(buf, mono)

	spec, err := dsp.RealFFT(buf)
	if err != nil {
		return nil, 0, false
	}

	power := make([]float64, len(spec))
	for k := 1; k < len(spec); k++ {
		p := spec[k].Real*spec[k].Real + spec[k].Imag*spec[k].Imag
		power[k] = p
	}
	return power, nfft, true
}

// tierForFreq maps an active top frequency (Hz) to a framing bandwidth, applying
// hysteresis relative to the previously detected bandwidth prev (negative = none).
func tierForFreq(topHz float64, prev int) int {
	// Narrowest tier whose edge still covers the active top frequency.
	idx := len(bandwidthTiers) - 1
	for i, t := range bandwidthTiers {
		if topHz <= t.edgeHz {
			idx = i
			break
		}
	}
	raw := bandwidthTiers[idx].bw
	if prev < 0 {
		return raw
	}

	// Rise immediately; only narrow when comfortably below the lower tier's edge.
	prevIdx := tierIndex(prev)
	if idx < prevIdx && topHz > bandwidthTiers[idx].edgeHz*bwDetectHysteresis {
		return bandwidthTiers[prevIdx].bw
	}
	return raw
}

// tierIndex returns the index of a framing bandwidth within bandwidthTiers.
func tierIndex(bw int) int {
	for i, t := range bandwidthTiers {
		if t.bw == bw {
			return i
		}
	}
	return len(bandwidthTiers) - 1
}
