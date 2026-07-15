# Real Corpus Matched-Bitrate Scoreboard

Last reviewed: 2026-07-15

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

## D-2 Iteration 0 (2026-07-15)

Branch `codex/d2-hybrid-target-clamp` fixes the baseline hybrid CVBR
encode-size failure. Non-redundant VBR/CVBR hybrid frames now emit the actual
range-coder size when final flush exceeds the adaptive target by a few bytes,
instead of returning `hybrid frame ... exceeds target`.

Review note: an alternative fix (raising the target to a floor before CELT
encodes) was implemented and rejected — it produced non-conformant streams on
the overshoot frames (libopus cross-decode SNR collapsed to ~0.1 dB). The
spill approach was validated against libopus:
`TestHybridCVBROnsetLibopusConsistency` cross-decodes the overshoot fixture
with libopus and guards against encoder/decoder allocation divergence.

Full local run:

```powershell
$env:OPUS_REAL_CORPUS = "1"
$env:OPUS_REAL_CORPUS_OUT = "testdata/real_corpus_scoreboard_d2_iter0.csv"
go test -count=1 -tags opusref -run TestOpusRealCorpusMatchedBitrateScoreboard -v .
```

Result: 140/140 cells `status=ok`. The two baseline failed bitrate cells now
expand to their four packet-loss rows each, so the total row count increases
from 134 to 140.

`gap_snr_matched_db` by class:

| class | n | avg | min | max |
|---|---:|---:|---:|---:|
| clean_speech | 20 | +0.08 | 0.00 | +1.50 |
| noisy_speech | 20 | -0.21 | -1.41 | +0.28 |
| stereo_speech | 20 | +0.02 | -0.00 | +0.42 |
| onset_plosive | 20 | -0.05 | -2.11 | +3.50 |
| source | 20 | -0.13 | -1.43 | +1.30 |
| mixed | 20 | -1.72 | -11.72 | +5.64 |
| music | 20 | -1.67 | -9.26 | +9.69 |
