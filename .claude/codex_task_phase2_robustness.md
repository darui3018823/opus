# Codex Task: Phase 2 Robustness

Source: `.claude/plan_pure_go_completeness_2026-07-16.md` Phase 2.
Status authority: `docs/CURRENT_IMPLEMENTATION.md`.
Integration branch: `codex/robustness`, stacked on the completed
`codex/feature-gaps` branch.

## Iterations

1. Add bounded stateful single-stream decoder sequence fuzzing for normal
   decode, PLC, FEC, reset, gain, and phase-inversion controls.
2. Add bounded Ogg Opus Writer-to-Reader fuzzing for timing, chaining,
   continuation, corruption, and seek behavior.
3. Add encoder adversarial-input and setter-sequence fuzzing, including
   non-finite float PCM.
4. Add local-only `opusref` differential decoder fuzzing.
5. Promote every discovered crash or behavioral divergence to a committed
   regression seed and fix one root cause per commit.

Each adopted target must have deterministic seeds, explicit operation and
allocation bounds, invariants stronger than no-panic, nightly amd64/arm64 CI
coverage, and a 30-minute local zero-crash qualification run. Run the normal
repository gates after every iteration and leave the branch unpushed.

