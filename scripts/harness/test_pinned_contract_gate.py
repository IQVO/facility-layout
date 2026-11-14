#!/usr/bin/env python3
"""Tests for pinned_contract_gate.py's pure comparison: no gh call, no network."""
import importlib.util
import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("pcg", os.path.join(HERE, "pinned_contract_gate.py"))
pcg = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pcg)

PIN = {"local": "testdata/x.yaml", "repo": "IQVO/producer", "path": "apis/openapi.yaml", "ref": "develop"}


class Compare(unittest.TestCase):
    def test_an_identical_copy_is_silent(self):
        self.assertEqual(pcg.compare(PIN, "a: 1\n", "a: 1\n"), [])

    def test_the_2026_11_01_incident_shape_is_reported(self):
        old = "properties:\n  purchaseOrders: {}\n  nextToken: {}\n"
        new = "properties:\n  items: {}\n  nextCursor: {}\n"
        report = "\n".join(pcg.compare(PIN, old, new))
        self.assertIn("STALE PIN testdata/x.yaml", report)
        self.assertIn("-  purchaseOrders: {}", report)
        self.assertIn("+  nextCursor: {}", report)

    def test_a_trailing_newline_difference_is_a_difference(self):
        self.assertTrue(pcg.compare(PIN, "a: 1", "a: 1\n"))

    def test_a_long_diff_is_truncated_not_dumped(self):
        report = pcg.compare(PIN, "", "\n".join(f"k{i}: 1" for i in range(200)))
        self.assertLess(len(report), 50)
        self.assertTrue(any("more diff lines" in line for line in report))


if __name__ == "__main__":
    unittest.main()
