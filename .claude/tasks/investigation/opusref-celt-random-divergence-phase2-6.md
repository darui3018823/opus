# Codex Task: Phase 2-6 — Root-cause opusref CELT random-packet divergence

Source: Phase 2-5 qualification attempt after fixing the first SILK redundancy
finding.
Status authority: `docs/CURRENT_IMPLEMENTATION.md`.
Integration branch: `codex/robustness`.

## Objective

Root-cause or explicitly reclassify the next
`FuzzOpusrefDecoderDifferential` finding: Pure Go and libopus both accept a
small CELT-only random packet at 48 kHz mono, but waveform output diverges beyond
the current gross-output threshold.

This is separate from the Phase 2-5 SILK leading-redundancy issue, which is
fixed locally.

## Reproducer

Fuzz target:

```text
go test -run='^$' -tags opusref -fuzz='^FuzzOpusrefDecoderDifferential$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v .
```

Minimized failing fuzz input from the failed qualification attempt:

```go
[]byte("\x04\x80\x7f\xa5\x00\xff\xe5\xe5\xa5\xa5\xc3")
```

Decoded fields:

```text
rate=48000
channels=1
packet=807fa500ffe5e5a5a5c3
packet_len=10
rmsDiff=9.15192
peakDiff=29.7155
peakGo=15.1083
peakRef=14.6072
```

The generated corpus file was under ignored `testdata/` and was removed after
recording this literal.

## Required behavior

1. Add a focused regression or diagnostic for the minimized packet before
   changing decoder behavior or fuzz oracle policy.
2. Determine whether this is:
   - a Pure Go CELT decoder bug,
   - acceptable non-bit-exact behavior on an extreme random accepted CELT packet,
   - or an oracle-policy issue where CELT waveform divergence should be logged
     rather than failed.
3. Do not weaken packet validation or production decode behavior merely to pass
   fuzz qualification.
4. If changing the oracle, document why accept/reject, duration, finiteness, and
   relative waveform checks remain useful without overclaiming CELT sample
   equivalence.
5. Re-run the Phase 2-4 opusref fuzz qualification after the fix or
   reclassification.

## Qualification

At minimum:

```text
go test -count=1 -tags opusref -run '^(TestOpusrefDecoderDifferentialMinimized12kSILK|FuzzOpusrefDecoderDifferential)$' -v .
go test -run='^$' -tags opusref -fuzz='^FuzzOpusrefDecoderDifferential$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v .
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

## Deliverables

- Root-cause or reclassification note added to this document.
- Regression/diagnostic coverage for `packet=807fa500ffe5e5a5a5c3`.
- Decoder fix or explicit evidence-backed fuzz oracle adjustment.
- Updated adoption decision for the opusref differential fuzz target.
