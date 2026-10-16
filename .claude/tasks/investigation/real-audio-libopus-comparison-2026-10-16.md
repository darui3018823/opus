# Real-Audio Comparison With libopus (2026-10-16)

- Status: complete; corrected and extended the same afternoon (see
  "Correction and multi-genre follow-up" at the end)
- Commit measured: `af80912` on `perf/decoder-encoder-optimization`
- Related: [performance brief](../implementation/performance-optimization-2026-10-15.md),
  [PERF_BASELINE](../../../docs/PERF_BASELINE.md)

## Objective

Check how the pure Go codec compares with a real (SIMD-enabled) libopus
build on real-world audio rather than test vectors: decoder agreement,
encoder quality at matched settings, bitstream agreement, speed, CPU time
and memory.

## Scope

- Source: a 280.16 s stereo vocal-cover track from YouTube (video
  `K6b4iYrLFLA`), fetched as format 251 (YouTube's own Opus, ~130 kb/s,
  48 kHz) with yt-dlp 2026.08.19 and Firefox cookies.
- libopus: the libopus linked into ffmpeg 8.1.2 (gyan.dev full build), used
  through `-c:a libopus` for both encoding and decoding
  (`-request_sample_fmt s16` so libopus produces the int16 output itself).
- This project: `opus.Decoder.Decode`, `opus.Encoder.Encode` and `oggopus`
  through a small harness (`ytcmp`: `dec`, `enc`, `cmp`, `pkts`).
- Encoder settings on both sides: VBR, complexity 10, application audio,
  20 ms frames, 32 / 64 / 128 kb/s. The encoder input is libopus's decode of
  the YouTube stream ("source").
- Machine: i7-11700, Windows 11, about 4.4 GB of free RAM (IDEs left
  running). Each timing is the median of three runs.

Not in scope: listening tests or perceptual metrics (PEAQ/ViSQOL); waveform
SNR here measures how close the two implementations are, not perceived
quality.

## Results

### Decoder

| Input stream | Identical samples | Max diff | SNR |
|---|---:|---:|---:|
| YouTube's Opus stream | 99.925% | 1 LSB | 110.9 dB |
| libopus-encoded (32/64/128k) | 99.92% | 1 LSB | 110.9 dB |
| Go-encoded (32/64/128k) | 99.93% | 1 LSB | 110.9 dB |

The ±1 LSB residue is SIMD vs scalar float rounding. Every quality metric
below is identical to three decimals whichever decoder is used.

### Encoder quality (decoded vs source)

LSD is the RMS log-spectral distance in dB per band (2048-point frames,
mid channel).

| Rate | Encoder | Avg rate | SNR | SegSNR | LSD 0-4k | 4-12k | 12-16k | 16-20k |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| 32k | libopus | 30.8 kb/s | 10.87 | 10.99 | 6.71 | 9.61 | 10.39 | 11.77 |
| 32k | Go | 31.7 kb/s | 10.99 | 11.03 | 6.62 | 9.60 | 10.43 | 11.76 |
| 64k | libopus | 62.0 kb/s | 14.14 | 14.31 | 4.95 | 7.76 | 9.58 | 11.56 |
| 64k | Go | 62.9 kb/s | 14.19 | 14.24 | 4.91 | 7.66 | 9.56 | 11.57 |
| 128k | libopus | 125.5 kb/s | 22.14 | 22.87 | 1.98 | 2.10 | 3.25 | 6.08 |
| 128k | Go | 125.4 kb/s | 21.55 | 21.88 | 2.17 | 2.35 | 3.63 | 6.70 |

At 32k and 64k the two match (Go spends 1.4% to 2.8% more bits). At 128k Go
is 0.6 dB lower in SNR at the same size; worth a look as a follow-up.

### Bitstream agreement

| Rate | Same TOC | Identical packets | Same length | Modes (both) |
|---|---:|---:|---:|---|
| 32k | 100% | 3.1% | 6.0% | CELT 13976 / Hybrid 12 / SILK 20 |
| 64k | 100% | 3.2% | 6.2% | CELT 14008 |
| 128k | 100% | 2.9% | 5.2% | CELT 14008 |

