# Encoder Input Pipeline: libopus Framing, Delay, and High-Pass

Status: Draft (2026-09-15). Prerequisite for encoder byte-exactness (convergence
plan Phase 4) and for exact SILK noise-shape analysis (Q3), whose windows
extend into the look-ahead.

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
