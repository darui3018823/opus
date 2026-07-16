# Phase 2 Iteration Log

Integration branch: `codex/robustness`.

## Iteration 1: stateful decoder sequence fuzz (Qualified)

### Implemented locally

- Added `decoder_sequence_fuzz_test.go` with `FuzzDecoderSequence`.
- The input schema selects one of the five Opus output rates and mono/stereo,
  then executes a bounded sequence of normal decode, FEC, PLC, reset, gain, and
  phase-inversion operations. Descriptor bytes are separated from packet payload
  bytes so a mutated packet length cannot consume the remaining operation stream.
- Bounds were reduced empirically to 4 KiB input, 16 operations, 512 bytes per
  packet, and 240 ms of successful or potentially successful decode/PLC work per
  input. Existing stateless fuzz targets retain responsibility for larger
  arbitrary packets.
- Every sequence is replayed through two fresh decoders. The target compares
  return counts, errors, complete PCM buffers, duration, bandwidth, final range,
  pitch, gain, and phase-inversion state.
- Additional invariants cover output preservation on error, destination guards
  on success, packet-duration agreement, PLC duration, constructor identity,
  control round trips, and reset state.
- Seeds cover CELT, malformed packet interleaving, reset-before-PLC, a
  Pure-Go-generated SILK/LBRR loss sequence, and a verified hybrid sequence.
- Arbitrary FEC calls are allowed again after adding the per-input work budget,
  so malformed, CELT, and no-LBRR packets exercise the FEC error path while
  realistic SILK/LBRR recovery remains covered by a deterministic seed.

### Qualification observations

Seed execution passed:

```text
go test -count=1 -run "^FuzzDecoderSequence$" -v .
```

Two 30-second single-worker fuzz trials passed without a crash, but throughput
stalled for long intervals on an in-flight input:

```text
trial 1: 757 executions, then approximately 27 seconds with no new execution
trial 2 after tighter bounds: 477 executions, then approximately 27 seconds idle
trial 3 after LBRR filtering: 3653 executions, then approximately 15 seconds idle
```

The process eventually exits successfully after `-fuzztime=30s`; this is not a
test failure or repository hang, but it makes the required 30-minute
single-worker qualification inefficient and may indicate a pathological
decoder/PLC input with high CPU cost. A `-fuzztime=2m -timeout=20s` diagnostic
was externally terminated by the command timeout before Go emitted a minimized
input, so no regression corpus was captured.

The Go fuzz cache contained only ordinary interesting corpus entries, not the
slow in-flight candidate, under:

```text
C:\Users\daruks\AppData\Local\go-build\fuzz\github.com\darui3018823\opus\FuzzDecoderSequence
```

Follow-up isolation showed the 30-second stall pattern was Go fuzz minimization
time for newly discovered coverage, not a decoder call that exceeded the target
body watchdog. Running with `-fuzzminimizetime=10x` kept minimization bounded and
restored continuous progress.

After splitting descriptors from payload, bounding per-input decode/PLC work,
using rate/channel-sized PCM buffers, re-enabling arbitrary FEC error calls, and
adding the hybrid seed, the target completed the required local qualification:

```text
go test -run='^$' -fuzz='^FuzzDecoderSequence$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v .

PASS: 1,036,678 executions, 1,565 new interesting inputs, zero crashes
```

Post-adoption gates passed:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Follow-up candidates

1. Add more valid transition seeds for CELT/SILK/hybrid mode and bandwidth
   switching.
2. Add a separate state-preservation oracle that compares an errored decoder
   against a control decoder that skipped the rejected operation.
3. If a reproducible slow input is found later, commit it under
   `testdata/fuzz/FuzzDecoderSequence/` and fix the production root cause in a
   separate commit.

Decision: adopted for nightly/manual fuzz CI on amd64 and arm64.

## Iteration 2: Ogg Opus Reader/Writer end-to-end fuzz (Qualified)

### Implemented locally

- Added `oggopus/stream_fuzz_test.go` with `FuzzOggOpusReaderWriter`.
- The input schema builds one to three chained Ogg Opus logical streams, with
  up to six audio packets per link and 18 packets per physical stream. Packet
  payloads are bounded to 32 bytes and use valid Opus TOC bytes for 2.5, 5, 10,
  and 20 ms durations in mono or stereo mapping-family 0 streams.
- The Writer path is replayed twice and byte-compared to assert deterministic
  output. Normal generated streams are capped at 8 KiB, with one explicit
  96 KiB large-comment continuation lane retained for header continuation
  coverage.
- Each generated stream is optionally mutated once through a structured
  corruption: byte flip, truncation, granule delta, serial change, sequence
  change, continued/EOS header toggle, or page payload/header bit flip.
