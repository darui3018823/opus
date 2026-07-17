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
import base64
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any


MODEL = "gemini-3.1-pro-preview"
MAX_OUTPUT_TOKENS = 65536
TRIGGER_PHRASE = "@gemini review"
DEFAULT_MAX_DIFF_CHARS = 850_000
MAX_TOOL_ROUNDS = 8
MAX_TOOL_RESPONSE_CHARS = 120_000
MAX_FILE_CHARS = 100_000
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


@dataclass
class ToolContext:
    repo: str
    pr_number: int
    pr: dict[str, Any]
    diff: str
    changed_files: list[str]


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


def run_readonly(args: list[str]) -> tuple[int, str, str]:
    result = subprocess.run(
        args,
        check=False,
        text=True,
        encoding="utf-8",
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    return result.returncode, result.stdout, result.stderr


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


def changed_files_from_diff(diff: str) -> list[str]:
    files: list[str] = []
    seen: set[str] = set()
    for raw_line in diff.splitlines():
        if not raw_line.startswith("+++ "):
            continue
        path = raw_line[4:].strip()
        if path == "/dev/null":
            continue
        if path.startswith("b/"):
            path = path[2:]
        if path and path not in seen:
            files.append(path)
            seen.add(path)
    return files


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


def extract_file_diff(diff: str, path: str) -> str:
    chunks: list[str] = []
    current: list[str] = []
    in_file = False
    target_markers = {f"+++ b/{path}", f"+++ {path}"}

    for line in diff.splitlines():
        if line.startswith("diff --git "):
            if in_file and current:
                chunks.extend(current)
                break
            current = [line]
            in_file = False
            continue
        if current:
            current.append(line)
        if line in target_markers:
            in_file = True

    if in_file and current and not chunks:
        chunks.extend(current)
    return "\n".join(chunks)


def safe_path(path: str) -> str:
    cleaned = path.strip().replace("\\", "/")
    if not cleaned or cleaned.startswith("/") or "\x00" in cleaned:
        raise ValueError("path must be a relative repository path")
    parts = [part for part in cleaned.split("/") if part]
    if any(part == ".." for part in parts):
        raise ValueError("path must not contain '..'")
    return "/".join(parts)


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

        Review this like an exacting senior reviewer doing a careful pass before
        merge. Actively hunt for small problems, nits, style issues, confusing
        wording, maintainability hazards, missing tests, and suspicious edge
        cases. A zero-comment review should be rare; return no comments only
        after you have checked the changed files and nearby context and still
        cannot find any concrete improvement.

        Use the available read-only tools aggressively when they are present.
        At minimum, inspect the changed file list and per-file diffs. Use file
        reads or repository searches for context before deciding that something
        is correct. You may use the read-only command tool only for inspection;
        do not ask to run untrusted PR code.

        Available tools include:
        - list_changed_files: see every changed path.
        - get_diff_for_file: inspect individual file diffs.
        - get_file: read changed PR files or base-branch files.
        - search_diff: find patterns in the full PR diff.
        - run_readonly_command: inspect with whitelisted gh/git commands such
          as gh_pr_view, gh_pr_diff, git_grep, and git_ls_files.

        Review for useful PR feedback, including:
        - correctness bugs
        - behavioral regressions
        - missing tests for changed behavior
        - security or reliability risks
        - performance regressions when performance is relevant
        - style, readability, maintainability, documentation, or API-design issues
        - typos, stale comments, awkward docs, unclear examples, brittle tests,
          questionable CI configuration, and other nit-level issues
        - other concrete suggestions that would improve the change

        Prefer concrete inline comments over a generic summary. It is acceptable
        and expected to include style or nit-level feedback. Avoid broad rewrites
        or subjective preferences unless the improvement is concrete.

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


def tool_declarations() -> list[dict[str, Any]]:
    return [
        {
            "name": "list_changed_files",
            "description": "List files changed by the pull request.",
            "parameters": {"type": "object", "properties": {}},
        },
        {
            "name": "get_diff_for_file",
            "description": "Return the unified diff for one changed file.",
            "parameters": {
                "type": "object",
                "properties": {"path": {"type": "string"}},
                "required": ["path"],
            },
        },
        {
            "name": "get_file",
            "description": (
                "Read a repository file without executing code. Use ref='head' "
                "for the PR version or ref='base' for the base branch version."
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "ref": {"type": "string", "enum": ["head", "base"]},
                },
                "required": ["path"],
            },
        },
        {
            "name": "search_diff",
            "description": "Search the pull request diff with a regular expression.",
            "parameters": {
                "type": "object",
                "properties": {
                    "pattern": {"type": "string"},
                    "max_matches": {"type": "integer"},
                },
                "required": ["pattern"],
            },
        },
        {
            "name": "run_readonly_command",
            "description": (
                "Run a whitelisted read-only inspection command in the base checkout. "
                "Allowed commands: gh_pr_view, gh_pr_diff, git_grep, git_ls_files."
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "command": {
                        "type": "string",
                        "enum": ["gh_pr_view", "gh_pr_diff", "git_grep", "git_ls_files"],
                    },
                    "pattern": {"type": "string"},
                    "pathspec": {"type": "string"},
                },
                "required": ["command"],
            },
        },
    ]


