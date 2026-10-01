# Commit Message Rules

All commits in this repository must strictly adhere to the Conventional Commits specification with explicit prefix tags, in accordance with [`CONTRIBUTING.md`](../../CONTRIBUTING.md). AI coding assistants (including Junie, Claude, etc.) and human contributors must never commit without an approved type prefix. Unprefixed commits are mechanically blocked by the Git `commit-msg` hook.

## Required Format

```text
<type>(<scope>): <subject>
```
or (if no specific scope applies):
```text
<type>: <subject>
```

### Constraints
1. **Type prefix is mandatory**: Commits without a recognized type prefix (e.g. `Update file.go`, `Fix bug`) are **strictly forbidden**.
2. **Scope is strongly recommended**: Whenever changes target a specific package or subsystem, include the scope in parentheses.
3. **Delimiter**: Follow the prefix with a colon and a single space (`: `).
4. **Subject line**:
   - Write in imperative mood, present tense (e.g., `add`, `fix`, `mirror`, `record`, not `added`, `fixing`, `adds`).
   - Use lowercase for the starting character.
   - Do not end with a period (`.`).
   - Keep the first line under 72 characters.

---

## Allowed Types

| Type | When to use |
|---|---|
| `feat` | New user-facing or public API features |
| `fix` | Bug fixes, parity alignment with libopus, PLC/FEC fixes |
| `test` | Adding, updating, or tracing tests / benchmarks / test vectors |
| `docs` | Documentation updates, task briefs, handoffs, `.claude/` notes |
| `refactor` | Code restructuring without fixing bugs or adding features |
| `perf` | Performance optimizations |
| `build` | Build system, module dependencies, Go version updates |
| `ci` | CI workflow, GitHub Actions, or validation scripts |
| `chore` | Routine repository maintenance, cleanup, tooling adjustments |
| `style` | Formatting, whitespace, go fmt compliance (no logic changes) |
| `revert` | Reverting a previous commit |

---

## Common Scopes

Use the affected package or subsystem name as the scope whenever possible:

- `celt`: CELT encoder/decoder, PVQ, MDCT band processing, anti-collapse
- `silk`: SILK encoder/decoder, NLSF, pitch/LTP, voice activity
- `dsp`: Math, FFT, MDCT transforms, window functions
- `packet`: RFC 6716 TOC, packet parsing, framing, packing
- `api`: Public API in `opus.go`, `constants.go`, `errors.go`
- `decoder`: Decoder routing, PLC, FEC, channel synthesis
- `encoder`: Encoder mode selection, VBR rate control, bandwidth decision
- `opusref`: CGO libopus reference oracle tests and harnesses
- `oggopus`: Ogg container multiplexing and header parsing
- `multistream`: Multistream and surround channel mapping APIs
- `surround`: Surround psychoacoustic and ambisonics APIs
- `resampler`: Sample rate converter
- `entcode`: Entropy range coder and decoder

---

## Examples

### Good (Compliant)
- `fix(celt): mirror float anti-collapse synthesis`
- `test(opusref): trace mono scalar CELT stages`
- `docs: record bit-exact tv01 PCM slice`
- `feat(api): add packet inspection helpers`
- `chore: update Project_Default.xml inspection profile`

### Bad (Forbidden)
- `update files` (missing type prefix)
- `Fixed CELT bug` (missing type prefix, past tense, capitalized)
- `celt: fix synthesis` (wrong order; type must come first)
- `WIP` (missing type prefix, non-descriptive)