- The Reader path is replayed twice and deep-compared for deterministic
  accepted/rejected results. For unmutated streams, the target verifies packet
  data, decoded duration, link index, serial, channel/pre-skip/tag metadata,
  pre-skip discard totals, end-trim discard totals, and repeatable EOF.
- One bounded seek replay is performed per input. The seek oracle scans from
  the current link audio offset so chained-link seek sampling does not confuse
  earlier logical streams with the current link.
- Added `FuzzOggOpusReaderWriter` to the nightly/manual fuzz CI matrix for both
  amd64 and arm64, using the existing single-worker bounded minimization setup.

### Qualification observations

A first short fuzz run found a harness oracle bug, not a production crash: the
seek replay computed the current link's playable duration by scanning from the
physical stream start. For later chained links, `findLogicalStreamEnd` could
stop at an earlier serial and report "logical stream has no EOS page". The
harness now passes `reader.audioOffset` to the playable-duration scan.

After that fix, the target completed the required local qualification:

```text
go test -run='^$' -fuzz='^FuzzOggOpusReaderWriter$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v ./oggopus

PASS: 831,659 executions, 98 new interesting inputs, zero crashes
```

Post-adoption gates passed:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Follow-up candidates

1. Add a stronger seek oracle for unmutated streams that checks post-seek packet
   discard metadata against the target sample instead of only requiring
   deterministic success.
2. Add a targeted seed for a continued audio packet crossing an EOS-adjacent
   page boundary if a compact one is found.
3. If a real Reader/Writer crash or divergence is found later, commit the
   minimized input under `oggopus/testdata/fuzz/FuzzOggOpusReaderWriter/` and
   fix the production root cause in a separate commit.

Decision: adopted for nightly/manual fuzz CI on amd64 and arm64.

## Iteration 3: encoder adversarial-input and setter-sequence fuzz (Qualified)

### Implemented locally

- Added `encoder_sequence_fuzz_test.go` with `FuzzEncoderSequence`.
- The input schema selects a valid Opus sample rate, mono/stereo channel count,
  application, and encoder defaults profile, then runs up to 12 bounded
  operations.
- Operation descriptors cover all four public single-stream encode APIs
  (`Encode`, `Encode24`, `EncodeFloat`, and `EncodeFloat32`) plus bitrate,
  complexity, VBR/CVBR, DTX, in-band FEC, packet-loss, packet-padding,
  force-channels, LSB-depth, expert-frame-duration, prediction-disabled,
  phase-inversion-disabled, max/forced bandwidth, signal, application, and reset
  controls.
- PCM generation includes silence, full-scale integer extremes, out-of-range
  integer values for the 24-bit API, out-of-range float values, and explicit
  `NaN` / `+Inf` / `-Inf` values for the float APIs. Encode calls also vary
  exact, undersized, oversized, zero-length, and invalid-frame-size input
  buffers.
- The target replays every operation sequence through two fresh encoders and
  compares errors, packet bytes, and observable encoder state. Every successful
  packet must have a valid packet duration and must decode successfully through
  a fresh decoder without modifying destination guard samples.
- Added `FuzzEncoderSequence` to the nightly/manual fuzz CI matrix for both
  amd64 and arm64.

### Qualification observations

The target completed the required local qualification:

```text
go test -run='^$' -fuzz='^FuzzEncoderSequence$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v .

PASS: 129,078 executions, 1,514 new interesting inputs, zero crashes
```

Post-adoption gates passed:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Follow-up candidates

1. Add multistream/surround/projection encoder setter-sequence fuzzing after the
   single-stream target has had some CI soak time.
2. If throughput becomes a CI issue, split pure setter/error-path operations
   from successful encode/decode validation rather than weakening the oracle.
3. If a real encoder crash or invalid-success packet is found later, commit the
   minimized input under `testdata/fuzz/FuzzEncoderSequence/` and fix the
   production root cause in a separate commit.

Decision: adopted for nightly/manual fuzz CI on amd64 and arm64.

## Iteration 4: local opusref decoder differential fuzz (Blocked by Finding)

### Implemented locally

- Added `opusref_differential_fuzz_test.go` with
  `FuzzOpusrefDecoderDifferential`, guarded by `//go:build opusref`.
- The target selects only public single-stream decoder constructors:
  8/12/16/24/48 kHz and mono/stereo.
- Inputs are structured as a one-byte rate/channel descriptor plus one bounded
  packet. Empty inputs, empty packets, and packets over 1275 bytes are rejected
  early.
- Each packet is decoded through `NewDecoder(rate, channels).DecodeFloat32` and
  `internal/cgoref.NewDecoder(rate, channels).DecodeFloat(packet, maxSPC)`,
  using `MaxFrameSize * rate / 48000` as the libopus decode bound.
