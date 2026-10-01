# Encoder Input Pipeline: libopus Framing, Delay, and High-Pass

Status: In progress on `dev/encoder-input-pipeline` (2026-09-16); step 0
landed in `5e6482f`, step 1 (SILK look-ahead buffer) in `70cae26`, step 2
(Opus-layer delay compensation, `Lookahead()` = Fs/400 + Fs/250) in
`f7de2bf`, step 3 (hp_cutoff / dc_reject / variable_HP_smth1+2 / float-API
guard, unit-exact against the libopus float bodies) in `a04f4ce`, step 4
(instrumented libopus 1.6.1 encoder oracle + SILK int16 front end) in
`81444af`. Verified end to end: on the 8/12/16 kHz mono AB fixtures the
conditioned input, the SILK `x_buf`, `speech_activity_Q8`, and both
high-pass smoothers are bit-exact with libopus for the first frame; packet
bytes still differ there (later analysis stages), so frames after the first
are reported, not gated, until those stages are exact.
Prerequisite for encoder byte-exactness (convergence plan Phase 4) and for
exact SILK noise-shape analysis (Q3), whose windows extend into the
look-ahead.

Measured end-to-end delay after step 2 (broadband noise, 48 kHz input,
libopus decoder for both encoders): CELT-only Go 312 = libopus 312 samples;
SILK-only and hybrid Go 278 vs libopus 312. The 34-sample (0.7 ms) shortfall
is the Go SILK input resampler's group delay versus libopus'
`silk_resampler`, so the hybrid low band still leads the high band by 34
samples until the SILK API resampler is ported (see "Open items").

## Problem

The Go encoder codes each SILK frame from exactly the samples the caller
passed, with no look-ahead, no delay compensation, and no input high-pass
filter. libopus conditions and re-frames the input before either codec sees
it, so even with every analysis stage bit-exact the Go encoder would code a
different audio slice per frame and could never produce libopus' bytes.

## libopus reference behaviour (1.6.1, `src/opus_encoder.c`, `silk/`)

### Delay buffer (Opus layer)

- `st->delay_compensation = Fs/250` (4 ms) except for
  `OPUS_APPLICATION_RESTRICTED_LOWDELAY` (0). `st->encoder_buffer = Fs/100`.
- Per call: `pcm_buf = [delay_buffer tail (total_buffer samples) | conditioned
  new pcm (frame_size)]`. CELT encodes `pcm_buf[0 : frame_size]` (delayed by
  4 ms); SILK encodes `pcm_buf[total_buffer : total_buffer + frame_size]`
  (the current frame, not delayed). After the call, `delay_buffer` keeps the
  last `encoder_buffer` samples of `pcm_buf`.
- `OPUS_GET_LOOKAHEAD` = `Fs/400 + delay_compensation` = 6.5 ms.
- Comment in `opus_encoder_init`: "Delay compensation of 4 ms (2.5 ms for
  SILK's extra look-ahead + 1.5 ms for SILK resamplers and stereo
  prediction)".

### Input conditioning (Opus layer)

- `hp_freq_smth1` comes from the SILK state `variable_HP_smth1_Q15` (or the
  minimum cutoff in CELT-only mode); `variable_HP_smth2_Q15` is smoothed with
  `VARIABLE_HP_SMTH_COEF2`; `cutoff_Hz = silk_log2lin(smth2 >> 8)`.
- `OPUS_APPLICATION_VOIP`: `hp_cutoff(pcm, cutoff_Hz, out, hp_mem, ...)` (a
  second-order high-pass whose coefficients follow the cutoff).
- Other applications: `dc_reject(pcm, 3 Hz, out, hp_mem, ...)`.
- Float API: if the frame energy is not `< 1e9` or is NaN, the frame and
  `hp_mem` are zeroed.

### SILK internal look-ahead (`silk/float/encode_frame_FLP.c`)

- `x_buf = [ltp_mem_length (20 ms) | frame_length | LA_SHAPE_MS (5 ms)]`.
- New input goes to `x_frame + LA_SHAPE_MS*fs_kHz` after
  `silk_LP_variable_cutoff` (bandwidth-transition low-pass on the int16
  `inputBuf`), and eight `±1e-6f` anti-denormal offsets are added.
- The coded frame is `x_frame[0 : frame_length]`: the last 5 ms of the previous
  input followed by the first 15 ms of the current input. Pitch analysis reads
  `la_pitch` (2 ms) past the frame, LTP reads `LTP_ORDER` samples past it, and
  noise-shape analysis windows extend `la_shape` on both sides of each
  subframe.
