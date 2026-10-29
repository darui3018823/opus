#!/usr/bin/env python3
import importlib.util
import pathlib
import sys
import unittest
from unittest.mock import patch


SCRIPT = pathlib.Path(__file__).with_name("gemini_pr_review.py")
SPEC = importlib.util.spec_from_file_location("gemini_pr_review", SCRIPT)
gemini_pr_review = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules["gemini_pr_review"] = gemini_pr_review
SPEC.loader.exec_module(gemini_pr_review)


class GeminiReviewTest(unittest.TestCase):
    def test_should_not_run_automatically_for_owner_pr(self):
        event = {"pull_request": {"user": {"login": "owner"}}}
        self.assertFalse(gemini_pr_review.should_run("pull_request_target", event, "owner"))
        self.assertIsNone(gemini_pr_review.resolve_pr_number("pull_request_target", event))

    def test_should_run_owner_comment_on_pr(self):
        event = {
            "issue": {"number": 7, "pull_request": {}},
            "comment": {"body": "Please @gemini review this", "user": {"login": "owner"}},
        }
        self.assertTrue(gemini_pr_review.should_run("issue_comment", event, "owner"))
        self.assertEqual(gemini_pr_review.resolve_pr_number("issue_comment", event), 7)

    def test_should_run_case_insensitive_owner_mention(self):
        event = {
            "issue": {"number": 7, "pull_request": {}},
            "comment": {"body": "@Gemini", "user": {"login": "owner"}},
        }
        self.assertTrue(gemini_pr_review.should_run("issue_comment", event, "owner"))

    def test_should_not_run_non_owner_comment(self):
        event = {
            "issue": {"number": 7, "pull_request": {}},
            "comment": {"body": "@gemini", "user": {"login": "contrib"}},
        }
        self.assertFalse(gemini_pr_review.should_run("issue_comment", event, "owner"))

    def test_should_not_run_without_exact_mention(self):
        for body in ("please review", "@gemini-reviewer", "email@gemini.example"):
            with self.subTest(body=body):
                event = {
                    "issue": {"number": 7, "pull_request": {}},
                    "comment": {"body": body, "user": {"login": "owner"}},
                }
                self.assertFalse(gemini_pr_review.should_run("issue_comment", event, "owner"))

    def test_should_not_run_issue_comment_outside_pr(self):
        event = {
            "issue": {"number": 7},
            "comment": {"body": "@gemini", "user": {"login": "owner"}},
        }
        self.assertFalse(gemini_pr_review.should_run("issue_comment", event, "owner"))
        self.assertIsNone(gemini_pr_review.resolve_pr_number("issue_comment", event))

    def test_should_run_owner_inline_review_comment(self):
        event = {
            "pull_request": {"number": 8},
            "comment": {"body": "@gemini please review", "user": {"login": "owner"}},
        }
        self.assertTrue(
            gemini_pr_review.should_run("pull_request_review_comment", event, "owner")
        )
        self.assertEqual(
            gemini_pr_review.resolve_pr_number("pull_request_review_comment", event), 8
        )

    def test_should_run_owner_review_body(self):
        event = {
            "pull_request": {"number": 9},
            "review": {"body": "@gemini", "user": {"login": "owner"}},
        }
        self.assertTrue(gemini_pr_review.should_run("pull_request_review", event, "owner"))
        self.assertEqual(gemini_pr_review.resolve_pr_number("pull_request_review", event), 9)

    def test_changed_right_lines(self):
        diff = """diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package main
-old
+new
+added
 keep
"""
        self.assertEqual(gemini_pr_review.changed_right_lines(diff), {"a.go": {2, 3}})
        self.assertEqual(gemini_pr_review.changed_files_from_diff(diff), ["a.go"])

    def test_extract_file_diff(self):
        diff = """diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-old
+new
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1 +1 @@
-old
+new
"""
        file_diff = gemini_pr_review.extract_file_diff(diff, "b.go")
        self.assertIn("diff --git a/b.go b/b.go", file_diff)
        self.assertNotIn("diff --git a/a.go b/a.go", file_diff)

    def test_filter_review_diff_excludes_github_changes(self):
        diff = """diff --git a/.github/workflows/test.yml b/.github/workflows/test.yml
--- a/.github/workflows/test.yml
+++ b/.github/workflows/test.yml
@@ -1 +1 @@
-old
+new
diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-old
+new
"""
        filtered = gemini_pr_review.filter_review_diff(diff)
        self.assertNotIn(".github/workflows/test.yml", filtered)
        self.assertIn("diff --git a/a.go b/a.go", filtered)
        self.assertEqual(gemini_pr_review.changed_files_from_diff(filtered), ["a.go"])
        self.assertEqual(gemini_pr_review.changed_right_lines(filtered), {"a.go": {1}})

    def test_filter_review_diff_skips_github_only_diff(self):
        diff = """diff --git a/.github/scripts/review.py b/.github/scripts/review.py
--- a/.github/scripts/review.py
+++ b/.github/scripts/review.py
@@ -1 +1 @@
-old
+new
"""
        self.assertEqual(gemini_pr_review.filter_review_diff(diff), "")

    def test_invalid_inline_anchor_is_unposted(self):
        comments = [
            gemini_pr_review.ReviewComment("a.go", 3, "ok", "P2"),
            gemini_pr_review.ReviewComment("a.go", 10, "bad anchor", "P2"),
        ]
        valid, unposted = gemini_pr_review.split_valid_comments(comments, {"a.go": {3}})
        self.assertEqual([c.line for c in valid], [3])
        self.assertEqual([c.line for c in unposted], [10])

    def test_parse_model_json_from_fence(self):
        parsed = gemini_pr_review.parse_model_json(
            '```json\n{"summary":"ok","comments":[]}\n```'
        )
        self.assertEqual(parsed["summary"], "ok")

    def test_prompt_allows_style_feedback(self):
        prompt = gemini_pr_review.build_prompt(
            {
                "number": 1,
                "title": "test",
                "url": "https://example.com/pull/1",
                "author": {"login": "owner"},
                "baseRefName": "main",
                "headRefName": "topic",
                "headRefOid": "abc123",
            },
            "diff --git a/a.go b/a.go\n",
            False,
        )
        self.assertIn("style, readability, maintainability", prompt)
        self.assertIn("other concrete suggestions", prompt)
        self.assertIn("zero-comment review should be rare", prompt)
        self.assertIn("list_changed_files", prompt)
        self.assertIn("run_readonly_command", prompt)
        self.assertNotIn("Ignore style", prompt)

    def test_format_review_body_prefixes_model(self):
        self.assertEqual(
            gemini_pr_review.format_review_body("body"),
            "Gemini 3.8 Flash (High):\n\nbody",
        )

    def test_call_gemini_uses_flash_high_without_tools(self):
        result = {"summary": "ok", "comments": []}
        with patch.object(gemini_pr_review, "request_gemini", return_value={
            "candidates": [{"content": {"parts": [{"text": '{"summary":"ok","comments":[]}'}]}}]
        }) as request:
            self.assertEqual(gemini_pr_review.call_gemini("review"), result)
        self.assertIn("/gemini-3.8-flash:generateContent", gemini_pr_review.GEMINI_ENDPOINT)
        config = request.call_args.args[0]["generationConfig"]
        self.assertEqual(config["thinkingConfig"], {"thinkingLevel": "HIGH"})
        self.assertNotIn("temperature", config)

    def test_tool_results_preserve_call_ids_and_thought_signatures(self):
        model_parts = [
            {"functionCall": {"id": "call-1", "name": "list_changed_files", "args": {}},
             "thoughtSignature": "signature"},
            {"functionCall": {"id": "call-2", "name": "get_diff_for_file", "args": {"path": "a.go"}}},
        ]
        replies = [
            {"candidates": [{"content": {"parts": model_parts}}]},
            {"candidates": [{"content": {"parts": [{"text": '{"summary":"ok","comments":[]}'}]}}]},
        ]
        ctx = gemini_pr_review.ToolContext(
            repo="owner/repo", pr_number=1, pr={}, diff="", changed_files=["a.go"]
        )
        with patch.object(gemini_pr_review, "request_gemini", side_effect=replies) as request, \
             patch.object(gemini_pr_review, "execute_tool", return_value={"result": "ok"}):
            self.assertEqual(gemini_pr_review.call_gemini("review", ctx)["summary"], "ok")
        for invocation in request.call_args_list:
            self.assertEqual(invocation.args[0]["generationConfig"]["thinkingConfig"]["thinkingLevel"], "HIGH")
        contents = request.call_args.args[0]["contents"]
        self.assertEqual(contents[1]["parts"], model_parts)
        results = contents[2]["parts"]
        self.assertEqual([part["functionResponse"]["id"] for part in results[:2]], ["call-1", "call-2"])
        self.assertEqual([part["functionResponse"]["name"] for part in results[:2]],
                         ["list_changed_files", "get_diff_for_file"])
        self.assertTrue(results[-1]["text"])

    def test_execute_tool_rejects_unlisted_file_diff(self):
        ctx = gemini_pr_review.ToolContext(
            repo="owner/repo",
            pr_number=1,
            pr={"headRefOid": "head", "baseRefName": "main"},
            diff="",
            changed_files=["a.go"],
        )
        result = gemini_pr_review.execute_tool(ctx, "get_diff_for_file", {"path": "b.go"})
        self.assertIn("error", result)


if __name__ == "__main__":
    unittest.main()