def trim_tool_response(value: Any) -> dict[str, Any]:
    text = json.dumps(value, ensure_ascii=False)
    if len(text) <= MAX_TOOL_RESPONSE_CHARS:
        return value if isinstance(value, dict) else {"result": value}
    return {
        "truncated": True,
        "result": text[:MAX_TOOL_RESPONSE_CHARS],
    }


def get_file_from_github(ctx: ToolContext, path: str, ref_name: str) -> dict[str, Any]:
    clean_path = safe_path(path)
    if ref_name == "head" and clean_path not in ctx.changed_files:
        return {"error": "head file reads are limited to changed files", "path": clean_path}
    ref = ctx.pr["headRefOid"] if ref_name == "head" else ctx.pr["baseRefName"]
    quoted_path = urllib.parse.quote(clean_path, safe="/")
    quoted_ref = urllib.parse.quote(str(ref), safe="")
    try:
        raw = run_gh(["api", f"repos/{ctx.repo}/contents/{quoted_path}?ref={quoted_ref}"])
    except subprocess.CalledProcessError as exc:
        return {
            "error": "file could not be read",
            "path": clean_path,
            "ref": ref_name,
            "stderr": exc.stderr,
        }
    data = json.loads(raw)
    if data.get("type") != "file":
        return {"error": "path is not a file", "path": clean_path, "ref": ref_name}
    encoded = data.get("content") or ""
    content = base64.b64decode(encoded).decode("utf-8", errors="replace")
    truncated = len(content) > MAX_FILE_CHARS
    if truncated:
        content = content[:MAX_FILE_CHARS]
    return {
        "path": clean_path,
        "ref": ref_name,
        "truncated": truncated,
        "content": content,
    }


def execute_tool(ctx: ToolContext, name: str, args: dict[str, Any]) -> dict[str, Any]:
    try:
        if name == "list_changed_files":
            return {"files": ctx.changed_files, "count": len(ctx.changed_files)}

        if name == "get_diff_for_file":
            path = safe_path(str(args.get("path", "")))
            if path not in ctx.changed_files:
                return {"error": "file is not changed in this PR", "path": path}
            return {"path": path, "diff": extract_file_diff(ctx.diff, path)}

        if name == "get_file":
            path = safe_path(str(args.get("path", "")))
            ref_name = str(args.get("ref") or "head")
            if ref_name not in {"head", "base"}:
                return {"error": "ref must be 'head' or 'base'", "path": path}
            return get_file_from_github(ctx, path, ref_name)

        if name == "search_diff":
            pattern = str(args.get("pattern", ""))
            max_matches = int(args.get("max_matches") or 20)
            regex = re.compile(pattern)
            matches: list[dict[str, Any]] = []
            lines = ctx.diff.splitlines()
            for idx, line in enumerate(lines):
                if regex.search(line):
                    start = max(0, idx - 2)
                    end = min(len(lines), idx + 3)
                    matches.append({"line": idx + 1, "context": "\n".join(lines[start:end])})
                    if len(matches) >= max_matches:
                        break
            return {"matches": matches, "count": len(matches)}

        if name == "run_readonly_command":
            command = str(args.get("command", ""))
            if command == "gh_pr_view":
                return {"stdout": json.dumps(ctx.pr, ensure_ascii=False)}
            if command == "gh_pr_diff":
                return {"stdout": ctx.diff[:MAX_TOOL_RESPONSE_CHARS], "truncated": len(ctx.diff) > MAX_TOOL_RESPONSE_CHARS}
            if command == "git_ls_files":
                pathspec = str(args.get("pathspec") or ".")
                code, stdout, stderr = run_readonly(["git", "ls-files", "--", pathspec])
                return {"exit_code": code, "stdout": stdout[:MAX_TOOL_RESPONSE_CHARS], "stderr": stderr}
            if command == "git_grep":
                pattern = str(args.get("pattern") or "")
                pathspec = str(args.get("pathspec") or ".")
                if not pattern:
                    return {"error": "pattern is required"}
                code, stdout, stderr = run_readonly(["git", "grep", "-n", "--", pattern, "--", pathspec])
                return {"exit_code": code, "stdout": stdout[:MAX_TOOL_RESPONSE_CHARS], "stderr": stderr}
            return {"error": "command is not allowed", "command": command}
    except Exception as exc:
        return {"error": str(exc), "tool": name}

    return {"error": "unknown tool", "tool": name}


def request_gemini(payload: dict[str, Any]) -> dict[str, Any]:
    api_key = os.environ.get("GEMINI_API_KEY")
    if not api_key:
        raise RuntimeError("GEMINI_API_KEY is not set")

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
    return json.loads(raw)


