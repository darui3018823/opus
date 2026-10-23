# libopus parity hunt (2026-10-19)

- Status: encoder, decoder (D1-D8) and API / multistream items fixed on
  `fix/libopus-parity-hunt` (decoder and API work cherry-picked from
  `fix/decoder-parity-hunt` and `fix/api-ms-parity-hunt`).
- Last updated: 2026-10-21.

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

| all encoder fixes | 5611 of 5690 (0 differ, 79 consistent errors) |
| encoder + decoder + API fixes | encoder 5611, decoder 5611 of 5690 (0 differ) |

The 79 errors are restricted-SILK frames shorter than 10 ms, which both
encoders reject.

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

Review follow-up (PR #36): multistream and projection integer entry points
now set the integer downmix on every stream (`fix(multistream): pass the
integer-input downmix to every stream`). Possible gap, not reproduced: a
hybrid frame after a SILK rate other than 16 kHz rebuilds the SILK encoder,
where libopus switches the rate through `silk_setup_resamplers`.

Known gap: E9's averaging with side `inputBuf` samples left by longer earlier
frames follows the C code but no available input exercised a non-zero tail.

## Decoder fixes

| Item | Commit subject |
|---|---|
| D1 | fix(celt): keep background energy of bands above a narrower end band |
| new | fix(celt): floor every band energy of a silence frame before background |
| D2, D5 | fix(decoder): conceal the hybrid FEC high band as opus_decode_frame |
| D3 | fix(decoder): decode CELT-to-SILK redundancy before the silence fade |
| D4 | fix(silk): follow the libopus has_side rule in stereo LBRR frames |
| D6 | fix(decoder): fade CELT out on a SILK FEC frame after hybrid |
| D7 | fix(decoder): follow a SILK channel switch in the FEC resamplers |
| D8 | fix(decoder): conceal FEC from a packet with a short first frame |

| new | fix(celt): run the comb post-filter on hybrid frames |

The last one was found in `TestMalformedPackets`: a SILK packet whose
trailing bytes decode as a SILK->CELT redundant frame leaves a post-filter in
the CELT state, and the next hybrid frame (no reset, `prev_redundancy`) must
fade it out; the Go decoder skipped the comb filter whenever `start != 0`.
The audit harness also drove projection PLC / FEC as unimplemented; it now
calls the real API (`test(opusref): drive projection PLC and FEC in the
decoder audit`), which fixed 8 of the 9 `TestMultistreamSurround/k2` cells.

Regression tests: `internal/audit/decoder/parity_hunt_test.go`
(`OPUS_LIBOPUS_AUDIT=1 go test ./internal/audit/decoder -run TestHunt`).
Decoder audit failures went from 138 to 37, none of them a decoding
difference:

- 24 `TestMalformedPackets` cells: `DecodeFloat32` on a zero-length packet
  returns an error, while libopus conceals the caller's buffer length; the Go
  float API has no length to conceal (API decision).
- 12 `TestSplicedVectors` cells: `Bandwidth()` before the first packet
  returns `BandwidthAuto` where libopus returns 0 (documented Go semantics).
- 1 `TestMultistreamSurround/k2` 9-channel float cell: one sample differs by
  1.1e-13 (int16 and 24-bit output match exactly); not investigated further.

## API and multistream fixes

See `docs/CURRENT_IMPLEMENTATION.md` for the caller-visible changes. Items
needing a decision:

- New `NewMultistreamEncoderWithProfile`, `NewSurroundEncoderWithProfile`,
  `NewProjectionEncoderWithProfile` (libopus create-time defaults); the old
  constructors keep complexity 5 / CBR.
- The multistream default packet buffer is now 1500 bytes per stream instead
  of 1,950,750 bytes, which also caps large VBR packets.
- Not changed: encoder `Bandwidth()` keeps its documented pre-encode
  prediction (libopus returns the last coded bandwidth); projection
  `Encode24` does not mirror the libopus `downmix_int` bug.

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
