# SILK/Hybrid Policy Phase 3 Decision

Status: **Completed; stopped after two consecutive rejected gates**

Last updated: 2026-07-17

Plan: `.claude/plans/post-audit-2026-07-17.md`

## Objective

Measure predictive mode, rate, and bandwidth policy gates at matched bytes and
retain only a net per-bit quality improvement without moving the loss into a
different corpus class.

## Candidate 1: libopus-style predictive-family thresholds

This candidate was measured immediately before the post-audit plan and remains
the direct predecessor evidence for Phase 3. It replaced the separate Go
SILK-only and hybrid limits with a single libopus-style predictive-family
threshold: 64 kbps mono or 44 kbps stereo, an 8 kbps VoIP bias, and the existing
FEC extension. Bandwidth then selected SILK-only or hybrid.

The 140-cell scoreboard completed successfully, but the target clean/noisy
speech cells were byte-for-byte and gap-for-gap unchanged. Stereo speech own
bytes increased from 34,956 to 57,096 (1.633x): its 48 kbps cell changed from a
127-byte SILK-WB stream to a 6,083-byte hybrid-SWB stream. The 64 kbps cell
changed from hybrid-FB to CELT-FB and saved only 421 bytes. The candidate was
rejected and its production changes were removed.

Phase 1 subsequently left every loss-0 speech-class byte total and the aggregate
speech matched gap unchanged. Phase 2 changed decoder loss semantics, not this
candidate's decisive no-loss byte expansion, so the rejection remains applicable
to the post-audit baseline.

## Candidate 2: broadband predictive exit

### Target and ablation

The current 48 kHz voice policy uses SILK-WB at 16/24/32 kbps while libopus
selects hybrid-FB on the local speech corpus. A targeted fullband ablation ran
the five speech-oriented classes at 16/24/32/48/64 kbps and losses 0/5/10/20.
It showed that a blanket fullband policy was invalid: clean/stereo own bytes
increased by 1.59x/2.63x. It did, however, nominate active broadband onset and
source speech for a narrower gate.

The candidate routed only automatic-bandwidth, non-sparse voice frames above
-34 dBFS RMS with detected energy beyond 8 kHz from low-rate SILK to CELT at
the detected bandwidth. Quiet broadband and sparse harmonic frames remained on
SILK. A focused test guarded those three predicates.

### Targeted scoreboard result

`testdata/phase3_broadband_exit.csv` completed 100/100 target cells. Compared
with `testdata/phase1_adopt_full.csv`:

| class | baseline avg gap | candidate avg gap | delta | baseline max | candidate max | loss-0 byte ratio |
|---|---:|---:|---:|---:|---:|---:|
| clean_speech | +0.08 | +0.08 | 0.00 | +1.50 | +1.50 | 1.00 |
| noisy_speech | -0.21 | -0.21 | 0.00 | +0.25 | +0.25 | 1.00 |
| onset_plosive | -0.02 | +0.08 | +0.11 | +3.50 | +3.77 | 1.02 |
| source | -0.10 | -0.01 | +0.09 | +1.30 | +1.73 | 1.01 |
| stereo_speech | +0.02 | +0.02 | 0.00 | +0.38 | +0.38 | 1.00 |

The target classes both regressed while using more bytes. Frequent
SILK/CELT transitions erased the static forced-fullband ablation's apparent
gain. The candidate was rejected before a full 140-cell run, and all production
and focused-test changes were removed.

## Decision

Phase 3 stops under its explicit rule after these two consecutive,
well-motivated candidates failed to produce a net per-bit win. No new production
policy gate is adopted. The targeted scoreboard controls for class, bitrate,
and forced Go bandwidth remain available for future investigations.

The remaining SILK internal-rate, stereo-width, and broader VBR/CVBR/DTX policy
gaps stay documented rather than being hidden behind unmeasured routing. Reopen
them only for a concrete interoperability defect, a real-use quality report, or
a richer corpus that supplies a new measured target.

## Verification

Candidate measurement:

```powershell
$env:OPUS_REAL_CORPUS = "1"
$env:OPUS_REAL_CORPUS_CLASSES = "clean_speech,noisy_speech,onset_plosive,source,stereo_speech"
$env:OPUS_REAL_CORPUS_OUT = "testdata/phase3_broadband_exit.csv"
go test -count=1 -tags opusref -run '^TestOpusRealCorpusMatchedBitrateScoreboard$' -v .
```

The final production tree contains neither rejected candidate. Common
verification results are recorded in the Phase 3 closeout commit.