def gemini_parts(data: dict[str, Any]) -> list[dict[str, Any]]:
    return (
        data.get("candidates", [{}])[0]
        .get("content", {})
        .get("parts", [])
    )


def call_gemini(prompt: str, tool_context: ToolContext | None = None) -> dict[str, Any]:
    contents: list[dict[str, Any]] = [{"role": "user", "parts": [{"text": prompt}]}]
    base_payload: dict[str, Any] = {
        "systemInstruction": {
            "parts": [
                {
                    "text": (
                        "You are a demanding, detail-oriented code reviewer. "
                        "Actively look for small but real problems, nits, inconsistencies, "
                        "and maintainability issues instead of defaulting to approval. "
                        "Return concise, actionable JSON. "
                        "Include correctness, reliability, test, documentation, maintainability, "
                        "style, nit, and other useful PR feedback when it is concrete."
                    )
                }
            ]
        },
        "generationConfig": {
            "temperature": 0.1,
            "maxOutputTokens": MAX_OUTPUT_TOKENS,
            "responseMimeType": "application/json",
            "responseSchema": gemini_schema(),
        },
    }

    if tool_context is None:
        payload = dict(base_payload)
        payload["contents"] = contents
        data = request_gemini(payload)
        text = "".join(part.get("text", "") for part in gemini_parts(data))
        return parse_model_json(text)

    tool_payload = dict(base_payload)
    tool_payload["tools"] = [{"functionDeclarations": tool_declarations()}]
    tool_payload["toolConfig"] = {"functionCallingConfig": {"mode": "AUTO"}}
    tool_call_count = 0
    inspected_detail = False

    for round_index in range(MAX_TOOL_ROUNDS):
        payload = dict(tool_payload)
        payload["contents"] = contents
        data = request_gemini(payload)
        parts = gemini_parts(data)
        calls = [part["functionCall"] for part in parts if "functionCall" in part]
        if not calls:
            if round_index == 0:
                contents.append({"role": "model", "parts": parts})
                contents.append(
                    {
                        "role": "user",
                        "parts": [
                            {
                                "text": (
                                    "You returned a review without using the read-only tools. "
                                    "Call list_changed_files and inspect relevant per-file diffs "
                                    "before returning the final JSON."
                                )
                            }
                        ],
                    }
                )
                continue
            if tool_context.changed_files and not inspected_detail:
                contents.append({"role": "model", "parts": parts})
                contents.append(
                    {
                        "role": "user",
                        "parts": [
                            {
                                "text": (
                                    "You have not inspected an individual file diff yet. "
                                    "Call get_diff_for_file for at least one relevant changed file "
                                    "or run_readonly_command with gh_pr_diff before returning final JSON."
                                )
                            }
                        ],
                    }
                )
                continue
            print(f"Gemini tool calls used: {tool_call_count}")
            text = "".join(part.get("text", "") for part in parts)
            return parse_model_json(text)

        contents.append({"role": "model", "parts": parts})
        response_parts = []
        for call in calls:
            name = call.get("name", "")
            args = call.get("args") or {}
            tool_call_count += 1
            print(f"Gemini tool call: {name}")
            if name == "get_diff_for_file" or (name == "run_readonly_command" and args.get("command") == "gh_pr_diff"):
                inspected_detail = True
            response = trim_tool_response(execute_tool(tool_context, name, args))
            response_parts.append({"functionResponse": {"name": name, "response": response}})
        contents.append({"role": "user", "parts": response_parts})

    contents.append(
        {
            "role": "user",
            "parts": [
                {
                    "text": (
                        "Tool round limit reached. Based on the inspected context, "
                        "return the final review JSON now. Include concrete nits and "
                        "style findings if any are defensible."
                    )
                }
            ],
        }
    )
    final_payload = dict(base_payload)
    final_payload["contents"] = contents
    data = request_gemini(final_payload)
    text = "".join(part.get("text", "") for part in gemini_parts(data))
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


def format_review_body(body: str) -> str:
    return f"Gemini 3.1 Pro:\n\n{body.strip()}"


def post_review(repo: str, pr_number: int, head_sha: str, body: str, comments: list[ReviewComment]) -> None:
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    if not token:
        raise RuntimeError("GH_TOKEN or GITHUB_TOKEN is not set")
    api_url = os.environ.get("GITHUB_API_URL", "https://api.github.com")
    payload: dict[str, Any] = {
        "commit_id": head_sha,
        "body": format_review_body(body),
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
    changed_files = changed_files_from_diff(diff)
    valid_lines = changed_right_lines(diff)
    review_diff, truncated = truncate_diff(diff)
    prompt = build_prompt(pr, review_diff, truncated)
    tool_context = ToolContext(repo=repo, pr_number=pr_number, pr=pr, diff=diff, changed_files=changed_files)
    result = call_gemini(prompt, tool_context)
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
