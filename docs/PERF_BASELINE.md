# Performance Baseline

Last measured: 2026-07-17

This baseline records the Phase 3-1 public benchmark harness. Future Phase 3
optimization iterations should compare against this file with `benchstat` or an
equivalent before/after benchmark table. A claimed performance win needs at
least 5% improvement in time or allocations and must preserve packet bytes or
decoded PCM for representative inputs.

## Benchmark Harness

Command:

```text
go test -run '^$' -bench '^BenchmarkPerf/' -benchtime=200ms -count=5 -benchmem .
```

Short smoke command:

```text
go test -run '^$' -bench '^BenchmarkPerf/' -benchtime=1x -benchmem .
```

Environment:

```text
goos: windows
goarch: amd64
Go: go1.26.5 windows/amd64
OS: Microsoft Windows 11 Home 10.0.22631
CPU: 11th Gen Intel(R) Core(TM) i7-11700 @ 2.50GHz
logical CPUs: 16
package: github.com/darui3018823/opus
source base before Phase 3-1 commit: b0044dd
```

Workloads:

| Workload | Encoder setup | Signal |
|---|---|---|
| `celt/mono/48k/20ms` | `ApplicationAudio`, 96 kbps, `SignalMusic` | broadband music-like harmonic frame |
| `celt/stereo/48k/20ms` | `ApplicationAudio`, 128 kbps, `SignalMusic` | broadband music-like harmonic frame |
| `silk/mono/48k/20ms` | `ApplicationVOIP`, 24 kbps, `SignalVoice` | deterministic speech-like frame |
| `silk/stereo/48k/20ms` | `ApplicationVOIP`, 36 kbps, `SignalVoice` | deterministic speech-like frame |
| `hybrid/mono/48k/20ms` | `ApplicationVOIP`, 64 kbps, `SignalVoice` | speech-like frame plus high-band tone |
| `hybrid/stereo/48k/20ms` | `ApplicationVOIP`, 96 kbps, `SignalVoice` | speech-like frame plus high-band tone |

Each benchmark validates the generated packet's TOC mode and 20 ms duration
before timing starts.

## Median Results