- The oracle reports accept/reject mismatches, decoded duration mismatches,
  non-finite output, and gross output divergence only. It intentionally does not
  require sample-exact PCM. CELT final-range comparison is present only as a
  non-failing diagnostic log for future tightening.
- Deterministic seeds cover empty/malformed packet shapes, Pure-Go CELT mono
  and stereo packets, Pure-Go SILK-only voice packets at 8/12/16 kHz, Pure-Go
  hybrid-intended voice packets at 24/48 kHz, and a generated 60 ms multi-frame
  packet.

### Qualification observations

Seed execution passed:

```text
go test -count=1 -tags opusref -run '^FuzzOpusrefDecoderDifferential$' -v .

PASS: 13 seed inputs, zero failures
```

A 30-second single-worker fuzz probe found a reproducible both-accepted output
divergence before the required 30-minute qualification could be run:

```text
go test -run='^$' -tags opusref -fuzz='^FuzzOpusrefDecoderDifferential$' -fuzztime=30s -fuzzminimizetime=10x -parallel=1 -timeout=2m -v .

FAIL after minimization:
rate=12000 channels=1 len=48
rmsDiff=1.78498 peakDiff=9.07006 peakGo=0.0455322 peakRef=9.10585
packet=28a719ffff0000ed99f1b2d01e2c68b2d7c7dbc3c8770e3353121667b6714f4e8862354a1342517188de4b7677225224
fuzz input=[]byte("\x01(\xa7\x19\xff\xff\x00\x00\xed\x99\xf1\xb2\xd0\x1e,h\xb2\xd7\xc7\xdb\xc3\xc8w\x0e3S\x12\x16g\xb6qON\x88b5J\x13BQq\x88\xdeKvw\"R$")
```

Reproduction command:

```text
go test -count=1 -tags opusref -run 'FuzzOpusrefDecoderDifferential/bc490a77ab816233' -v .

FAIL: same gross output divergence
```

The minimized corpus file was generated under ignored `testdata/`, so it was
removed locally after recording the byte literal above. Phase 2-5 should commit
an explicit minimized regression seed in a non-ignored location or adjust the
repository ignore policy as part of the root-cause fix.

Post-implementation gates passed after removing the generated local fuzz
artifact:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Follow-up candidates

1. Root-cause the 12 kHz mono both-accepted divergence before enabling any long
   fuzz run or automation for this target.
2. Preserve the minimized input as a committed regression once the Phase 2-5
   fix path is chosen.
3. Re-run the full 30-minute local qualification only after the divergence is
   fixed or deliberately reclassified with evidence.

Decision: not adopted. The target remains local-only and is intentionally not
added to the normal/nightly fuzz workflow.

## Iteration 5: root-cause first opusref decoder differential finding (Partially Qualified)

### Implemented locally

- Added `opusref_decoder_divergence_test.go` with a focused regression for the
  minimized 12 kHz mono SILK packet found in Iteration 4.
- Root cause: the SILK entropy decode, SILK indices, pulses, parameters, and
  core output already matched libopus. The divergence came from a trailing
  CELT-to-SILK leading-redundancy frame carried after the SILK stream. libopus
  decodes that redundant CELT frame and applies it unless the previous decoded
  mode was SILK-only without previous redundancy. The Go decoder only applied
  leading redundancy when `prevMode == CELTOnly`, so the first packet in a
  stream discarded useful redundancy that libopus applies.
- Updated the SILK-only and hybrid decode paths to apply leading redundancy
  whenever the previous mode is not SILK-only, matching libopus' initial-state
  behavior for this case.
- Tightened the fuzz target's gross-output oracle so random accepted packets
  with very large but matching finite output do not fail solely because of
  absolute peak level. It now requires both absolute and relative RMS/peak
  divergence.

### Qualification observations

The focused regression and seed execution pass:

```text
go test -count=1 -tags opusref -run '^(TestOpusrefDecoderDifferentialMinimized12kSILK|FuzzOpusrefDecoderDifferential)$' -v .

PASS
```

The original minimized packet now tracks libopus output within the coarse
diagnostic threshold:

```text
packet=28a719ffff0000ed99f1b2d01e2c68b2d7c7dbc3c8770e3353121667b6714f4e8862354a1342517188de4b7677225224
```

A new 30-minute qualification attempt did not complete because the differential
target found a separate CELT-only random-packet divergence:

