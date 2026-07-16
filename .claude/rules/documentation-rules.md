# Documentation Rules

These rules govern Markdown documents under `.claude/`.

## Status authority

`.claude/` contains task briefs, plans, specifications, and historical working
notes. These documents provide context for a specific piece of work; they are
not the authority for the repository's current implementation status.

Before relying on an older task or plan, compare it with the current code and
`docs/CURRENT_IMPLEMENTATION.md`. The latter is the code-derived status
snapshot and takes precedence over older roadmap or README claims when they
disagree.

## Directory classification

Classify each Markdown file by its primary purpose:

```text
.claude/
├── tasks/
│   ├── implementation/  # Bounded feature, API, or bug-fix work
│   ├── investigation/   # Root-cause analysis, experiments, and measurements
│   └── testing/         # Test-harness, fuzzing, and test-robustness work
├── plans/               # Multi-phase roadmaps and sequencing decisions
├── specs/               # Detailed design and implementation contracts
├── rules/               # Repository-local operational rules
└── memory/
    ├── audits/          # Dated audit snapshots and historical assessments
    ├── handoffs/        # Dated cross-session handoff snapshots
    └── iterations/      # Completed phase or iteration decision logs
```

- Put each Markdown file in exactly one category based on its main deliverable.
- If a task includes tests, keep it with the implementation or investigation
  unless the test infrastructure itself is the deliverable.
- Put normative, reusable repository instructions in `rules/`, not in task,
  plan, specification, or memory documents.

## Naming and tracking

- Use lowercase kebab-case filenames.
- Include `YYYY-MM-DD` in dated snapshots.
- Do not add author or tool prefixes such as `codex-task-` or `claude-`.
- Keep the `.claude/` root free of Markdown files.
- Track Markdown documents in the categorized directories unless a specific
  local-only rule file is ignored by name.
- Keep tool-owned local state and credential files at their designated ignored
  paths. Do not move them into the tracked document hierarchy or copy their
  contents into tracked files, commits, logs, or user-visible command output.

## Document contents and links

- New or actively revised task briefs must state their objective, status or
  last-updated date, scope, acceptance criteria, and verification commands.
- Use repository-relative links with `/` separators.
- When moving a document, update references in the same change and do not leave
  a duplicate at the old path.

## Historical documents

- Treat superseded plans, audits, handoffs, and iteration logs as historical
  snapshots. Record supersession in the document instead of rewriting old
  measurements to look current.
- Keep completed task documents when they contain useful decisions or
  reproducer data. Put completion state in the document rather than creating an
  `archive/` directory.
- Delete a document only when its information is duplicated elsewhere and no
  longer useful.
