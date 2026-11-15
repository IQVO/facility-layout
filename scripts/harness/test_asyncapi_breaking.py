#!/usr/bin/env python3
"""Tests for asyncapi_breaking.py's pure comparison logic: no network, no yaml import."""
import importlib.util
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
spec = importlib.util.spec_from_file_location("ab", os.path.join(HERE, "asyncapi_breaking.py"))
ab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ab)

# CloudEvents envelope shape of apis/asyncapi.yaml: payload.allOf [$ref cloudEvent,
# {properties.data: allOf [$ref eventDataBase, {required, properties}]}]
SCHEMAS = {
    "cloudEvent": {"required": ["specversion", "id"], "properties": {"specversion": {"const": "1.0"}}},
    "eventDataBase": {"required": ["eventName", "occurredAt"],
                      "properties": {"eventName": {"type": "string"}, "occurredAt": {"type": "string"}}},
}


def message(data_required, data_props):
    return {"payload": {"allOf": [
        {"$ref": "#/components/schemas/cloudEvent"},
        {"properties": {"data": {"allOf": [
            {"$ref": "#/components/schemas/eventDataBase"},
            {"required": data_required, "properties": data_props},
        ]}}},
    ]}}


def doc(messages):
    return {"components": {"messages": messages, "schemas": SCHEMAS}}


class Compare(unittest.TestCase):
    def test_identical_specs_have_no_breaking_changes(self):
        base = doc({"m": message(["locationCode"], {"locationCode": {"type": "string"}})})
        self.assertEqual(ab.compare(base, doc(dict(base["components"]["messages"]))), [])

    def test_removed_message_is_breaking(self):
        base = doc({"m": message(["locationCode"], {"locationCode": {"type": "string"}})})
        self.assertEqual(ab.compare(base, doc({})), ["message removed: m"])

    def test_removed_required_data_field_is_breaking_and_renames_surface_as_removal(self):
        base = doc({"m": message(["oldName"], {"oldName": {"type": "string"}})})
        head = doc({"m": message(["newName"], {"newName": {"type": "string"}})})
        self.assertEqual(ab.compare(base, head), ["m.data: required field removed/renamed: oldName"])

    def test_required_field_kept_as_optional_property_is_a_relaxation_not_breaking(self):
        base = doc({"m": message(["f"], {"f": {"type": "string"}})})
        head = doc({"m": message([], {"f": {"type": "string"}})})
        self.assertEqual(ab.compare(base, head), [])

    def test_number_to_integer_is_narrowing(self):
        base = doc({"m": message([], {"f": {"type": "number"}})})
        head = doc({"m": message([], {"f": {"type": "integer"}})})
        self.assertEqual(ab.compare(base, head), ["m.data: f: type narrowed ['number'] -> ['integer']"])

    def test_integer_to_number_is_widening_not_breaking(self):
        base = doc({"m": message([], {"f": {"type": "integer"}})})
        head = doc({"m": message([], {"f": {"type": "number"}})})
        self.assertEqual(ab.compare(base, head), [])

    def test_dropping_null_from_a_nullable_field_is_narrowing(self):
        base = doc({"m": message([], {"f": {"type": ["string", "null"]}})})
        head = doc({"m": message([], {"f": {"type": "string"}})})
        self.assertEqual(ab.compare(base, head), ["m.data: f: type narrowed ['null', 'string'] -> ['string']"])

    def test_enum_shrinking_and_newly_constraining_are_narrowing(self):
        base = doc({"m": message([], {"f": {"type": "string", "enum": ["A", "B"]},
                                      "g": {"type": "string"}})})
        head = doc({"m": message([], {"f": {"type": "string", "enum": ["A"]},
                                      "g": {"type": "string", "enum": ["X"]}})})
        self.assertEqual(ab.compare(base, head), [
            "m.data: f: enum lost values ['B']",
            "m.data: g: enum added where base was unconstrained",
        ])

    def test_additive_change_is_not_breaking(self):
        base = doc({"m": message(["f"], {"f": {"type": "string"}})})
        head = doc({"m": message(["f"], {"f": {"type": "string"}, "opt": {"type": "number"}}),
                    "newMsg": message(["z"], {"z": {"type": "string"}})})
        self.assertEqual(ab.compare(base, head), [])

    def test_the_modules_own_self_test_passes(self):
        self.assertEqual(ab._self_test(), 0)


if __name__ == "__main__":
    unittest.main()