- `silk_HP_variable_cutoff` (`silk/HP_variable_cutoff.c`) updates
  `variable_HP_smth1_Q15` from the previous frame's pitch lag and speech
  activity before each frame; the Opus layer reads it for `hp_cutoff`.
- The SILK API resamples the Opus-rate input to `fs_kHz` with the fixed-point
  resampler before `inputBuf` (`silk/enc_API.c`), and the first frame after a
  reset is unvoiced.

### VAD flags and packet header (`silk/enc_API.c`, `silk/float/encode_frame_FLP.c`)

- At `nFramesEncoded == 0` libopus reserves `(nFramesPerPacket + 1) *
  nChannelsInternal` bits with a placeholder `ec_enc_icdf` symbol, encodes the
  LBRR data, then encodes each frame after running `silk_encode_do_VAD_FLP`
  on that frame (fixed-point `silk_VAD_GetSA_Q8`; `speech_activity_Q8 <
  SILK_FIX_CONST(SPEECH_ACTIVITY_DTX_THRES, 8)` marks the frame inactive and
  drives `noSpeechCounter`/`inDTX`; an Opus-layer `VAD_NO_ACTIVITY` decision
  lowers the activity just under the threshold). When the last frame is done,
  `ec_enc_patch_initial_bits` writes the VAD and LBRR flags into the reserved
  bits.
- The Go encoder decides the per-frame VAD flags up front with a separate
  detector (`e.vad.Detect`) and runs the fixed-point VAD again inside
  `encodeRangeFrame`. For exactness the flags must come from the fixed-point
  VAD's per-frame activity, computed in frame order once, and the inactive
  path must follow `silk_encode_do_VAD_FLP`.

### Prefill on CELT→SILK switches

`silk_Encode(..., prefill=1)` on the delay buffer primes the SILK state when
the mode switches to SILK/hybrid; the Go encoder has its own transition logic
that must be reconciled when this pipeline lands.

## Open items

- **Digital-silence shortcut.** The Go encoder emits a one-byte SILK frame
  for digitally silent input without DTX; libopus codes the frame (6–8
  bytes) and its state (x_buf offsets, VAD noise floor, gains, NSQ) keeps
  evolving. After such a frame the two encoders no longer share state
  (`TestSILKEncoderInputPipelineOracle` on the `onset` fixture: x_buf differs
  by the ±1e-6 offsets, speech_activity_Q8 by 3–4). Removing the shortcut is
  a packet-policy decision for Phase 4.
- **Scoreboard reference.** `TestOpusSILKABAgainstLibopusEncoder` scores
  both encoders against the raw input. With the high-pass in place the
  achievable integer-aligned SNR of a fixture whose fundamental sits near the
  60–100 Hz cutoff depends on each encoder's cutoff trajectory (phase), so
  the SNR gaps are now a noisier instrument than the byte ratios and the
  RMS loudness; a phase-insensitive (spectral) distance is the fix.
- **SILK API resampler — done (`94938d2`).** `silk.NewEncoderResampler`
  (encoder direction of the bit-exact `silk_resampler` port) feeds SILK at
  24/48 kHz input; the equal-rate delay, `inputBuf + 1` offset and int16
  quantisation live in `internal/silk/front_end.go`. End-to-end delay equals
  libopus in every mode and the pipeline oracle is exact through the
  resampler at 24/48 kHz.
- **decide_fec bandwidth reduction.** `decide_fec` lowers the coded
  bandwidth until FEC fits when the loss exceeds 5 % and the equivalent rate
  is below the threshold; the Go encoder mirrors the LBRR_coded decision
  (`encoder_fec.go`) but its SILK bandwidth follows the input rate, so such
  streams stay at the wider bandwidth (with FEC).
- **CBR gain loop — done (`46c8703`, `8798be9`).** The
  `encode_frame_FLP` quantiser loop is ported (`encode_frame_loop.go`), the
  Opus layer sizes CBR packets to `cbr_bytes` and pads them like
  `opus_packet_pad`; CBR mono and stereo SILK packets are byte-identical
  (`TestSILKEncoderInputPipelineOracleCBR`, `TestCGOEncodeRefSILKByteExact`).
- **CELT prefill on mode switches.** libopus primes CELT with
  `tmp_prefill` (the Fs/400 samples preceding the frame from `delay_buffer`)
  when switching into CELT/hybrid; the Go transition logic does not.
