# Phase 1 Iteration Log

## Iteration 1: multistream and surround DecodeFEC

- Branch: `codex/feature-gaps`
- Production commit: `127ae27` (`feat(multistream): add FEC decoding`)
- Test support commit: `e7687db` (`test(opusref): add multistream FEC controls`)
- Interoperability commit: `cbe89b9`
  (`test(multistream): cross-check FEC with libopus`)
- Change: add `(*MultistreamDecoder).DecodeFEC`; `SurroundDecoder` exposes it
  through embedding. Self-delimited packets are split once, each SILK/hybrid
  stream uses its existing decoder FEC path, and CELT current/previous modes
  use PLC as libopus does. Mapping 255 and duplicate mappings are preserved,
  and caller PCM is copied only after every stream succeeds.
- Tests: Go SILK/LBRR round trip, mixed SILK/CELT fallback, CELT-to-SILK
  previous-mode fallback, output preservation on error, promoted surround API,
  and libopus multistream encode to Go FEC decode. The deterministic libopus
  fixture produced sample-identical Go/libopus FEC output (`+Inf dB` SNR).
- Validation: `go vet ./...`, `go test -count=1 ./...`, and
  `go test -count=1 -tags opusref ./...` all passed on 2026-07-16.
- Decision: adopted. The feature adds only a new public method and test-only
  libopus wrappers; existing normal decode and packet framing paths are
  unchanged.

## Iteration 2: multistream and surround DecodePLC

- Branch: `codex/feature-gaps`
- Production/test commit: `e7e8e84` (`feat(multistream): add PLC decoding`)
- Change: add `(*MultistreamDecoder).DecodePLC`; `SurroundDecoder` exposes it
  through embedding. The method validates frame size and output capacity before
  touching child state, conceals every elementary stream through its existing
  decoder, applies duplicate/silent mappings, and copies caller PCM only after
  all children succeed.
- Tests: mono SILK and coupled CELT concealment, mixed coupled-CELT/mono-SILK
  repeated loss with non-zero monotonic energy decay, mapping 255 and duplicate
  channels, normal-decode recovery continuity, validation state preservation,
  child failure output preservation, and promoted surround API.
- Validation: `go vet ./...`, `go test -count=1 ./...`, and
  `go test -count=1 -tags opusref ./...` all passed on 2026-07-16.
- Decision: adopted. The method adds a public aggregation path over existing
  PLC implementations without changing normal packet decode behavior.

## Iteration 3: Ogg Opus granule-position seeking

- Branch: `codex/feature-gaps`
- Duration prerequisite: `b8fba6b`
  (`feat(multistream): expose packet duration`)
- Sequential timing commit: `d4a076d`
  (`feat(oggopus): expose packet timing trims`)
- Seek commit: `4a581b6` (`feat(oggopus): add granule-based seeking`)
- API correction: `75c8875` (`fix(oggopus): use vet-safe seek API name`)
- Review fix: `9fdcbb7` (`fix(oggopus): preserve seek stream identity`)
- Change: `Reader.NextPacket` now supplies 48 kHz duration and discard metadata
  for pre-skip and EOS granule trimming. `Reader.SeekPCM` uses CRC-validated Ogg
  page bisection on `io.ReadSeeker`, starts at least 3840 samples before the
  target, discards orphaned continued-packet prefixes when resynchronizing, and
  marks decoder pre-roll through `DiscardStart`. Non-seekable and out-of-range
  requests return stable sentinel errors without changing reader state.
- API note: the roadmap's provisional `Seek(sample)` name was changed to
  `SeekPCM(sample)` because `go vet` requires methods named `Seek` to implement
  the standard `io.Seeker` signature.
- Tests: pre-skip spanning packets, final-page trim spanning packets,
  multistream packet duration, invalid granules, seek start/interior/page
  boundary/end/restart, failed-seek state preservation, non-seekable input,
  continued-page orphan resynchronization, and seek-time serial/sequence
  validation. An independent review found and the fix covers RFC-valid initial
  continued audio pages and stream identity loss on `SeekPCM(0)`.