Medians from five runs:

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkPerf/encode/celt/mono/48k/20ms-16` | 374810 | 182730 | 245 |
| `BenchmarkPerf/decode/celt/mono/48k/20ms-16` | 226426 | 114833 | 123 |
| `BenchmarkPerf/encode/celt/stereo/48k/20ms-16` | 463695 | 277111 | 352 |
| `BenchmarkPerf/decode/celt/stereo/48k/20ms-16` | 356998 | 213512 | 166 |
| `BenchmarkPerf/encode/silk/mono/48k/20ms-16` | 5594485 | 683858 | 8407 |
| `BenchmarkPerf/decode/silk/mono/48k/20ms-16` | 56689 | 44940 | 51 |
| `BenchmarkPerf/encode/silk/stereo/48k/20ms-16` | 20143800 | 5095172 | 14060 |
| `BenchmarkPerf/decode/silk/stereo/48k/20ms-16` | 116918 | 98306 | 89 |
| `BenchmarkPerf/encode/hybrid/mono/48k/20ms-16` | 3544840 | 670307 | 4517 |
| `BenchmarkPerf/decode/hybrid/mono/48k/20ms-16` | 195856 | 139174 | 94 |
| `BenchmarkPerf/encode/hybrid/stereo/48k/20ms-16` | 12391829 | 3226950 | 10867 |
| `BenchmarkPerf/decode/hybrid/stereo/48k/20ms-16` | 351388 | 276985 | 148 |

## Raw Output

```text
goos: windows
goarch: amd64
pkg: github.com/darui3018823/opus
cpu: 11th Gen Intel(R) Core(TM) i7-11700 @ 2.50GHz
BenchmarkPerf/encode/celt/mono/48k/20ms-16         	     614	    553286 ns/op	  182741 B/op	     245 allocs/op
BenchmarkPerf/encode/celt/mono/48k/20ms-16         	     644	    383187 ns/op	  182726 B/op	     245 allocs/op
BenchmarkPerf/encode/celt/mono/48k/20ms-16         	     618	    374810 ns/op	  182742 B/op	     245 allocs/op
BenchmarkPerf/encode/celt/mono/48k/20ms-16         	     724	    340323 ns/op	  182730 B/op	     245 allocs/op
BenchmarkPerf/encode/celt/mono/48k/20ms-16         	     778	    349270 ns/op	  182728 B/op	     245 allocs/op
BenchmarkPerf/decode/celt/mono/48k/20ms-16         	     996	    257095 ns/op	  114833 B/op	     123 allocs/op
BenchmarkPerf/decode/celt/mono/48k/20ms-16         	    1102	    241880 ns/op	  114840 B/op	     123 allocs/op
BenchmarkPerf/decode/celt/mono/48k/20ms-16         	    1104	    226426 ns/op	  114838 B/op	     123 allocs/op
BenchmarkPerf/decode/celt/mono/48k/20ms-16         	    1135	    219945 ns/op	  114830 B/op	     123 allocs/op
BenchmarkPerf/decode/celt/mono/48k/20ms-16         	    1140	    216153 ns/op	  114825 B/op	     123 allocs/op
BenchmarkPerf/encode/celt/stereo/48k/20ms-16       	     505	    458229 ns/op	  277111 B/op	     352 allocs/op
BenchmarkPerf/encode/celt/stereo/48k/20ms-16       	     518	    492906 ns/op	  277090 B/op	     352 allocs/op
BenchmarkPerf/encode/celt/stereo/48k/20ms-16       	     494	    463695 ns/op	  277122 B/op	     352 allocs/op
BenchmarkPerf/encode/celt/stereo/48k/20ms-16       	     544	    471085 ns/op	  277125 B/op	     352 allocs/op
BenchmarkPerf/encode/celt/stereo/48k/20ms-16       	     505	    440483 ns/op	  277111 B/op	     352 allocs/op
BenchmarkPerf/decode/celt/stereo/48k/20ms-16       	     744	    328846 ns/op	  213531 B/op	     166 allocs/op
BenchmarkPerf/decode/celt/stereo/48k/20ms-16       	     652	    370454 ns/op	  213562 B/op	     166 allocs/op
BenchmarkPerf/decode/celt/stereo/48k/20ms-16       	     698	    356998 ns/op	  213505 B/op	     166 allocs/op
BenchmarkPerf/decode/celt/stereo/48k/20ms-16       	     631	    342089 ns/op	  213512 B/op	     166 allocs/op
BenchmarkPerf/decode/celt/stereo/48k/20ms-16       	     704	    391441 ns/op	  213496 B/op	     166 allocs/op
BenchmarkPerf/encode/silk/mono/48k/20ms-16         	      38	   5684339 ns/op	  683858 B/op	    8407 allocs/op
BenchmarkPerf/encode/silk/mono/48k/20ms-16         	      40	   5493475 ns/op	  688078 B/op	    8464 allocs/op
BenchmarkPerf/encode/silk/mono/48k/20ms-16         	      40	   5594485 ns/op	  688083 B/op	    8464 allocs/op
BenchmarkPerf/encode/silk/mono/48k/20ms-16         	      36	   5558683 ns/op	  678934 B/op	    8339 allocs/op
BenchmarkPerf/encode/silk/mono/48k/20ms-16         	      38	   6116232 ns/op	  683858 B/op	    8407 allocs/op
BenchmarkPerf/decode/silk/mono/48k/20ms-16         	    3945	     58577 ns/op	   44939 B/op	      51 allocs/op
BenchmarkPerf/decode/silk/mono/48k/20ms-16         	    4537	     54145 ns/op	   44940 B/op	      51 allocs/op
BenchmarkPerf/decode/silk/mono/48k/20ms-16         	    4650	     51409 ns/op	   44939 B/op	      51 allocs/op
BenchmarkPerf/decode/silk/mono/48k/20ms-16         	    3976	     57954 ns/op	   44940 B/op	      51 allocs/op
BenchmarkPerf/decode/silk/mono/48k/20ms-16         	    3714	     56689 ns/op	   44940 B/op	      51 allocs/op
BenchmarkPerf/encode/silk/stereo/48k/20ms-16       	      22	  19619423 ns/op	 5095172 B/op	   14060 allocs/op
BenchmarkPerf/encode/silk/stereo/48k/20ms-16       	      25	  20365804 ns/op	 5178321 B/op	   14318 allocs/op
BenchmarkPerf/encode/silk/stereo/48k/20ms-16       	      19	  20143800 ns/op	 4988754 B/op	   13749 allocs/op
BenchmarkPerf/encode/silk/stereo/48k/20ms-16       	      22	  21526895 ns/op	 5095155 B/op	   14060 allocs/op
BenchmarkPerf/encode/silk/stereo/48k/20ms-16       	      22	  19513936 ns/op	 5095150 B/op	   14060 allocs/op
BenchmarkPerf/decode/silk/stereo/48k/20ms-16       	    2258	    126948 ns/op	   98305 B/op	      89 allocs/op
BenchmarkPerf/decode/silk/stereo/48k/20ms-16       	    2328	    116918 ns/op	   98306 B/op	      89 allocs/op
BenchmarkPerf/decode/silk/stereo/48k/20ms-16       	    2074	    125415 ns/op	   98307 B/op	      89 allocs/op
BenchmarkPerf/decode/silk/stereo/48k/20ms-16       	    2149	    106664 ns/op	   98308 B/op	      89 allocs/op
BenchmarkPerf/decode/silk/stereo/48k/20ms-16       	    2185	    104203 ns/op	   98305 B/op	      89 allocs/op
BenchmarkPerf/encode/hybrid/mono/48k/20ms-16       	      69	   3533735 ns/op	  674826 B/op	    4577 allocs/op
BenchmarkPerf/encode/hybrid/mono/48k/20ms-16       	      58	   3507891 ns/op	  668613 B/op	    4494 allocs/op
BenchmarkPerf/encode/hybrid/mono/48k/20ms-16       	      58	   3716669 ns/op	  668613 B/op	    4494 allocs/op
BenchmarkPerf/encode/hybrid/mono/48k/20ms-16       	      64	   3630075 ns/op	  672635 B/op	    4549 allocs/op
BenchmarkPerf/encode/hybrid/mono/48k/20ms-16       	      60	   3544840 ns/op	  670307 B/op	    4517 allocs/op
BenchmarkPerf/decode/hybrid/mono/48k/20ms-16       	    1303	    198104 ns/op	  139174 B/op	      94 allocs/op
BenchmarkPerf/decode/hybrid/mono/48k/20ms-16       	    1386	    167967 ns/op	  139167 B/op	      94 allocs/op
BenchmarkPerf/decode/hybrid/mono/48k/20ms-16       	    1461	    172437 ns/op	  139163 B/op	      94 allocs/op
BenchmarkPerf/decode/hybrid/mono/48k/20ms-16       	    1302	    195856 ns/op	  139175 B/op	      94 allocs/op
BenchmarkPerf/decode/hybrid/mono/48k/20ms-16       	    1411	    208802 ns/op	  139177 B/op	      94 allocs/op
BenchmarkPerf/encode/hybrid/stereo/48k/20ms-16     	      21	  12814533 ns/op	 3226950 B/op	   10867 allocs/op
BenchmarkPerf/encode/hybrid/stereo/48k/20ms-16     	      21	  12181419 ns/op	 3226946 B/op	   10867 allocs/op
BenchmarkPerf/encode/hybrid/stereo/48k/20ms-16     	      21	  12391829 ns/op	 3226952 B/op	   10867 allocs/op
BenchmarkPerf/encode/hybrid/stereo/48k/20ms-16     	      22	  12395818 ns/op	 3211733 B/op	   10795 allocs/op
BenchmarkPerf/encode/hybrid/stereo/48k/20ms-16     	      21	  12138771 ns/op	 3226952 B/op	   10867 allocs/op
BenchmarkPerf/decode/hybrid/stereo/48k/20ms-16     	     714	    408285 ns/op	  276990 B/op	     148 allocs/op
BenchmarkPerf/decode/hybrid/stereo/48k/20ms-16     	     727	    316587 ns/op	  276971 B/op	     148 allocs/op
BenchmarkPerf/decode/hybrid/stereo/48k/20ms-16     	     656	    371267 ns/op	  276985 B/op	     148 allocs/op
BenchmarkPerf/decode/hybrid/stereo/48k/20ms-16     	     733	    351388 ns/op	  276965 B/op	     148 allocs/op
BenchmarkPerf/decode/hybrid/stereo/48k/20ms-16     	     646	    322342 ns/op	  276993 B/op	     148 allocs/op
PASS
ok  	github.com/darui3018823/opus	69.906s
```

## Phase 3-2 Comparison: NSQ Restore Buffer Reuse

Change: `restoreFrameState` now restores saved SILK NSQ history into existing
encoder buffers when possible, while `snapshotFrameState` remains a deep copy.

Command:

```text
go test -run '^$' -bench '^BenchmarkPerf/' -benchtime=200ms -count=5 -benchmem .
```

Median comparison:

| Benchmark | Baseline ns/op | New ns/op | Time | Baseline B/op | New B/op | Allocation |
|---|---:|---:|---:|---:|---:|---:|
| `BenchmarkPerf/encode/silk/mono/48k/20ms-16` | 5594485 | 5118559 | -8.5% | 683858 | 686536 | +0.4% |
| `BenchmarkPerf/encode/silk/stereo/48k/20ms-16` | 20143800 | 18465131 | -8.3% | 5095172 | 4663431 | -8.4% |
| `BenchmarkPerf/encode/hybrid/mono/48k/20ms-16` | 3544840 | 3413251 | -3.7% | 670307 | 657077 | -2.0% |
| `BenchmarkPerf/encode/hybrid/stereo/48k/20ms-16` | 12391829 | 12004582 | -3.1% | 3226950 | 2962475 | -8.2% |

The change is confined to SILK encoder speculative state rollback, so CELT-only
and decode workloads are outside the expected effect surface.

## Phase 3-3 Comparison: Noise-Shape Scratch Reuse

Change: `analyzeNoiseShapeFLP` now reuses per-subframe scratch slices within
one analysis call for windowing, autocorrelation, Schur reflection
coefficients, and AR coefficients.

The parent commit `aef1481` was measured in a detached temporary worktree with
the same command used for the new result:

```text
go test -run '^$' -bench '^BenchmarkPerf/encode/(silk|hybrid)/stereo/48k/20ms$' -benchtime=1s -count=5 -benchmem .
```

Median comparison against parent `aef1481`:

| Benchmark | Parent ns/op | New ns/op | Time | Parent B/op | New B/op | Allocation |
|---|---:|---:|---:|---:|---:|---:|
| `BenchmarkPerf/encode/silk/stereo/48k/20ms-16` | 21135259 | 20171730 | -4.6% | 4766144 | 3156567 | -33.8% |
| `BenchmarkPerf/encode/hybrid/stereo/48k/20ms-16` | 13511848 | 13134365 | -2.8% | 2978435 | 2262515 | -24.1% |

## Phase 3-4 Comparison: Noise-Shape Input Buffer Reuse

Change: `noiseShapeAnalysisBuffer` now reuses an internal SILK encoder scratch
buffer across analysis calls and explicitly clears the prefix that was
previously zero-filled by `make`.

The parent commit `7f0456f` was measured in a detached temporary worktree with
the same command used for the new result:

```text
go test -run '^$' -bench '^BenchmarkPerf/encode/(silk|hybrid)/stereo/48k/20ms$' -benchtime=1s -count=5 -benchmem .
```

Median comparison against parent `7f0456f`:

| Benchmark | Parent ns/op | New ns/op | Time | Parent B/op | New B/op | Allocation |
|---|---:|---:|---:|---:|---:|---:|
| `BenchmarkPerf/encode/silk/stereo/48k/20ms-16` | 18640968 | 18501940 | -0.7% | 3156568 | 2776086 | -12.1% |
| `BenchmarkPerf/encode/hybrid/stereo/48k/20ms-16` | 12137100 | 12289418 | +1.3% | 2262512 | 2093297 | -7.5% |
