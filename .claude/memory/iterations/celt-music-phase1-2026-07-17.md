# CELT/Music Phase 1 Decision Log

Date: 2026-07-17

Status: **Complete; CVBR candidate adopted**

## Objective

Explain and reduce the matched-byte CELT/music loss on the deterministic
stereo-chords cell without routing it away from CELT or hiding a rate increase.

## Scope and Acceptance

This iteration covers post-audit Phase 1 slices 1-1 through 1-5: preserve a
code-generated reproducer, rank the four requested decision areas, select one
root-cause fix, prove broad non-regression, and make an explicit adoption
decision.

The candidate must reduce the focused gap by at least 2 dB or 25%, keep packet
bytes within 5%, preserve CELT mode and final-range agreement, avoid adjacent
music regressions, and keep the speech-oriented average gap within 0.3 dB.

## Reproducer

`TestCELTMusicChordsMatchedBitrateReproducer` generates one second of 48 kHz
stereo PCM in code: 220, 277.18, and 329.63 Hz sines at the same gains as
`scripts/fetch_real_corpus.ps1`, with `R = 0.7*L` and signed-16-bit
quantization. The operating point is 20 ms, `ApplicationAudio`, `SignalMusic`,
complexity 5, constrained VBR, and zero loss.

The baseline is deterministic across fresh encoders. At 48 kbps it emitted
5,870 bytes over 50 packets, matched libopus at 5,901 bytes (ratio 0.994747),
used CELT NB config 19 throughout, and reproduced an 8.816 dB generated-fixture
gap. The ignored WAV scoreboard measured 9.688 dB at the same operating point.

The libopus wrapper gained an explicit music signal control. Earlier published
scoreboard rows used libopus signal `AUTO`; Phase 1 measurements set
`OPUS_SIGNAL_MUSIC` on both encoders. The reference TOC and byte totals did not
change at the focused 24-64 kbps cells, but the comparison-condition change is
recorded so the baselines are not silently conflated.

## Controlled Ablations

All rows below are the generated 48 kbps / 1 second cell. Every candidate kept
5,870 Go bytes and 5,901 matched-libopus bytes unless noted. Go encoder, Go
decoder, and libopus decoder final ranges agreed on all 50 packets; libopus
cross-decode SNR was at least 77 dB.

| area / ablation | own SNR | matched gap | effect vs baseline | result |
|---|---:|---:|---:|---|
| baseline | 21.097 | +8.816 | — | reproduces loss |
| TF disabled | 21.106 | +8.807 | +0.009 dB | neutral |
| forced short blocks | 20.927 | +8.985 | -0.170 dB | rejected |
| dynalloc disabled | 23.207 | +6.706 | +2.110 dB | secondary cause |
| allocation trim neutral | 22.184 | +7.728 | +1.087 dB | secondary cause |
| dynalloc + trim neutral | 24.234 | +5.678 | +3.137 dB | useful, not dominant |
| forced dual stereo, no intensity | 18.411 | +11.501 | -2.686 dB | rejected |
| forced fullband config 31 | 20.945 | +8.968 | -0.152 dB | rejected |
| full nominal CVBR target | 32.592 | -2.440 | +11.495 dB; bytes +3.1% | dominant but overly broad |
| 50% CVBR target floor | 28.887 | +1.026 | +7.790 dB; bytes unchanged | confirms startup pacing |
| libopus-style 2/3 CVBR blend | 29.847 | +0.066 | +8.750 dB; bytes unchanged | selected |

The baseline packet-size ramp was `31, 76, 98, 110, 115, 118, 120, 120,
121...` bytes including TOC. The selected blend changed it to `61, 81, 94,
103, 109, 113, 116, 118, 118, 120...`, while the one-second total remained
5,870 bytes because the existing reservoir recovered the same later-frame
budget. This identifies temporal target distribution, not total rate or coded
bandwidth, as the dominant interaction.

## Root Cause and Candidate

The simplified CELT VBR activity curve can reduce a non-silent frame to 25% of
the nominal target. Constrained VBR then starts with an empty local reservoir,
so quiet tonal streams severely underfund their first packets and recover only
after several damaged frames. libopus makes constrained VBR less aggressive in
`compute_vbr` using approximately:

```text
base_target + 0.67 * (target - base_target)
```

Applying the equivalent two-thirds blend to the Go byte target is one bounded
rate-target correction. It does not change range-coder symbol order, mode,
bandwidth, TF, allocation, or stereo decisions.

## Scoreboard Gates

The selected blend was run over every local class with each clip limited to one
second. All 140 cells encoded. Loss-0 own-byte totals were exactly unchanged in
every class. The five speech-oriented class averages were unchanged to the
reported 0.01 dB precision. The music worst cell fell from 9.69 to 5.68 dB;
the mixed worst fell from 5.64 to 1.71 dB. No new music or mixed worst cell was
created.

The final default-length run also encoded 140/140 cells. Relative to the D-2
Iteration 0 REDO full baseline, loss-0 own-byte totals were exactly unchanged
in all seven classes and the five speech-oriented aggregate gap was unchanged
at -0.0463 dB. Music's worst cell fell from +9.69 to +5.68 dB and mixed's from
+5.64 to +1.71 dB.

Focused WAV loss-0 results:

| bitrate | before gap | candidate gap | reduction | matched-byte ratio |
|---:|---:|---:|---:|---:|
| 16 kbps | +1.818 | -1.345 | 3.163 dB | 0.9940 |
| 24 kbps | +9.180 | +5.682 | 3.498 dB | 0.9867 |
| 32 kbps | +8.072 | +5.213 | 2.859 dB | 0.9944 |
| 48 kbps | +9.688 | +0.939 | 8.749 dB | 0.9947 |
| 64 kbps | +7.702 | -1.418 | 9.120 dB | 0.9949 |

The 24 and 32 kbps cells remain the largest CELT/music gaps. Dynamic allocation
and trim are the next measured cause if a later phase revisits them; they are
not combined with the CVBR fix in this iteration.

## Adoption Decision

**Adopted** in production commit `278cc8a` (`fix(celt): damp constrained VBR
target`). The change is limited to CELT-only constrained-VBR target damping;
temporary ablation gates were removed. The reproducer commit is `e155c54` and
the full investigation checkpoint is `0b367fc`.

## Verification Commands

Completed commands:

```powershell
go test -count=1 -tags opusref -run TestCELTMusicChordsMatchedBitrateReproducer -v .
go test -count=1 -tags opusref -run TestCELTMusicChordsPhase1Ablations -v .
$env:OPUS_REAL_CORPUS = "1"
$env:OPUS_REAL_CORPUS_MAX_SECONDS = "1"
go test -count=1 -tags opusref -run TestOpusRealCorpusMatchedBitrateScoreboard -v .
```

```powershell
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
go test -count=1 -run '^TestOfficialVectors$' -v .
$env:OPUS_REAL_CORPUS = "1"
go test -count=1 -tags opusref -run TestOpusRealCorpusMatchedBitrateScoreboard -v .
```

All commands passed on 2026-07-17. `TestOfficialVectors` passed 12/12 with a
maximum RMSE of 0.000809. The focused reproducer also passed at 24/48/64 kbps,
including fresh-encoder byte determinism, libopus cross-decode above 93 dB, and
encoder/Go-decoder/libopus-decoder final-range equality for all 50 packets per
cell.
