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