Mode and bandwidth decisions agree on all 14,008 packets. Byte agreement is
low because ffmpeg's libopus runs SIMD float kernels whose rounding differs
from the plain-C libopus 1.6.1 this encoder matches byte for byte (the
opusref oracle tests); the difference enters CELT quantization and VBR
spreads it.

### Speed and resources (280 s track)

| Job | Wall | CPU | x realtime | Peak WS | Go alloc / GCs |
|---|---:|---:|---:|---:|---|
| decode YouTube, libopus | 0.78 s | 1.08 s | 361 | 20.2 MB | - |
| decode YouTube, Go | 1.25 s | 1.22 s | 225 | 14.7 MB | 37 MB / 13 |
| encode 32k, libopus | 1.53 s | 1.75 s | 184 | 19.7 MB | - |
| encode 32k, Go | 3.14 s | 3.12 s | 89 | 16.6 MB | 361 MB / 115 |
| encode 64k, libopus | 1.71 s | 1.95 s | 164 | 19.6 MB | - |
| encode 64k, Go | 3.70 s | 3.68 s | 76 | 15.5 MB | 422 MB / 147 |
| encode 128k, libopus | 2.05 s | 2.28 s | 137 | 19.6 MB | - |
| encode 128k, Go | 4.63 s | 4.63 s | 61 | 16.4 MB | 607 MB / 214 |
| decode Go 64k stream, libopus | 0.61 s | 0.89 s | 462 | 20.6 MB | - |
| decode Go 64k stream, Go | 1.00 s | 0.96 s | 281 | 15.2 MB | 29 MB / 9 |
| ffmpeg raw copy (no codec) | 0.09 s | 0.16 s | - | 18.7 MB | - |

- Go decodes about 1.6x and encodes about 2.1x to 2.3x slower than SIMD C
  libopus. Per 20 ms frame that is about 89 µs vs 56 µs to decode and
  264 µs vs 122 µs to encode at 64k, i.e. about 0.4% and 1.3% of one core
  in real time. The gap matters for batch transcoding and for servers with
  many concurrent streams (about 75 vs 160 real-time 64k encodes per core),
  not for a single playback or call.
- Peak working set is lower for Go; the libopus figures include ffmpeg's
  own ~19 MB footprint.
- The encoder still allocates tens of KB per frame (115 to 214 GCs per
  track): the next optimization target.
- CPU time is TSC cycles from `QueryProcessCycleTime` divided by 2.5 GHz.
  On this machine `GetProcessTimes` undercounts (a 1 s busy loop reports
  0.33 s), so it was not used. ffmpeg's CPU exceeds its wall time because
  of its I/O thread.

## Follow-ups

1. Reduce the remaining encoder allocations (GC count above).
2. Look into the 0.6 dB SNR gap at 128 kb/s CELT; check against the
   plain-C libopus oracle first to see whether it is the SIMD rounding or
   a real difference.
3. Repeat with a speech source to exercise SILK and hybrid, which this
   music track barely used (32 of 14,008 packets at 32k).

## Acceptance criteria

The decoder agrees with libopus within ±1 LSB on real audio, and encoder
quality is within about 1 dB SNR of libopus at matched bitrates. Both hold.

## Verification commands

The harness, scripts and outputs are kept outside the repository at
`D:/ShareFiles/yt-dlp/others/2026-10-16_opus-compare_K6b4iYrLFLA/`
(`tools/`, `metrics/`, `plots/`, `README.md`). With `ytcmp` built from
`tools/` (its `go.mod` points at this repository with `replace`):

```sh
ffmpeg -i src.webm -c copy src.opus
ffmpeg -request_sample_fmt s16 -c:a libopus -i src.opus -f s16le -ac 2 -ar 48000 dec_libopus.raw
ytcmp dec src.opus dec_ours.raw
ytcmp cmp dec_libopus.raw dec_ours.raw "decoder agreement"
ytcmp enc dec_libopus.raw enc_ours_64.opus 64
ffmpeg -f s16le -ar 48000 -ac 2 -i dec_libopus.raw -c:a libopus -b:a 64k -vbr on \
  -compression_level 10 -application audio -frame_duration 20 enc_libopus_64.opus
ytcmp pkts enc_libopus_64.opus enc_ours_64.opus "bitstream agreement"
pwsh tools/measure.ps1   # timing, CPU cycles and peak memory
```

