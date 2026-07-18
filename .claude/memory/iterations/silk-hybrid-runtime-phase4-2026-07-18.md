# SILK/Hybrid Runtime Phase 4

Status: **Complete; three iterations adopted**

Last updated: 2026-07-18

Plan: `.claude/plans/post-audit-2026-07-17.md`

## Objective

Reduce realtime SILK/hybrid encoder allocation and latency without changing
packet bytes, decoded behavior, or final ranges. Measure mono/stereo SILK and
hybrid separately, retain only immediate-parent wins, and add a long-running
stream measurement before closing the phase.

## Baseline and Profiles

The fresh public benchmark baseline used five one-second samples:

```text
go test -run '^$' -bench '^BenchmarkPerf$/encode/(silk|hybrid)/(mono|stereo)/48k/20ms$' -benchtime=1s -count=5 -benchmem .
```

Baseline medians were:

| Workload | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| SILK mono | 5754602 | 672231 | 8541 |
| SILK stereo | 18341526 | 2102575 | 10973 |
| Hybrid mono | 3223129 | 585975 | 4409 |
| Hybrid stereo | 12418132 | 1793724 | 9457 |

Individual two-second CPU and heap profiles ranked
`silkLPCInversePredGainQ12` first for allocation count in every workload
(29-45%). `nlsfToLPCLibopus` was the next common family, while
`silkNSQDelDec` dominated stereo allocation bytes. CPU remained dominated by
LPC residual scoring, inverse prediction gain, and stereo delayed-decision
quantization.

## Bitstream Guard

`TestPerfPredictivePacketRegression` hashes length-delimited packet bytes and
`FinalRange` for 64 deterministic frames in each workload. It was recorded
before production changes and passed after every iteration. The fixed digests
cover all four SILK/hybrid mono/stereo routes.

## Iteration 1: Inverse-Gain Scratch

Commit: `81aa6aa perf(silk): stack allocate inverse gain scratch`

`silkLPCInversePredGainQ12` replaced a per-call `make([]int32, order)` with a
fixed `[silkMaxLPCOrder]int32` local array. Median allocation fell by 9-41% in
bytes/op and 29-50% in allocs/op across the four workloads. SILK mono time
improved 8.7%; no neighboring target had a material time regression.

Decision: adopted.

## Iteration 2: NLSF-to-LPC Scratch

Commit: `6fb2831 perf(silk): stack allocate NLSF conversion scratch`

`nlsfToLPCLibopus` replaced its bounded cLSF, P/Q polynomial, and a32QA1 heap
slices with maximum-order local arrays. Against Iteration 1, bytes/op fell by
6.8-28.8% and allocs/op by 29-48%. All target times improved in that
immediate-parent measurement.

Decision: adopted.

## Iteration 3: Delayed-Decision State Reuse

Commit: `2b189e5 perf(silk): reuse delayed decision states`

The maximum four `nsqDelayedDecision` candidates are now encoder-owned scratch.
Every used state is explicitly zeroed before initialization, preserving the
old fresh-allocation semantics. A detached worktree at parent `8fbd98f` was
measured in the same load window. SILK stereo fell from 1,772,856 to 1,413,062
B/op (-20.3%) and hybrid stereo from 1,468,271 to 1,311,290 B/op (-10.7%).
Their median times improved 2.7% and 0.4%; mono stayed within 3.1%.

Decision: adopted.

## Cumulative Result

Against the fresh phase baseline, final median allocation changed as follows:

| Workload | B/op change | allocs/op change |
|---|---:|---:|
| SILK mono | -57.5% | -73.8% |
| SILK stereo | -32.8% | -50.6% |
| Hybrid mono | -33.7% | -70.7% |
| Hybrid stereo | -26.9% | -57.3% |

Absolute time measurements drifted with machine load, so the adoption decisions
use the same-window parent comparisons above rather than unrelated absolute
samples.

## Long Stream and Retention

Commit: `8fbd98f test(perf): add predictive long stream benchmark`

`BenchmarkPerfLongStream` encodes 256 continuous frames per operation and
reports packet bytes/frame, frames/second, allocations, and live-heap growth
after warm-up. Five final samples used:

```text
go test -run '^$' -bench '^BenchmarkPerfLongStream$/encode/(silk|hybrid)/(mono|stereo)/48k/20ms$' -benchtime=1x -count=5 -benchmem .
```

Median retained live heap was 432 B for SILK mono, 4,608 B for SILK stereo,
384 B for hybrid mono, and 4,992 B for hybrid stereo. Packet bytes/frame were
constant in every sample. No frame-proportional retained-state growth was
observed.

## Verification

All required gates passed on 2026-07-18:

```text
go vet ./...
go test -count=1 ./...
go test -count=1 -tags opusref ./...
go test -count=1 -run '^TestOfficialVectors$' -v .
```

The official-vector run passed all 12 vectors. Normal and opusref full suites
passed, including the packet/final-range digest guard and libopus cross-decode
coverage.

## Conclusion

Phase 4 is adopted and complete. The next allocation candidates are NLSF
reconstruction/LPC destination-buffer APIs and deeper stereo NSQ/CELT working
storage. They require broader lifetime and aliasing changes than the bounded
scratch substitutions adopted here and are left for a future measured phase.
