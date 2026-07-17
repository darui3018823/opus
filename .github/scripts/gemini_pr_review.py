#!/usr/bin/env python3
"""Post a Gemini-generated pull request review.

The workflow that calls this script uses pull_request_target, so this script
must not checkout or execute pull request head code. It reads PR metadata and
diffs through GitHub APIs only.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import textwrap
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any


MODEL = "gemini-3.1-pro-preview"
MAX_OUTPUT_TOKENS = 65536
TRIGGER_PHRASE = "@gemini review"
DEFAULT_MAX_DIFF_CHARS = 850_000
GEMINI_ENDPOINT = (
    "https://generativelanguage.googleapis.com/v1beta/models/"
    f"{MODEL}:generateContent"
)


@dataclass
class ReviewComment:
    path: str
    line: int
    body: str
    severity: str


def run_gh(args: list[str]) -> str:
    result = subprocess.run(
        ["gh", *args],
        check=True,
        text=True,
        encoding="utf-8",
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    return result.stdout


def load_event() -> dict[str, Any]:
    event_path = os.environ.get("GITHUB_EVENT_PATH")
    if not event_path:
        raise RuntimeError("GITHUB_EVENT_PATH is not set")
    with open(event_path, "r", encoding="utf-8") as f:
        return json.load(f)


def resolve_pr_number(event_name: str, event: dict[str, Any]) -> int | None:
    if event_name == "pull_request_target":
        pull_request = event.get("pull_request") or {}
        number = pull_request.get("number")
        return int(number) if number is not None else None
    if event_name == "issue_comment":
        issue = event.get("issue") or {}
        if "pull_request" not in issue or issue.get("pull_request") is None:
            return None
        number = issue.get("number")
        return int(number) if number is not None else None
    return None


def should_run(event_name: str, event: dict[str, Any], owner: str) -> bool:
    if event_name == "pull_request_target":
        pull_request = event.get("pull_request") or {}
        author = (pull_request.get("user") or {}).get("login")
        return author == owner

    if event_name == "issue_comment":
        issue = event.get("issue") or {}
        comment = event.get("comment") or {}
        author = (comment.get("user") or {}).get("login")
        body = comment.get("body") or ""
        is_pr = "pull_request" in issue and issue.get("pull_request") is not None
        return is_pr and author == owner and TRIGGER_PHRASE in body

    return False


def fetch_pr(repo: str, pr_number: int) -> dict[str, Any]:
    fields = [
        "number",
        "title",
        "url",
        "author",
        "headRefName",
        "baseRefName",
        "headRefOid",
    ]
    raw = run_gh(["pr", "view", str(pr_number), "--repo", repo, "--json", ",".join(fields)])
    return json.loads(raw)


def fetch_diff(repo: str, pr_number: int) -> str:
    return run_gh(["pr", "diff", str(pr_number), "--repo", repo, "--patch"])


def changed_right_lines(diff: str) -> dict[str, set[int]]:
    changed: dict[str, set[int]] = {}
    current_path: str | None = None
    old_line = 0
    new_line = 0

    for raw_line in diff.splitlines():
        if raw_line.startswith("diff --git "):
            current_path = None
            continue

        if raw_line.startswith("+++ "):
            path = raw_line[4:].strip()
            if path == "/dev/null":
                current_path = None
            elif path.startswith("b/"):
                current_path = path[2:]
                changed.setdefault(current_path, set())
            else:
                current_path = path
                changed.setdefault(current_path, set())
            continue

        if raw_line.startswith("@@ "):
            match = re.search(r"@@ -(?P<old>\d+)(?:,\d+)? \+(?P<new>\d+)(?:,\d+)? @@", raw_line)
            if match:
                old_line = int(match.group("old"))
                new_line = int(match.group("new"))
            continue

        if current_path is None:
            continue

        if raw_line.startswith("+") and not raw_line.startswith("+++"):
            changed[current_path].add(new_line)
            new_line += 1
        elif raw_line.startswith("-") and not raw_line.startswith("---"):
            old_line += 1
        elif raw_line.startswith(" "):
            old_line += 1
            new_line += 1
        elif raw_line == r"\ No newline at end of file":
            continue

    return changed


def truncate_diff(diff: str) -> tuple[str, bool]:
    max_chars = int(os.environ.get("GEMINI_REVIEW_MAX_DIFF_CHARS", DEFAULT_MAX_DIFF_CHARS))
    if len(diff) <= max_chars:
        return diff, False
    return diff[:max_chars], True


def build_prompt(pr: dict[str, Any], diff: str, truncated: bool) -> str:
    author = (pr.get("author") or {}).get("login", "unknown")
    truncated_note = (
        "\nThe diff was truncated due to size. Review only the visible diff and say so in the summary."
        if truncated
        else ""
    )
    return textwrap.dedent(
        f"""
        Review this GitHub pull request diff.

        Repository: {os.environ.get("GITHUB_REPOSITORY", "")}
        PR: #{pr.get("number")} {pr.get("title", "")}
        URL: {pr.get("url", "")}
        Author: {author}
        Base: {pr.get("baseRefName", "")}
        Head: {pr.get("headRefName", "")}
        Head SHA: {pr.get("headRefOid", "")}
        {truncated_note}

        Review for useful PR feedback, including:
        - correctness bugs
        - behavioral regressions
        - missing tests for changed behavior
        - security or reliability risks
        - performance regressions when performance is relevant
        - style, readability, maintainability, documentation, or API-design issues
        - other concrete suggestions that would improve the change

        Keep comments high-signal and actionable. It is acceptable to include
        style or nit-level feedback, but avoid broad rewrites or subjective
        preferences unless the improvement is concrete.

        Return JSON only. Inline comments must use a path and line from changed
        right-side lines in the diff. Use single-line right-side comments only.
        If there are no actionable findings, return an empty comments array and
        a concise summary saying no findings or suggestions were found.

        Unified diff:
        ```diff
        {diff}
        ```
        """
    ).strip()


def gemini_schema() -> dict[str, Any]:
    return {
        "type": "object",
        "properties": {
            "summary": {"type": "string"},
            "comments": {
                "type": "array",
                "items": {
                    "type": "object",
                    "properties": {
                        "path": {"type": "string"},
                        "line": {"type": "integer"},
                        "side": {"type": "string"},
                        "severity": {"type": "string"},
                        "body": {"type": "string"},
                    },
                    "required": ["path", "line", "body"],
                },
            },
        },
        "required": ["summary", "comments"],
    }


def call_gemini(prompt: str) -> dict[str, Any]:
    api_key = os.environ.get("GEMINI_API_KEY")
    if not api_key:
        raise RuntimeError("GEMINI_API_KEY is not set")

    payload = {
        "systemInstruction": {
            "parts": [
                {
                    "text": (
                        "You are a practical code reviewer. Return concise, actionable JSON. "
                        "Include correctness, reliability, test, documentation, maintainability, "
                        "style, nit, and other useful PR feedback when it is concrete."
                    )
                }
            ]
        },
        "contents": [{"role": "user", "parts": [{"text": prompt}]}],
        "generationConfig": {
            "temperature": 0.1,
            "maxOutputTokens": MAX_OUTPUT_TOKENS,
            "responseMimeType": "application/json",
            "responseSchema": gemini_schema(),
        },
    }
    request = urllib.request.Request(
        f"{GEMINI_ENDPOINT}?key={api_key}",
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=180) as response:
            raw = response.read().decode("utf-8")
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"Gemini API failed: HTTP {exc.code}: {detail}") from exc

    data = json.loads(raw)
    parts = (
        data.get("candidates", [{}])[0]
        .get("content", {})
        .get("parts", [])
    )
    text = "".join(part.get("text", "") for part in parts)
    return parse_model_json(text)


def parse_model_json(text: str) -> dict[str, Any]:
    cleaned = text.strip()
    if cleaned.startswith("```"):
        cleaned = re.sub(r"^```(?:json)?\s*", "", cleaned)
        cleaned = re.sub(r"\s*```$", "", cleaned)
    try:
        return json.loads(cleaned)
    except json.JSONDecodeError:
        start = cleaned.find("{")
        end = cleaned.rfind("}")
        if start != -1 and end != -1 and end > start:
            return json.loads(cleaned[start : end + 1])
        raise


def normalize_review(result: dict[str, Any]) -> tuple[str, list[ReviewComment]]:
    summary = str(result.get("summary") or "").strip()
    comments: list[ReviewComment] = []
    for item in result.get("comments") or []:
        if not isinstance(item, dict):
            continue
        try:
            path = str(item["path"]).strip()
            line = int(item["line"])
            body = str(item["body"]).strip()
        except (KeyError, TypeError, ValueError):
            continue
        if not path or line <= 0 or not body:
            continue
        side = str(item.get("side") or "RIGHT").upper()
        if side != "RIGHT":
            continue
        severity = str(item.get("severity") or "finding").strip() or "finding"
        comments.append(ReviewComment(path=path, line=line, body=body, severity=severity))
    if not summary:
        summary = "No findings or suggestions were found." if not comments else "Gemini review findings."
    return summary, comments


def split_valid_comments(
    comments: list[ReviewComment], valid_lines: dict[str, set[int]]
) -> tuple[list[ReviewComment], list[ReviewComment]]:
    valid: list[ReviewComment] = []
    unposted: list[ReviewComment] = []
    for comment in comments:
        if comment.line in valid_lines.get(comment.path, set()):
            valid.append(comment)
        else:
            unposted.append(comment)
    return valid, unposted


def format_inline_body(comment: ReviewComment) -> str:
    severity = comment.severity.strip()
    if severity:
        return f"**{severity}:** {comment.body}"
    return comment.body


def append_unposted(summary: str, unposted: list[ReviewComment]) -> str:
    if not unposted:
        return summary
    lines = [
        summary.rstrip(),
        "",
        "## Unposted findings",
        "",
        "These findings could not be anchored to changed right-side lines and were not posted inline.",
    ]
    for comment in unposted:
        lines.append(f"- `{comment.path}:{comment.line}` **{comment.severity}:** {comment.body}")
    return "\n".join(lines)


def post_review(repo: str, pr_number: int, head_sha: str, body: str, comments: list[ReviewComment]) -> None:
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    if not token:
        raise RuntimeError("GH_TOKEN or GITHUB_TOKEN is not set")
    api_url = os.environ.get("GITHUB_API_URL", "https://api.github.com")
    payload: dict[str, Any] = {
        "commit_id": head_sha,
        "body": body,
        "event": "COMMENT",
        "comments": [
            {
                "path": comment.path,
                "line": comment.line,
                "side": "RIGHT",
                "body": format_inline_body(comment),
            }
            for comment in comments
        ],
    }
    request = urllib.request.Request(
        f"{api_url}/repos/{repo}/pulls/{pr_number}/reviews",
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            response.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"GitHub review post failed: HTTP {exc.code}: {detail}") from exc


def main() -> int:
    event_name = os.environ.get("GITHUB_EVENT_NAME", "")
    owner = os.environ.get("GITHUB_REPOSITORY_OWNER", "")
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    if not event_name or not owner or not repo:
        raise RuntimeError("GITHUB_EVENT_NAME, GITHUB_REPOSITORY_OWNER, and GITHUB_REPOSITORY are required")

    event = load_event()
    if not should_run(event_name, event, owner):
        print("Gemini review skipped by event guard.")
        return 0

    pr_number = resolve_pr_number(event_name, event)
    if pr_number is None:
        print("Gemini review skipped: event is not associated with a pull request.")
        return 0

    pr = fetch_pr(repo, pr_number)
    diff = fetch_diff(repo, pr_number)
    valid_lines = changed_right_lines(diff)
    review_diff, truncated = truncate_diff(diff)
    prompt = build_prompt(pr, review_diff, truncated)
    result = call_gemini(prompt)
    summary, comments = normalize_review(result)
    valid_comments, unposted = split_valid_comments(comments, valid_lines)
    body = append_unposted(summary, unposted)
    post_review(repo, pr_number, pr["headRefOid"], body, valid_comments)
    print(
        f"Posted Gemini review for PR #{pr_number}: "
        f"{len(valid_comments)} inline, {len(unposted)} unposted."
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1)
