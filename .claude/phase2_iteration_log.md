# Phase 2 Iteration Log

Integration branch: `codex/robustness`.

## Iteration 1: stateful decoder sequence fuzz (WIP)

### Implemented locally

- Added `decoder_sequence_fuzz_test.go` with `FuzzDecoderSequence`.
- The input schema selects one of the five Opus output rates and mono/stereo,
  then executes a bounded sequence of normal decode, FEC, PLC, reset, gain, and
  phase-inversion operations.
- Bounds were reduced empirically to 4 KiB input, 16 operations, and 512 bytes
  per packet. Existing stateless fuzz targets retain responsibility for larger
  arbitrary packets.
- Every sequence is replayed through two fresh decoders. The target compares
  return counts, errors, complete PCM buffers, duration, bandwidth, final range,
  pitch, gain, and phase-inversion state.
- Additional invariants cover output preservation on error, destination guards
  on success, packet-duration agreement, PLC duration, constructor identity,
  control round trips, and reset state.
- Seeds cover CELT, malformed packet interleaving, reset-before-PLC, and a
  Pure-Go-generated SILK/LBRR loss sequence.
- Arbitrary FEC calls were restricted to packets for which `PacketHasLBRR`
  succeeds, because malformed arbitrary FEC payload probing appeared to be one
  possible source of poor iteration latency. The realistic FEC seed remains.

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

### Next steps

1. Do not mark Phase 2-1 complete and do not add the target to nightly CI yet.
2. Isolate operation latency by temporarily splitting normal Decode, FEC, and
   PLC sequence lanes or by running each cached/generated candidate in a child
   test process with a per-input timeout and input logging.
3. Determine whether the slow candidate is normal Decode after a rejected
   packet, long PLC after corrupted state, or FEC/LBRR probing.
4. If a reproducible slow input is found, commit it under
   `testdata/fuzz/FuzzDecoderSequence/` and fix the production root cause in a
   separate commit.
5. Restore useful sequence breadth, run the required 30-minute zero-crash
   qualification, then update `.github/workflows/fuzz.yml`, docs, and the Phase
   2 status.

Decision: WIP, not adopted. The target is committed only as a shutdown-safe
checkpoint.
