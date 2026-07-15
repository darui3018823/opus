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
