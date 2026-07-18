# Real Corpus Matched-Bitrate Scoreboard

Last reviewed: 2026-07-17

This diagnostic harness measures the Go encoder against libopus on local WAV
corpus clips at matched bitrate. It is intentionally opt-in because
`testdata/` is git-ignored and the corpus is not part of CI.

## Prepare Corpus

```powershell
pwsh scripts/fetch_real_corpus.ps1
```

The script downloads a Creative Commons speech seed from
`voxserv/audio_quality_testing_samples` and, when `ffmpeg` is available, writes
short 48 kHz WAV clips under:

- `testdata/real_corpus/clean_speech`
- `testdata/real_corpus/noisy_speech`
- `testdata/real_corpus/onset_plosive`
- `testdata/real_corpus/stereo_speech`
- `testdata/real_corpus/mixed`
- `testdata/real_corpus/music`

Additional 16-bit PCM or float32 WAV files can be dropped anywhere under
`testdata/real_corpus/`. The test scans recursively.

## Run Scoreboard

```powershell
$env:OPUS_REAL_CORPUS = "1"
go test -count=1 -tags opusref -run TestOpusRealCorpusMatchedBitrateScoreboard -v .
```

Optional environment variables:

- `OPUS_REAL_CORPUS_OUT`: CSV output path. Default:
  `testdata/real_corpus_scoreboard.csv`.
- `OPUS_REAL_CORPUS_MAX_SECONDS`: max seconds read from each clip. Default: `6`.
- `OPUS_REAL_CORPUS_CLASSES`: comma-separated corpus classes to include. Empty
  runs every discovered class.
- `OPUS_REAL_CORPUS_BITRATES`: comma-separated bitrates in bit/s. Default:
  `16000,24000,32000,48000,64000`.
- `OPUS_REAL_CORPUS_FORCE_BANDWIDTH`: force the Go encoder to `nb`, `mb`, `wb`,
  `swb`, or `fb` while leaving libopus automatic. Empty or `auto` preserves the
  normal comparison. This is an ablation control, not a fair default scoreboard
  condition.

The CSV columns include:

- cell status: `status`, `error`
- clip metadata: `file`, `class`, `rate`, `channels`, `frames`
- operating point: `bitrate`, `loss_percent`
- byte accounting: `own_bytes`, `libopus_bytes`, `matched_bitrate`,
  `matched_bytes`, `ratio_bytes`, `ratio_bytes_matched`
- quality: `own_snr_db`, `libopus_snr_db`, `matched_snr_db`,
  `gap_snr_matched_db`
- first TOC config: `own_cfg`, `libopus_cfg`, `matched_cfg`

`gap_snr_matched_db = matched_snr_db - own_snr_db`; positive values mean the Go
encoder is behind libopus at comparable bytes. `ratio_bytes_matched` should stay
near `1.0`; if it does not, treat the cell as a rate-control mismatch before
using it to justify encoder policy changes.

## Scope

This harness is diagnostic only. It should be used before Phase D policy
changes, but it should not become a mandatory unit test while its corpus lives
under ignored `testdata/`.

## Baseline (2026-07-15)

First full run on the default fetched corpus (134 cells, bitrates
16/24/32/48/64 kbps, loss 0-20%). The CSV itself is git-ignored, so the
aggregate is recorded here as the Phase D-2 starting point.

Cell status: 132 ok, 2 `own_encode_error` — both
`hybrid frame 0 exceeds target: 121-123 > 120 bytes` on 48 kHz mono hybrid
cells. The hybrid target-size clamp rejects the frame instead of shrinking
it; fix before or as the first item of Phase D-2.

`gap_snr_matched_db` by class (positive = Go behind libopus at matched bytes):

| class | n | avg | min | max |
|---|---:|---:|---:|---:|
| clean_speech | 20 | +0.08 | 0.00 | +1.50 |
| noisy_speech | 20 | -0.21 | -1.41 | +0.28 |
| stereo_speech | 20 | +0.02 | -0.00 | +0.42 |
| onset_plosive | 16 | +0.13 | -1.99 | +3.50 |
| source | 16 | -0.07 | -1.19 | +1.30 |
| mixed | 20 | -1.72 | -11.72 | +5.64 |
| music | 20 | -1.67 | -9.26 | +9.69 |

