# Predictive NLSF Destination-Scratch Iteration

Date: 2026-07-21
Status: Complete — adopted

## Objective

Reduce SILK/hybrid predictive encode allocation without changing packet bytes,
entropy final ranges, mode selection, or codec quality decisions. The target
was the NLSF reconstruction/LPC result-buffer family identified by the
post-audit runtime profile.

## Profile

Fresh `alloc_objects` profiles ranked `silkLPCFit`,
`refineNLSFResidualFrom`, and `nlsfToLPCLibopus` among the largest allocation
families. The residual search allocated a trial index slice, reconstructed NLSF
slice, and LPC result slice for every bounded candidate even though each result
was consumed before the next candidate.

## Adopted Change

- Added internal destination-buffer forms of NLSF reconstruction, NLSF-to-LPC,
  and LPC fitting while preserving the existing allocating wrappers.
- Reused maximum-order stack arrays across every candidate in one residual
  search and removed the redundant zero seed allocation.
- Added a direct test proving the NLSF-to-LPC destination form reuses caller
  storage and produces the same coefficients.

All bounds are the existing `silkMaxLPCOrder`; no returned state aliases the
scratch arrays.

## Short Benchmark

Three one-second samples were taken before and after in the same work window.
Allocation medians were stable; time is reported only as supporting evidence.

| workload | before B/op | after B/op | change | before allocs/op | after allocs/op | change |
|---|---:|---:|---:|---:|---:|---:|
| SILK stereo | 1,376,152 | 1,273,821 | -7.4% | 4,839 | 3,179 | -34.3% |
| hybrid stereo | 1,274,752 | 1,173,521 | -7.9% | 3,464 | 1,821 | -47.4% |

The final four-workload medians were 161,256 B/op and 231 allocs/op for SILK
mono, 1,273,821/3,179 for SILK stereo, 327,171/298 for hybrid mono, and
1,173,521/1,821 for hybrid stereo. Against the prior recorded medians, mono
allocation counts fell by approximately 86% for SILK and 71% for hybrid.

## Long Stream

The 256-frame benchmark kept packet bytes/frame unchanged at 96.16, 171.8,
161.0, and 241.0. Median retained live heap remained bounded at 384 bytes for
SILK mono, 4,608 for SILK stereo, 832 for hybrid mono, and 4,880 for hybrid
stereo. Allocation counts per 256 frames fell to 59,242, 826,789, 76,888, and
466,433 respectively.

## Verification

```text
go test -count=1 ./internal/silk
go test -count=1 -run '^TestPerfPredictivePacketRegression$' .
go test -count=1 -tags opusref -run '^(TestCGOStereoTrellisFinalRange|TestCGOHybridTrellisFinalRange)$' .
go test -count=1 ./...
go test -count=1 -tags opusref ./...
go vet ./...
go test -run '^$' -bench '^BenchmarkPerf$/encode/(silk|hybrid)/(mono|stereo)/48k/20ms$' -benchtime=1s -count=3 -benchmem .
go test -run '^$' -bench '^BenchmarkPerfLongStream$/encode/(silk|hybrid)/(mono|stereo)/48k/20ms$' -benchtime=1x -count=3 -benchmem .
```

The four 64-frame packet/final-range digests are unchanged, and the full normal
and `opusref` suites pass.
