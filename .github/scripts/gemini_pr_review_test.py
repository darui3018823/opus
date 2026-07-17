#!/usr/bin/env python3
import importlib.util
import pathlib
import sys
import unittest


SCRIPT = pathlib.Path(__file__).with_name("gemini_pr_review.py")
SPEC = importlib.util.spec_from_file_location("gemini_pr_review", SCRIPT)
gemini_pr_review = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules["gemini_pr_review"] = gemini_pr_review
SPEC.loader.exec_module(gemini_pr_review)


class GeminiReviewTest(unittest.TestCase):
    def test_should_run_owner_pr(self):
        event = {"pull_request": {"user": {"login": "owner"}}}
        self.assertTrue(gemini_pr_review.should_run("pull_request_target", event, "owner"))

    def test_should_not_run_non_owner_pr(self):
        event = {"pull_request": {"user": {"login": "contrib"}}}
        self.assertFalse(gemini_pr_review.should_run("pull_request_target", event, "owner"))

    def test_should_run_owner_comment_on_pr(self):
        event = {
            "issue": {"number": 7, "pull_request": {}},
            "comment": {"body": "@gemini review", "user": {"login": "owner"}},
        }
        self.assertTrue(gemini_pr_review.should_run("issue_comment", event, "owner"))

    def test_should_not_run_non_owner_comment(self):
        event = {
            "issue": {"number": 7, "pull_request": {}},
            "comment": {"body": "@gemini review", "user": {"login": "contrib"}},
        }
        self.assertFalse(gemini_pr_review.should_run("issue_comment", event, "owner"))

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


if __name__ == "__main__":
    unittest.main()
