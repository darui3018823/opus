# Rules Index

Read this index before repository work, then read every rule that applies to
the task. Tracked rules are shared repository guidance; local rules are ignored
by exact filename and remain specific to this workspace.

Local / ignored rules are optional workspace overrides and apply only when the
listed file exists. If an ignored rule file is absent, treat that rule as if it
does not exist: do not block work, warn about the missing file, recreate it,
infer its contents, or recover it from Git history.

| Rule | Tracking | Applies to |
|---|---|---|
| `documentation-rules.md` | Tracked | Status authority, `.claude/` classification, naming, links, and document lifecycle |
| `webhook-rules.md` | Local / ignored | Discord notification payload and identity handling |
| `user-preferences.md` | Local / ignored | User-specific commit, escalation, and SubAgent preferences |

Local credential values remain in `.claude/webhooks.local.json`, not in any
tracked rule or index.
