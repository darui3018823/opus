# libopus parity hunt (2026-10-19)

- Status: encoder items fixed on `fix/libopus-parity-hunt`; decoder (D1-D8)
  and API / multistream items in progress on separate branches.
- Last updated: 2026-10-20.

## Objective

Find and fix every byte-level difference between this encoder/decoder and
plain-C libopus 1.6.1 (gcc -O2, no SIMD) on real audio, starting from v1.7.1.

## Scope

- Encoder: single-stream `opus_encode` / `opus_encode24` / `opus_encode_float`
  paths under the libopus mode policy, all applications, 8-48 kHz, mono and
  stereo, CBR / CVBR / VBR, complexity 0-10, 2.5-120 ms, FEC, DTX, bandwidth
  forcing, max payload, bitrate sweeps.
- Decoder and API / multistream items are tracked here but fixed on their
  own branches.

## Method

- An `opus_demo -e/-d` mirror in Go compared byte for byte with
  `opus_demo` built from plain-C libopus over 5690 cells (14 real tracks,
  576 of them 24-bit or float input). The matrix and tracks live outside the
  repository.
- Differences were traced to the first diverging value with instrumented
  copies of libopus and of this encoder.
- Every fix has an oracle regression test in
  `libopus_parity_hunt_opusref_test.go` (plain-C audit oracle, see
  `scripts/oracle/build_encoder_audit.ps1`) or a unit test, checked to fail
  without the fix where the inputs in `testdata` trigger it.

## Results

| Run | Encoder cells identical |
|---|---|
| v1.7.1 | 4613 of 5114 (501 differ) |
| first fixes (E1, E4, E5 debug, E6-E8, E10, E11) | 5507 of 5690 (99 differ) |
| `fix/libopus-parity-hunt` before E14 and the restricted-SILK fixes | 5595 of 5690 (16 differ, 79 consistent errors) |

The 79 errors are restricted-SILK frames shorter than 10 ms, which both
encoders reject. The remaining 16 cells of that run were then fixed (see
below); a full rerun on the final commit is recorded in the PR.

## Encoder fixes

| Item | Cause | Commit subject |
|---|---|---|
| E1 | hybrid `to_celt` did not make CELT the previous mode without redundancy | fix(encoder): make CELT the prev mode after a hybrid to_celt packet |
| E2, E5 | no mid-stream SILK rate switch (`opusCanSwitch` persistence, `silk_setup_resamplers`, `silk_setup_fs`); no `maxInternalSampleRate` cap | fix(silk): switch the internal rate mid-stream like libopus |
| E3 | side channel ran the bandwidth state machine on the first stereo packet | fix(silk): skip the side bandwidth control on the first stereo packet |
| E4 | theta RDO dropped `lowband_scratch` on the last band | fix(celt): keep the lowband scratch for the last band under theta RDO |
| E6, E8 | prefill skipped `silk_setup_LBRR`; LBRR usage not reset per frame | fix(silk): set up LBRR in the prefill and reset LBRR usage per frame |
| E7 | LBRR threshold 76 instead of 77 | fix(silk): use 77 as the LBRR speech activity threshold |
| E9 | stereo-to-mono prefill did not average with the stale side channel | fix(silk): average a stereo-to-mono prefill with the stale side channel |
| E10 | CELT hybrid decisions keyed on the shared coder, not `start != 0` | fix(celt): take the hybrid decisions from a nonzero start band |
| E11 | negative packet equivalent rate treated as unset | fix(encoder): use a negative packet equiv rate for the CELT stereo width |
| E12 | CBR multi-frame padding with P = 255k+1 fell back to an unpadded packet | fix(packet): pad CBR multi-frame packets with the libopus padding run |
| E13 | (was the prefill delay line, see below) | fix(silk): restart the prefill input delay line from zeros |
| E14 | analysis downmix capped integer input at +6 dBFS | fix(encoder): skip the analysis downmix cap for integer input |
| new | LBRR copied the quantiser loop's lowered quant offset | fix(silk): keep the pre-loop quant offset for the LBRR frame |
| new | hybrid prefill ran with the previous end band | fix(encoder): set the CELT end band before the hybrid prefill |
| new | stereo prefill lost the restored side LP state | fix(silk): keep the restored side LP state on a stereo prefill |
| new | restricted SILK coded switch redundancy | fix(encoder): code no switch redundancy under restricted SILK |
| new | equal-rate x_buf resampling skipped the decoder resampler delay | fix(silk): delay x_buf through the equal-rate resampler on a switch |
| new | rate switch kept the previous packet's LBRR | fix(silk): drop the pending LBRR on an internal rate switch |
| new | mono-to-stereo rate switch seeded the side resampler too early | fix(silk): copy the switched mid resampler on a stereo rate switch |
| new | stereo-to-mono averaging covered the whole packet | fix(encoder): average only the first frame after a stereo-to-mono switch |

Known gap: E9's averaging with side `inputBuf` samples left by longer earlier
frames follows the C code but no available input exercised a non-zero tail.

## Decoder and API items (other branches)

D1 `backgroundLogE` across bandwidth switches, D2 hybrid FEC after SILK-only,
D3 redundancy / fade order, D4 stereo FEC `has_side`, D5 FEC end band, D6 SILK
FEC after hybrid, D7 FEC channel-count switch, D8 FEC from a <= 1-byte frame;
API: `SetMaxPacketSize(1)` at >= 60 ms, FEC with an empty packet, stale
`FinalRange`, frame-size error codes, `SetPacketLossPerc` range, `Bandwidth()`,
restricted-application CTLs; multistream: default CBR / complexity, CBR at
`BitrateMax`, projection create error, expert duration, DecodeFEC with an
empty packet, projection `Encode24`.

## Acceptance criteria

- Every hunt test passes against the plain-C audit oracle.
- `go test ./...` and `go test -tags opusref ./...` pass.
- The matrix shows no encoder difference other than consistent errors.

## Verification

```powershell
pwsh scripts/oracle/build_encoder_audit.ps1
go test -tags opusref -run TestHunt -count=1 .
go test -tags opusref -timeout 60m -count=1 ./...
go test -count=1 ./...
```
