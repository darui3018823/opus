# AGENTS.md

## Current Project Status

Before making implementation or documentation claims, read
`docs/CURRENT_IMPLEMENTATION.md`. It is the code-derived status snapshot and
takes precedence over older roadmap or README claims when they disagree.

Before working in this repository, also read `CLAUDE.md` for repository
commands, architecture notes, and the rules for organizing and using documents
under `.claude/`.

## Contribution Guidelines (CONTRIBUTING.md)

All AI coding assistants (including Junie, Claude, etc.) and contributors must strictly adhere to [`CONTRIBUTING.md`](CONTRIBUTING.md). Never bypass repository contribution standards:

- **Formatting & Linting**: Run `go fmt ./...` and `go vet ./...` before submitting changes.
- **Testing**: Ensure `go test ./...` passes (and `go test -race ./...` where applicable).
- **Commit Messages**: Strictly follow Conventional Commits with mandatory type prefixes as outlined below and in `CONTRIBUTING.md`.

## Commit Messages

All commits in this repository must strictly use Conventional Commits with an explicit prefix (e.g. `<type>(<scope>): <subject>` or `<type>: <subject>`). Never commit without a valid type prefix. See `.claude/rules/commit-rules.md` and `CONTRIBUTING.md` for full requirements.

Allowed prefixes: `feat`, `fix`, `test`, `docs`, `refactor`, `perf`, `build`, `ci`, `chore`, `style`, `revert`. Commits without a valid prefix will be rejected by the Git `commit-msg` hook.

Examples:
- `fix(celt): separate anti-collapse energy history`
- `test(celt): add denormalized band oracle trace`
- `docs: update CELT handoff notes`
- `chore: update inspection profile`