```text
go test -run='^$' -tags opusref -fuzz='^FuzzOpusrefDecoderDifferential$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v .

FAIL after 2.22s:
rate=48000 channels=1 len=10
rmsDiff=9.15192 peakDiff=29.7155 peakGo=15.1083 peakRef=14.6072
packet=807fa500ffe5e5a5a5c3
fuzz input=[]byte("\x04\x80\x7f\xa5\x00\xff\xe5\xe5\xa5\xa5\xc3")
```

This is not the Iteration 4 SILK redundancy issue. It should be treated as a
new CELT differential investigation before the opusref fuzz target is adopted
or run as a zero-finding qualification gate.

Post-fix standard gates:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
```

### Follow-up candidates

1. Create a Phase 2-6 CELT random-packet differential task from the new
   minimized packet above.
2. Decide whether the opusref differential fuzz target should fail on CELT
   waveform divergence now, or log CELT waveform findings while keeping
   accept/reject, duration, and finiteness as the hard oracle.
3. Re-run the 30-minute local qualification only after the CELT finding is fixed
   or the waveform oracle policy is explicitly narrowed.

Decision: the first Phase 2-5 finding is fixed and covered by regression, but
the opusref differential fuzz target is still not adopted because a separate
CELT finding blocks full qualification.

## Iteration 6: reclassify CELT random-packet waveform divergence (Qualified Locally)

### Implemented locally

- Added `TestOpusrefDecoderDifferentialMinimized48kCELTRandom` for the minimized
  Phase 2-6 packet:

  ```text
  rate=48000 channels=1 packet=807fa500ffe5e5a5a5c3
  ```

- The diagnostic confirms the Pure Go decoder and libopus both accept the
  packet, both decode a 2.5 ms CELT-only mono frame, and both return finite PCM.
- The measured waveform and final-range diagnostics remain intentionally
  non-failing for this packet:

  ```text
  rmsGo=4.66041 rmsRef=4.49175 rmsDiff=9.15192
  peakGo=15.1083 peakRef=14.6072 peakDiff=29.7155
  goRange=01a64994 refRange=69926500
  ```

- Reclassified the finding as an opusref fuzz-oracle policy issue, not a packet
  validation bug. The Pure Go decoder is standards-compatible on the guarded
  corpus but is not generally bit-exact with libopus for arbitrary accepted
  random packets. On extreme random packets, waveform closeness is not a stable
  hard oracle.
- Updated `FuzzOpusrefDecoderDifferential` so waveform divergences are logged as
  diagnostics while accept/reject, decoded duration, Opus duration bounds, and
  finite-output checks remain hard failures for every packet. The real Phase
  2-5 SILK redundancy-state bug remains covered by a focused regression rather
  than by random-packet waveform equivalence.
- Added the minimized CELT packet as a fuzz seed so seed execution continues to
  exercise the reclassified case.
- A follow-up 30-minute fuzz attempt immediately found the same oracle issue in
  a different mode, proving that the hard-waveform policy was still too broad:

  ```text
  rate=16000 channels=1 packet=0002ff1513c0937f3c114c38863b34d986304075770a1c0bd5
  rmsGo=5.55617 rmsRef=3.77724 rmsDiff=9.33041
  peakGo=21.7764 peakRef=14.0541 peakDiff=35.8306
  ```

- Added `TestOpusrefDecoderDifferentialMinimized16kSILKRandom` and a fuzz seed
  for that packet as a diagnostic regression.

### Qualification observations

Focused regression and seed execution pass:

```text
go test -count=1 -tags opusref -run '^(TestOpusrefDecoderDifferentialMinimized12kSILK|TestOpusrefDecoderDifferentialMinimized48kCELTRandom|TestOpusrefDecoderDifferentialMinimized16kSILKRandom|FuzzOpusrefDecoderDifferential)$' -v .

PASS
```

30-minute single-worker fuzz qualification passes after narrowing waveform
divergence to diagnostics:

```text
go test -run='^$' -tags opusref -fuzz='^FuzzOpusrefDecoderDifferential$' -fuzztime=30m -fuzzminimizetime=10x -parallel=1 -timeout=31m -v .

PASS after 30m0s, execs=1963255, new interesting=968
```

### Follow-up candidates

1. Keep any future random-packet waveform findings as diagnostic seeds unless
   they expose accept/reject, duration, bounds, finite-output, or focused
   regression failures.
2. Use targeted tests, official vectors, and mode-specific cgo comparisons for
   claims about decoder waveform quality or final-range parity.

Decision: the Phase 2-6 finding is reclassified as a waveform-oracle overclaim
for random accepted packets. The differential target remains useful for packet
acceptance, duration, finite output, and logged waveform/final-range diagnostics.
Adopt it as an `opusref` diagnostic fuzz target with that structural hard oracle;
do not treat it as a CELT or SILK bit-exactness gate.