- **Stereo side channel — done (`8fdbcf6`).** The side encoder now gets
  `silk_Encode`'s partial reset (shaping, NSQ, previous NLSFs, lag/gain
  history, first_frame_after_reset) and `CODE_INDEPENDENTLY_NO_LTP_SCALING`
  on the first coded side frame after a mid-only frame; stereo packets are
  byte-identical to libopus (`TestSILKEncoderStereoOracle`).
- **Stream channel decision — done (`38072d0`, `8a93b3e`).** A stereo
  input is coded as a mono SILK stream below the stereo threshold, with
  libopus' toMono transition and the mono↔stereo state hand-over
  (`TestCGOEncodeRefSILKStreamChannels`). `voice_est` follows the signal
  hint / application only: libopus' tonality analysis (complexity ≥ 7)
  is not ported, so AUTO signal at complexity ≥ 7 can decide differently.
- **Bandwidth policy — static part done (`d683ded`).** The automatic
  bandwidth decision sets the SILK internal rate before the first SILK
  packet (NB below ~9 kbps voice, WB above; `TestCGOEncodeRefSILKAutoBandwidth`).
  Not ported: mid-stream switching (`allowBandwidthSwitch`,
  `silk_control_audio_bandwidth`'s transition state machine, the sLP
  variable LP filter and the redundancy frame) — a bitrate change across a
  threshold keeps the Go rate while libopus switches — and `decide_fec`'s
  bandwidth narrowing for FEC.
- **Mode policy.** libopus picks hybrid (SWB/FB) for 24/48 kHz mono voice
  input at ≥ ~15 kbps (`mode_thresholds`, then bandwidth > WB → hybrid); the
  Go mode decision is its own heuristic and stays SILK WB there. Exactness
  needs the CELT/hybrid encoder first.

## Go implementation plan

0. **VAD flags from the fixed-point VAD.** Pre-pass the packet's frames
   through `silkVADGetSAQ8` in order, store per-frame activity/tilt/quality,
   derive the VAD flags and `inDTX` like `silk_encode_do_VAD_FLP`, and reuse
   the stored values inside the frame encode instead of re-running the VAD.
1. **SILK look-ahead buffer.** Give `internal/silk.Encoder` an `xBuf` with
   `ltp_mem + frame + la_shape` samples in silk_float scale; `EncodeMulti`
   appends the new frame at `x_frame + la_shape` and codes
   `x_frame[0:frame]`. Remove the zero padding standing in for look-ahead in
   `silkFindPitchLags` (use the real `la_pitch` window) and in the LTP
   residual, and let the noise-shape analysis read the real look-ahead.
   `pitchHist` becomes the `ltp_mem` region of `xBuf`.
2. **Opus-layer delay buffer.** Add `delayBuffer`/`encoderBuffer` to
   `opus.Encoder`; feed CELT `pcm_buf[0:frame]` and SILK the current frame for
   every application except restricted low-delay; update `Lookahead()` to
   `Fs/400 + Fs/250`. Hybrid stays aligned because SILK's 5 ms internal delay
   pairs with CELT's 4 ms delay exactly as in libopus.
3. **High-pass conditioning.** Port `hp_cutoff`, `dc_reject`, the
   `variable_HP_smth2_Q15` smoothing, and `silk_HP_variable_cutoff`, plus the
   float-API NaN/energy guard.
4. **Oracles.** Feed identical PCM to libopus (`cgoref` encoder, scalar arch)
   and Go, and compare (a) the conditioned `pcm_buf` per frame, (b) the SILK
   `x_frame` contents, and (c) per-frame VAD/pitch/LTP/NLSF indices, then
   packet bytes. Byte comparison against the *installed* libopus is only
   meaningful with a scalar (no SIMD) build; the checked-in scalar oracle
   path is the target.

## Acceptance

- Conditioned input and `x_frame` bit-exact per frame on 8/12/16 kHz SILK
  fixtures and 48 kHz hybrid fixtures.
- Decoded output of Go packets aligns with libopus decoded output with the
  same 6.5 ms look-ahead (existing delay-aligned SNR tests keep passing).
- `go test -count=1 ./...`, `go vet ./...`, `go test -count=1 -tags opusref
  ./...`, `go test -race -count=1 ./...`.

## Verification commands

```text
go test -count=1 -tags opusref -run 'TestSILKQ' -v ./internal/silk/
go test -count=1 -tags opusref -run 'TestOpusSILKABAgainstLibopusEncoder' -v .
go test -count=1 ./...
```