Reading: speech classes are effectively at parity. The large losses are
concentrated in one synthetic stereo-chords music clip (+7.7 to +9.7 dB at
24-64 kbps with fair byte ratios 0.89-0.99, CELT music path) and one
mixed speech+tone mono clip at low bitrate (+5.6 dB at 16 kbps). These are
CELT/music cells, not SILK voice cells, so they are out of scope for the
Phase D-2 SILK/hybrid gates and belong to the deprioritized CELT parity track.

Caveat: `ratio_bytes_matched` averages 1.40 (max 2.54) across all cells even
though the worst cells sit near 1.0. Cells with a ratio far from 1.0 mean the
matched-bitrate search could not bring libopus close to our byte count
(mostly loss>0 cells with different FEC overhead); ignore their gap values
when judging policy changes.

## D-2 Iteration 0 REDO (2026-07-15)

Branch `codex/d2-hybrid-target-clamp` now fixes the baseline hybrid CVBR
encode-size failure at the CELT allocation boundary. Non-redundant hybrid VBR
starts with the RFC frame ceiling; after CELT writes its pre-allocation symbols,
it computes the target and `min_allowed`, applies the existing high-band
activity calibration, shrinks the shared entropy coder, and then performs
allocation. The top-level encoder no longer pre-shrinks to
`hybridAdaptiveTargetBytes()` and no longer accepts a post-allocation spill.

Two earlier variants were rejected. Raising the target before CELT encoding
collapsed libopus cross-decode SNR to about 0.1 dB. Accepting the flushed size
after allocation decoded similarly in Go and libopus, but encoder and decoder
final ranges diverged on the overshoot frame because they used different
allocation budgets. `TestHybridCVBROnsetFinalRange` now checks six consecutive
frames under normal tags, while `TestHybridCVBROnsetLibopusConsistency` retains
the libopus 1.6.1 cross-decode guard.

Full local run:

```powershell
$env:OPUS_REAL_CORPUS = "1"
$env:OPUS_REAL_CORPUS_OUT = "testdata/real_corpus_scoreboard_d2_iter0_redo.csv"
go test -count=1 -tags opusref -run TestOpusRealCorpusMatchedBitrateScoreboard -v .
```

Result: 140/140 cells `status=ok`. The two baseline failed bitrate cells now
expand to their four packet-loss rows each, so the total row count increases
from 134 to 140.

`gap_snr_matched_db` and own-byte totals relative to the prior Iteration 0 run:

| class | n | avg gap | min | max | bytes ratio |
|---|---:|---:|---:|---:|---:|
| clean_speech | 20 | +0.08 | 0.00 | +1.50 | 1.01 |
| noisy_speech | 20 | -0.21 | -1.46 | +0.25 | 0.99 |
| stereo_speech | 20 | +0.02 | -0.00 | +0.38 | 1.02 |
| onset_plosive | 20 | -0.02 | -2.00 | +3.50 | 1.00 |
| source | 20 | -0.10 | -1.28 | +1.30 | 1.00 |
| mixed | 20 | -1.72 | -11.72 | +5.64 | 1.00 |
| music | 20 | -1.67 | -9.26 | +9.69 | 1.00 |

Across the five speech-oriented classes, own bytes are 692,384 versus 689,344
in the prior run (ratio 1.004), and average matched gap moves from -0.058 dB to
-0.046 dB. This is the same rate/quality level; the change removes the entropy
allocation mismatch rather than trading materially more bytes for quality.

## Post-Audit Phase 1: CELT CVBR Startup Damping (2026-07-17)

Phase 1 preserved the synthetic stereo-chords cell as the code-generated
`TestCELTMusicChordsMatchedBitrateReproducer`, then ranked TF/block,
dynamic-allocation/trim, stereo, and bandwidth/rate-target decisions. The
dominant cause was the simplified CELT CVBR target: a quiet tonal stream could
start at one quarter of its nominal packet target and take several frames to
refill its reservoir. Forced fullband slightly reduced quality, confirming that
the config 19 versus config 31 TOC difference was not the cause.

The adopted change applies libopus `compute_vbr`'s constrained-VBR damping in
the byte domain, approximately `base + 0.67*(target-base)`. It changes the
temporal distribution of the existing budget without changing CELT mode,
bandwidth, range-coder symbol order, or the one-second byte totals. The focused
test checks fresh-encoder packet determinism, first/subsequent TOCs, matched
bytes, Go/libopus cross-decode, and encoder/Go-decoder/libopus-decoder final
range equality on every packet.