- Validation: `go vet ./...`, `go test -count=1 ./...`, and
  `go test -count=1 -tags opusref ./...` all passed on 2026-07-16.
- Decision: adopted. Existing sequential packet data and page metadata remain
  unchanged; the new timing fields are additive.

## Iteration 4: Ogg Opus chained logical streams

- Branch: `codex/feature-gaps`
- Production/test commit: `b7f3f94`
  (`feat(oggopus): read chained logical streams`)
- Review fix: `d1d6375`
  (`fix(oggopus): validate chained EOS boundaries`)
- Change: after a logical EOS, `Reader` creates a fresh `PacketReader`, validates
  the next link's OpusHead/OpusTags, resets granule and pre-skip state, and
  continues automatically. `Reader.Link()` and `Packet.LinkIndex` expose link
  transitions so callers can rebuild decoders when channels or mapping change.
  `SeekPCM` remains relative to the current link and locates that serial's EOS
  boundary by bisection instead of treating the physical file end as the link
  end.
- Tests: three-link concatenation with changing metadata/channels/serials,
  inline and empty EOS pages, sticky physical EOF, sticky malformed next
  headers, current-link start/end seeking, automatic advancement after end
  seek, restart within a later link, seek after physical EOF, truncated EOS
  packets, mismatched empty-EOS granules, sticky audio validation errors, and
  reused chained serials. Independent review identified the four latter
  corruption/identity cases before closeout.
- Validation: `go vet ./...`, `go test -count=1 ./...`, and
  `go test -count=1 -tags opusref ./...` all passed on 2026-07-16.
- Decision: adopted. `PacketReader` remains a strict single-logical-stream
  primitive; chaining is isolated in the higher-level Ogg Opus `Reader`.

## Iteration 5: expert frame duration

- Branch: `codex/feature-gaps`
- Reference-wrapper commit: `60824f0`
  (`test(opusref): wrap expert frame duration`)
- Single-stream production/test commit: `4faf143`
  (`feat(encoder): add expert frame duration`)
- Aggregate integration commit: `d5f78ea`
  (`feat(multistream): propagate expert frame duration`)
- Interoperability commit: `66216a6`
  (`test(opusref): cross-check expert frame duration`)
- Change: add `ExpertFrameDuration` choices for argument-selected and fixed
  2.5/5/10/20/40/60/80/100/120 ms packets. In fixed mode, Encode's
  `frameSize` describes available samples per channel and only the selected
  prefix is consumed. The setting survives reset and propagates to forced-mono,
  multistream, surround, and projection encoders. Surround and projection rate
  allocation use the selected duration rather than the available input length.
- Tests: all supported rates and durations, every PCM API, arbitrary and short
  availability, full-buffer validation, default/invalid/reset behavior,
  argument-mode byte/final-range identity, unconsumed-tail continuity,
  forced-mono propagation, aggregate encoder propagation/rate allocation, and
  libopus 1.6.1 SET/GET plus packet-duration parity for all fixed choices.
- Validation: `go vet ./...`, `go test -count=1 ./...`, and
  `go test -count=1 -tags opusref ./...` all passed on 2026-07-16.
- Decision: adopted. Argument mode preserves existing packet bytes and final
  range; fixed mode is reachable only through the new public control.

## Phase 1 closeout

Required iterations 1-1 through 1-5 are complete and all three repository
gates pass. Optional iteration 1-6 (multiplexed Ogg demux and custom projection
encoder matrices) remains out of scope by default pending explicit approval.

An independent final review found four uncovered boundary cases, fixed before
closeout:

- `acd4c2b` rejects per-stream expert-duration divergence before any elementary
  encoder advances, including surround/projection rate preparation.
- `85191df` prevents seek page scans from reading or accepting pages beyond the
  current chained-link boundary.
- `7d8ebb0` treats physical EOF before a logical EOS page as a sticky invalid
  Ogg Opus stream instead of clean EOF.
- `85eb9bb` preflights every multistream PLC/FEC child's deterministic state
  requirements before any child decoder advances.

After these fixes, `go vet ./...`, `go test -count=1 ./...`, and
`go test -count=1 -tags opusref ./...` all passed again on 2026-07-16.