## Correction and multi-genre follow-up (2026-10-16 afternoon)

The encoder comparison above compared different rate-control modes. ffmpeg's
`-vbr on` runs libopus with unconstrained VBR (`OPUS_SET_VBR_CONSTRAINT(0)`),
while this encoder defaults to constrained VBR, like libopus itself. The
3% packet agreement and the -0.6 dB at 128 kb/s came mostly from that, not
from SIMD rounding. With `SetVBRConstraint(false)` on the Go side, the same
track gives 128 kb/s dSNR +0.01 dB and 65.3% identical packets.

Rerun on nine YouTube sources (J-POP, rock, EDM, classical, jazz, acoustic
guitar, a TED talk and two ASMR recordings, one of them 1684 s in full), at
16/32/64/128 kb/s, unconstrained VBR on both sides, HEAD `1bdf41f`:

| Measure | Result |
|---|---|
| Decoder agreement (81 pairs) | max diff 1 LSB everywhere; 99.92% to 99.997% identical samples |
| Encoder dSNR, Go - libopus (36 cells) | min -0.03, median 0.00, max +0.25 dB |
| Average-rate difference | 0% to +1.5% |
| Identical packets | min 1.3%, median 76.7%, max 99.7% (remaining divergence: SIMD float rounding entering CELT quantization) |
| Mode (TOC) agreement | 100% in all 36 cells |
| Determinism (two encodes) | 36/36 byte-identical |
| Time vs libopus (median over tracks) | decode 1.70x, encode 2.09x to 2.25x |
| Peak working set | Go 14.9-18.8 MB, ffmpeg+libopus 19.5-24.0 MB; flat over the 1684 s track |

Same-machine decoder comparison with pion/opus (`44637de`), 12 official
vectors: 980 ms vs 925 ms in total (1.06x; 2.8x before this branch); Go is
faster on tv02-tv06 and tv12, 1.1x to 1.27x slower on the CELT vectors.

New follow-up, ahead of the ones above: `Encoder.Encode` and `Encode24`
allocate a `[]float64` conversion buffer per call (15 KB per 20 ms stereo
frame). The benchmarks use `EncodeFloat` and miss it; it accounts for most
of the 300-600 MB per track the harness saw.

The full Japanese report, charts, metrics (including the constrained-VBR
run) and tools are in
`D:/ShareFiles/yt-dlp/others/2026-10-16_opus-perf-and-real-audio-report/`.

Follow-ups the same day: the `Encode` allocation is fixed in `9dc3e79`
(byte-identical on all 36 cells); five lossless FLAC tracks give dSNR
-0.00..+0.03 dB and a median 88% identical packets; concurrent encoding
scales at 80% efficiency up to the 8 physical cores. Section 9 of the
Japanese report has the tables.

Plain-C comparison (same day): libopus 1.6.1 `opus_demo` built twice with
gcc 16.1 -O2, plain C (no intrinsics/RTCD) and x86 SIMD (SSE..AVX2 with
RTCD). Against plain C the Go encoder is byte-identical on 45 of 52 cells
(19/20 lossless, 26/32 YouTube); the SIMD build agrees on 49% to 99%. The
seven differing cells are parity-bug candidates, outside this
output-preserving branch: Endorfin 64k (diverges at packet 10, right after
a CELT FB -> SWB bandwidth switch at packet 9), jazz 64k (from packet
145), jazz 32k/128k (1-4 packets from 7259, then identical again), ASMR
short 16k/32k. Time ratios (median of four sources): Go / plain C is 1.62
to 1.69 for encode and 1.63 for decode; plain C / SIMD is 1.23 to 1.34
for encode and 1.05 for decode. So the decode gap is almost all Go vs C,
and the encode gap vs SIMD libopus is about 1.65 (Go) x 1.3 (SIMD).