The libopus scoreboard wrapper now sets `OPUS_SIGNAL_MUSIC` for music/mixed
classes, matching the Go `SignalMusic` hint. Older published rows left libopus
at signal `AUTO`; the focused 24-64 kbps reference TOCs and byte totals were
unchanged, but the comparison-condition correction is recorded explicitly.

Full local run:

```powershell
$env:OPUS_REAL_CORPUS = "1"
$env:OPUS_REAL_CORPUS_OUT = "testdata/phase1_adopt_full.csv"
go test -count=1 -tags opusref -run TestOpusRealCorpusMatchedBitrateScoreboard -v .
```

Result: 140/140 cells `status=ok`. Against the D-2 Iteration 0 REDO full
baseline, every class's loss-0 own-byte total is exactly unchanged. The five
speech-oriented classes keep the same aggregate matched gap, -0.0463 dB.

| class | n | avg gap | min | max | loss-0 byte ratio |
|---|---:|---:|---:|---:|---:|
| clean_speech | 20 | +0.08 | 0.00 | +1.50 | 1.000 |
| noisy_speech | 20 | -0.21 | -1.46 | +0.25 | 1.000 |
| stereo_speech | 20 | +0.02 | -0.00 | +0.38 | 1.000 |
| onset_plosive | 20 | -0.02 | -2.00 | +3.50 | 1.000 |
| source | 20 | -0.10 | -1.28 | +1.30 | 1.000 |
| mixed | 20 | -1.08 | -1.80 | +1.71 | 1.000 |
| music | 20 | -2.67 | -4.88 | +5.68 | 1.000 |

Stereo-chords loss-0 cells at matched bytes:

| bitrate | baseline gap | adopted gap | reduction | matched-byte ratio |
|---:|---:|---:|---:|---:|
| 16 kbps | +1.82 | -1.35 | 3.16 dB | 0.994 |
| 24 kbps | +9.18 | +5.68 | 3.50 dB | 0.987 |
| 32 kbps | +8.07 | +5.21 | 2.86 dB | 0.994 |
| 48 kbps | +9.69 | +0.94 | 8.75 dB | 0.995 |
| 64 kbps | +7.70 | -1.42 | 9.12 dB | 0.995 |

The prior music worst falls from +9.69 to +5.68 dB and the mixed worst from
+5.64 to +1.71 dB; no new music or mixed worst cell is created. The remaining
24/32 kbps chord gap is now the next measured CELT allocation/trim opportunity,
not evidence that the adopted CVBR correction should be expanded further.

## Post-Audit Medium: CELT TF-Estimate Trim (2026-07-18)

The isolated Medium trial adds the already-computed CELT `tfEstimate` to
allocation-trim analysis (`trim -= 2*tfEstimate` before quantization). The
focused 24 kbps stereo-chords matched gap moved from approximately +6.026 to
+5.886 dB. The full opt-in corpus completed 140/140 cells with all loss-0 own
byte totals unchanged and no class regression above the 0.3 dB gate.

The speech-oriented class summaries remained effectively unchanged. Mixed
average/worst gaps moved from -1.08/+1.71 to -1.07/+1.64 dB; music moved from
-2.67/+5.68 to -2.64/+5.61 dB. This is a small allocation-quality correction,
not a rate increase. Tonality slope and stateful stereo-saving inputs were not
combined into this trial.

## Post-Audit Phase 3: SILK/Hybrid Policy Gates (2026-07-17)

Phase 3 stopped without adopting a production policy change. A previously
measured libopus-style unified predictive threshold left target clean/noisy
speech unchanged and increased stereo-speech bytes by 63.3%. A new targeted
active-broadband exit from low-rate SILK to CELT completed 100/100 selected
speech cells but regressed onset/source average matched gaps by 0.11/0.09 dB
while increasing loss-0 bytes by 2%/1%.

The latter experiment followed a forced-fullband ablation. Blanket fullband
was not eligible for adoption because clean/stereo byte totals increased by
1.59x/2.63x. The narrower automatic gate removed those non-target byte
regressions but lost the apparent onset/source gain through additional mode
transitions. Both candidate implementations were removed; the diagnostic
class, bitrate, and bandwidth controls remain in the scoreboard harness.

See `.claude/memory/iterations/silk-hybrid-policy-phase3-2026-07-17.md` for the
decision table and exact command.
