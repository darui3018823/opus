# CELT Stereo Tonality-Slope Iteration

Date: 2026-07-21
Status: Complete — adopted

## Objective

Measure one isolated allocation hypothesis for the remaining 24/32 kbit/s
stereo-chords quality gap, without changing packet byte totals or combining the
trial with stereo-saving or broader dynamic-allocation policy.

## Baseline

The code-generated matched-bitrate reproducer reported a 5.886 dB gap at
24 kbit/s. The saved full-corpus baseline reported loss-0 stereo-chords gaps of
5.61 dB at 24 kbit/s and 5.49 dB at 32 kbit/s.

Representative 24 kbit/s encoder analysis used 59-byte frames, intensity band
9, coded band 13, allocation trim 5, and no TF boost. A forced trim reduction
of two steps worsened the focused gap to 6.898 dB; a forced increase of one or
two steps improved it to 5.608/5.311 dB. This established direction only; the
constant overrides were removed.

## Adopted Change

`spectralTonalitySlope` estimates whether active, spectrally concentrated CELT
bands are weighted toward low or high frequencies. It uses the existing
normalised MDCT spectrum, gates bands more than eight log2-amplitude units below
the peak, and applies the libopus `2*(tonality_slope+.05)` allocation-trim form.

The consumer is limited to CELT-only stereo (`start == 0`) in this iteration.
An initial mono+stereo trial improved the target but changed loss-0
onset/source totals by 2/3 bytes. A first stereo restriction also changed the
hybrid-stereo packet/final-range digest; excluding the hybrid high-band path
restored all four predictive digests. The final CELT-only stereo version keeps
the target improvement while leaving every class's loss-0 byte total unchanged.

## Results

The focused code-generated test now covers 24, 32, 48, and 64 kbit/s. It keeps
packet determinism, matched bytes, Go/libopus cross-decode, TOC mode, and every
packet's encoder/decoder final range checked.

| bitrate | prior focused gap | adopted focused gap | own bytes |
|---:|---:|---:|---:|
| 24 kbit/s | 5.886 dB | 5.726 dB | 2,960 |
| 32 kbit/s | about 5.21 dB in the audit record | 4.948 dB | 3,930 |

The final full corpus completed 140/140 cells. Every class's loss-0 own-byte
total was unchanged. The saved-baseline to adopted loss-0 stereo-chords changes
were 5.61 to 5.55 dB at 24 kbit/s and 5.49 to 5.40 dB at 32 kbit/s. The largest
observed regression was 0.01 dB in a 5% loss cell, below the 0.3 dB adoption
gate; several other chord cells improved by 0.02-0.24 dB.

## Verification

```text
go test -count=1 ./internal/celt
go test -count=1 -tags opusref -run '^TestCELTMusicChordsMatchedBitrateReproducer$' .
go test -count=1 -run '^TestPerfPredictivePacketRegression$' .
go test -count=1 ./...
go vet ./...
OPUS_REAL_CORPUS=1 OPUS_REAL_CORPUS_OUT=testdata/phase5_tonality_slope_celt_only.csv \
  go test -count=1 -tags opusref -run '^TestOpusRealCorpusMatchedBitrateScoreboard$' -v .
```

The generated CSV remains ignored local evidence. Stereo saving and broader
dynamic allocation remain separate future hypotheses.
