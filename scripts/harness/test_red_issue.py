#!/usr/bin/env python3
"""Tests for red_issue.py's pure drafting logic (harness-template v4): no gh/git calls, no network."""
import importlib.util
import os
import re
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
spec = importlib.util.spec_from_file_location("ri", os.path.join(HERE, "red_issue.py"))
ri = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ri)


class ComposeIncidentRow(unittest.TestCase):
    def test_row_is_a_valid_markdown_table_row_with_six_columns(self):
        row = ri.compose_incident_row("mutation", "https://x/runs/1", date="2026-10-18")
        cells = [c.strip() for c in row.strip().strip("|").split("|")]
        self.assertEqual(len(cells), 6)

    def test_row_marks_itself_draft_and_keeps_the_run_url_traceable(self):
        row = ri.compose_incident_row("drift", "https://x/runs/42", date="2026-10-18")
        self.assertIn("DRAFT", row)
        self.assertIn("https://x/runs/42", row)

    def test_root_cause_and_fix_are_left_for_a_human_not_fabricated(self):
        row = ri.compose_incident_row("mutation", "https://x/runs/1", date="2026-10-18")
        cells = [c.strip() for c in row.strip().strip("|").split("|")]
        # columns: Date, Symptom, Root cause, Fix, Guard, Status
        self.assertTrue(cells[2].startswith("TBD"))
        self.assertEqual(cells[3], "TBD")
        self.assertEqual(cells[4], "TBD")


class ComposeFitnessStub(unittest.TestCase):
    def test_stub_is_valid_looking_go_and_skips_itself(self):
        src = ri.compose_fitness_stub("mutation-fast", "https://x/runs/7", date="2026-10-18")
        self.assertIn("package architecture", src)
        self.assertRegex(src, r"func Test\w+DraftPendingHumanReview\(t \*testing\.T\)")
        self.assertIn("t.Skip(", src)

    def test_job_name_with_hyphens_becomes_a_valid_go_identifier(self):
        src = ri.compose_fitness_stub("drift-weekly", "https://x/runs/9")
        m = re.search(r"func (Test\w+)\(", src)
        self.assertIsNotNone(m)
        self.assertNotIn("-", m.group(1))

    def test_stub_never_asserts_anything_real_by_itself(self):
        # the whole point of a draft is that it must not silently count as coverage: the only
        # non-comment statement in the function body is the Skip call, nothing that could pass/fail.
        src = ri.compose_fitness_stub("mutation", "https://x/runs/1")
        body = re.search(r"func Test\w+\(t \*testing\.T\) \{\n(.*)\n\}\n\Z", src, re.S).group(1)
        statements = [l.strip() for l in body.splitlines() if l.strip()]
        self.assertEqual(len(statements), 1)
        self.assertTrue(statements[0].startswith("t.Skip("))


if __name__ == "__main__":
    unittest.main()
