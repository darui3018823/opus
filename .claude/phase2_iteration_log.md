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
