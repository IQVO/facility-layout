#!/usr/bin/env python3
"""Tests for asyncapi_breaking.py: the union of the cases the nine hand-rolled copies
(facility-layout, order-management, inventory-storage, fulfillment-execution,
workforce-management, wes-work-planning, product-master, dock-scheduling,
yard-management) carried, deduplicated, plus the shapes and rules only the superset has.

No network. PyYAML is needed only by the file-level cases (skipped without it).
Run: python3 scripts/harness/test_asyncapi_breaking.py

Two decisions pin a behaviour where the copies disagreed:
  * required -> optional IS breaking (dock, yard, product-master flagged it; facility-layout
    and order-management tested the opposite). A reader that relied on a field being present
    breaks when it stops being guaranteed.
  * optional -> required IS breaking (wes-work-planning); a NEW required field is additive.
"""
import contextlib
import copy
import importlib.util
import io
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "asyncapi_breaking.py")
spec = importlib.util.spec_from_file_location("aab", SCRIPT)
aab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(aab)

try:
    import yaml
except ImportError:  # the CI job installs it from the hash-pinned requirements file
    yaml = None


def doc26(extra_schemas=None):
    """Shape (a): channel perspective, own topic under subscribe, oneOf of $refs, allOf envelope."""
    schemas = {
        "Envelope": {"required": ["specversion", "data"],
                     "properties": {"specversion": {"const": "1.0"}, "type": {"type": "string"},
                                    "data": {"type": "object"}}},
        "BookedCloudEvent": {"allOf": [{"$ref": "#/components/schemas/Envelope"},
                                       {"properties": {"data": {"$ref": "#/components/schemas/BookedData"}}}]},
        "CancelledCloudEvent": {"allOf": [{"$ref": "#/components/schemas/Envelope"},
                                          {"properties": {"data": {"$ref": "#/components/schemas/CancelledData"}}}]},
        "BookedData": {"type": "object", "required": ["site_id", "version"],
                       "properties": {"site_id": {"type": "string"}, "version": {"type": "integer"},
                                      "weight": {"type": "number"}, "memo": {"type": "string"}}},
        "CancelledData": {"type": "object", "required": ["reason"],
                          "properties": {"reason": {"type": ["string", "null"], "enum": ["A", "B", None]}}},
    }
    schemas.update(extra_schemas or {})
    return {
        "asyncapi": "2.6.0",
        "channels": {"warehouse.dock.events": {"subscribe": {"message": {"oneOf": [
            {"$ref": "#/components/messages/Booked"}, {"$ref": "#/components/messages/Cancelled"}]}}}},
        "components": {"messages": {
            "Booked": {"name": "Booked", "payload": {"$ref": "#/components/schemas/BookedCloudEvent"}},
            "Cancelled": {"name": "Cancelled", "payload": {"$ref": "#/components/schemas/CancelledCloudEvent"}}},
            "schemas": schemas},
    }


def doc30():
    """Shapes (c)/(d): logical channel key + address, channel-level messages map, no operations."""
    d = doc26()
    d["asyncapi"] = "3.0.0"
    d["channels"] = {"events": {"address": "warehouse.dock.events", "messages": {
        "Booked": {"$ref": "#/components/messages/Booked"},
        "Cancelled": {"$ref": "#/components/messages/Cancelled"}}}}
    return d


def edit(base, fn):
    head = copy.deepcopy(base)
    fn(head)
    return head


def schema(doc, name):
    return doc["components"]["schemas"][name]


def found(base, head):
    return "\n".join(aab.breaking(base, head))


class Shapes(unittest.TestCase):
    def test_identical_specs_are_clean(self):
        for d in (doc26(), doc30()):
            self.assertEqual(aab.breaking(d, copy.deepcopy(d)), [])

    def test_the_same_contract_in_26_and_30_shape_is_clean_in_both_directions(self):
        self.assertEqual(aab.breaking(doc26(), doc30()), [])
        self.assertEqual(aab.breaking(doc30(), doc26()), [])

    def test_shape_b_application_perspective_own_topic_under_publish(self):
        d = doc26()
        ch = d["channels"]["warehouse.dock.events"]
        ch["publish"] = ch.pop("subscribe")
        head = edit(d, lambda x: x["channels"]["warehouse.dock.events"]["publish"]["message"]["oneOf"].pop())
        self.assertIn("message Cancelled removed from channel warehouse.dock.events", found(d, head))

    def test_shape_d_channel_messages_map_without_operations_and_inline_payloads(self):
        d = {"asyncapi": "3.0.0", "channels": {"c": {"address": "t.c", "messages": {
            "Inline": {"payload": {"allOf": [
                {"properties": {"specversion": {"type": "string"}}},
                {"properties": {"data": {"type": "object", "required": ["x"],
                                         "properties": {"x": {"type": "string"}}}}}]}}}}}}
        head = edit(d, lambda x: x["channels"]["c"]["messages"]["Inline"]["payload"]["allOf"][1]
                    ["properties"]["data"].update(required=[], properties={}))
        self.assertIn("Inline.data.x: required field removed or renamed", found(d, head))

    def test_30_operations_pointing_at_a_removed_channel_message_do_not_mask_the_removal(self):
        d = doc30()
        d["operations"] = {"send": {"action": "send", "channel": {"$ref": "#/channels/events"},
                                    "messages": [{"$ref": "#/channels/events/messages/Booked"}]}}
        head = edit(d, lambda x: x["channels"]["events"]["messages"].pop("Booked"))
        self.assertIn("message Booked removed from channel warehouse.dock.events", found(d, head))

    def test_workforce_flat_payload_allof_without_an_envelope_ref(self):
        d = {"components": {"messages": {"M": {"payload": {"$ref": "#/components/schemas/E"}}}, "schemas": {
            "E": {"allOf": [{"required": ["data"], "properties": {"data": {"$ref": "#/components/schemas/D"}}}]},
            "D": {"required": ["site_code"], "properties": {"site_code": {"type": "string"}}}}}}
        head = edit(d, lambda x: x["components"]["schemas"]["D"].update(
            required=["siteCode"], properties={"siteCode": {"type": "string"}}))
        self.assertEqual(aab.breaking(d, head), ["M.data.site_code: required field removed or renamed"])

    def test_facility_data_is_itself_an_allof_of_a_shared_base_and_a_message_block(self):
        base_part = {"required": ["eventName"], "properties": {"eventName": {"type": "string"}}}
        d = {"components": {"messages": {"M": {"payload": {"allOf": [
            {"properties": {"data": {"type": "object"}}},
            {"properties": {"data": {"allOf": [
                {"$ref": "#/components/schemas/Base"},
                {"required": ["code"], "properties": {"code": {"type": "string"}}}]}}}]}}},
            "schemas": {"Base": base_part}}}
        head = edit(d, lambda x: x["components"]["schemas"]["Base"].update(required=[]))
        self.assertIn("M.data.eventName: no longer required", found(d, head))
        head = edit(d, lambda x: x["components"]["messages"]["M"]["payload"]["allOf"][1]["properties"]["data"]
                    ["allOf"][1].update(properties={}))
        self.assertIn("M.data.code: required field removed or renamed", found(d, head))


class Removals(unittest.TestCase):
    def test_removed_channel_is_breaking_even_when_keyed_by_a_logical_name(self):
        for d in (doc26(), doc30()):
            head = edit(d, lambda x: x["channels"].clear())
            self.assertIn("channel warehouse.dock.events removed", found(d, head))

    def test_renamed_channel_address_is_breaking_but_a_renamed_logical_key_is_not(self):
        d = doc30()
        moved = edit(d, lambda x: x["channels"]["events"].update(address="warehouse.dock.events.v2"))
        self.assertIn("channel warehouse.dock.events removed", found(d, moved))
        rekeyed = edit(d, lambda x: x["channels"].update(dockEvents=x["channels"].pop("events")))
        self.assertEqual(aab.breaking(d, rekeyed), [])

    def test_removed_message_from_a_live_channel(self):
        d = doc26()
        head = edit(d, lambda x: x["channels"]["warehouse.dock.events"]["subscribe"]["message"]["oneOf"].pop())
        self.assertEqual(aab.breaking(d, head), ["message Cancelled removed from channel warehouse.dock.events"])

    def test_removed_message_from_the_catalog_is_reported_once(self):
        d = doc30()
        head = edit(d, lambda x: (x["components"]["messages"].pop("Cancelled"),
                                  x["channels"]["events"]["messages"].pop("Cancelled")))
        self.assertEqual(aab.breaking(d, head), ["message Cancelled removed"])

    def test_message_moved_off_a_live_channel(self):
        d = doc26()
        d["channels"]["warehouse.dock.analytics"] = {"subscribe": {"message": {"oneOf": [
            {"$ref": "#/components/messages/Cancelled"}]}}}
        head = edit(d, lambda x: x["channels"]["warehouse.dock.events"]["subscribe"]["message"]["oneOf"].pop())
        self.assertIn("message Cancelled moved off channel warehouse.dock.events", found(d, head))

    def test_a_message_keyed_differently_in_the_channel_map_is_still_identified_by_its_ref(self):
        d = doc30()
        d["channels"]["events"]["messages"] = {"publish": {"$ref": "#/components/messages/Booked"},
                                               "second": {"$ref": "#/components/messages/Cancelled"}}
        head = edit(d, lambda x: x["channels"]["events"]["messages"].pop("second"))
        self.assertIn("message Cancelled removed from channel warehouse.dock.events", found(d, head))

    def test_a_dangling_data_ref_in_head_is_breaking_and_in_base_is_only_a_note(self):
        d = doc30()
        head = edit(d, lambda x: x["components"]["schemas"]["BookedCloudEvent"]["allOf"][1]["properties"]["data"]
                    .update({"$ref": "#/components/schemas/NoSuchData"}))
        self.assertIn("Booked: payload no longer resolvable", found(d, head))
        findings, notes = aab.analyze(head, d)
        self.assertEqual(findings, [])
        self.assertTrue(any("base payload does not resolve" in n for n in notes), notes)

    def test_ref_loops_and_recursive_schemas_do_not_hang(self):
        d = doc26({"Tree": {"type": "object", "required": ["id"], "properties": {
            "id": {"type": "string"}, "kids": {"type": "array", "items": {"$ref": "#/components/schemas/Tree"}}}}})
        schema(d, "BookedData")["properties"]["tree"] = {"$ref": "#/components/schemas/Tree"}
        self.assertEqual(aab.breaking(d, copy.deepcopy(d)), [])
        head = edit(d, lambda x: schema(x, "Tree")["properties"].pop("id"))
        self.assertIn("id: required field removed or renamed", found(d, head))
        with self.assertRaises(aab.Unresolved):
            aab.deref({"$ref": "#/a"}, {"a": {"$ref": "#/a"}})


class Fields(unittest.TestCase):
    def setUp(self):
        self.base = doc26()

    def head(self, fn):
        return edit(self.base, fn)

    def booked(self, fn):
        return self.head(lambda d: fn(schema(d, "BookedData")))

    def test_removed_required_field(self):
        head = self.booked(lambda s: (s["required"].remove("site_id"), s["properties"].pop("site_id")))
        self.assertEqual(aab.breaking(self.base, head), ["Booked.data.site_id: required field removed or renamed"])

    def test_renamed_required_field_surfaces_as_a_removal(self):
        head = self.booked(lambda s: (s.update(required=["site", "version"]),
                                      s["properties"].update(site=s["properties"].pop("site_id"))))
        self.assertIn("Booked.data.site_id: required field removed or renamed", found(self.base, head))

    def test_required_field_dropped_from_properties_but_still_listed_is_still_a_removal(self):
        head = self.booked(lambda s: s["properties"].pop("site_id"))
        self.assertIn("site_id: required field removed or renamed", found(self.base, head))

    def test_required_becoming_optional_is_breaking(self):
        head = self.booked(lambda s: s["required"].remove("version"))
        self.assertEqual(aab.breaking(self.base, head),
                         ["Booked.data.version: no longer required (readers relied on it being present)"])

    def test_optional_becoming_required_is_breaking_but_a_new_required_field_is_additive(self):
        head = self.booked(lambda s: s["required"].append("memo"))
        self.assertEqual(aab.breaking(self.base, head), ["Booked.data.memo: optional field became required"])
        head = self.booked(lambda s: (s["properties"].update(tolerance={"type": "integer"}),
                                      s["required"].append("tolerance")))
        self.assertEqual(aab.breaking(self.base, head), [])

    def test_removing_an_optional_field_is_not_breaking(self):
        self.assertEqual(aab.breaking(self.base, self.booked(lambda s: s["properties"].pop("memo"))), [])

    def test_required_listed_but_never_declared_is_silent_until_the_listing_changes(self):
        base = edit(self.base, lambda d: schema(d, "BookedData")["required"].append("ghost"))
        self.assertEqual(aab.breaking(base, copy.deepcopy(base)), [])
        head = edit(base, lambda d: schema(d, "BookedData")["required"].remove("ghost"))
        self.assertIn("ghost: required field removed or renamed", found(base, head))

    def test_type_swap_is_narrowing(self):
        head = self.booked(lambda s: s["properties"]["site_id"].update(type="integer"))
        self.assertEqual(aab.breaking(self.base, head),
                         ["Booked.data.site_id: type narrowed ['string'] -> ['integer']"])

    def test_number_to_integer_is_narrowing_and_integer_to_number_is_not(self):
        head = self.booked(lambda s: s["properties"]["weight"].update(type="integer"))
        self.assertEqual(aab.breaking(self.base, head),
                         ["Booked.data.weight: type narrowed ['number'] -> ['integer']"])
        self.assertEqual(aab.breaking(self.base, self.booked(lambda s: s["properties"]["version"].update(type="number"))), [])

    def test_dropping_null_from_a_union_is_narrowing(self):
        head = self.head(lambda d: schema(d, "CancelledData")["properties"]["reason"].update(type="string"))
        self.assertIn("Cancelled.data.reason: type narrowed ['null', 'string'] -> ['string']", found(self.base, head))

    def test_dropping_nullable_true_is_narrowing(self):
        base = edit(self.base, lambda d: schema(d, "CancelledData")["properties"]["reason"].update(type="string", nullable=True))
        head = edit(base, lambda d: schema(d, "CancelledData")["properties"]["reason"].update(nullable=False))
        self.assertIn("Cancelled.data.reason: type narrowed ['null', 'string'] -> ['string']", found(base, head))

    def test_nullable_true_is_the_same_as_a_null_union(self):
        base = edit(self.base, lambda d: schema(d, "BookedData")["properties"]["memo"].update(type=["string", "null"]))
        head = edit(self.base, lambda d: schema(d, "BookedData")["properties"]["memo"].update(nullable=True))
        self.assertEqual(aab.breaking(base, head), [])

    def test_enum_shrinking_is_narrowing_and_growing_is_not(self):
        head = self.head(lambda d: schema(d, "CancelledData")["properties"]["reason"].update(enum=["A"]))
        self.assertIn("Cancelled.data.reason: enum/const lost 2 value(s)", found(self.base, head))
        head = self.head(lambda d: schema(d, "CancelledData")["properties"]["reason"].update(enum=["A", "B", None, "C"]))
        self.assertEqual(aab.breaking(self.base, head), [])

    def test_a_new_enum_or_const_on_an_open_field_is_narrowing(self):
        head = self.booked(lambda s: s["properties"]["memo"].update(enum=["x"]))
        self.assertIn("memo: enum/const constraint introduced", found(self.base, head))
        head = self.booked(lambda s: s["properties"]["memo"].update(const="x"))
        self.assertIn("memo: enum/const constraint introduced", found(self.base, head))

    def test_const_value_change_and_const_to_enum_superset(self):
        head = self.head(lambda d: schema(d, "Envelope")["properties"]["specversion"].update(const="2.0"))
        self.assertIn("Booked.specversion: enum/const lost 1 value(s)", found(self.base, head))
        head = self.head(lambda d: schema(d, "Envelope")["properties"]["specversion"].update(const=None, enum=["1.0", "2.0"]))
        head["components"]["schemas"]["Envelope"]["properties"]["specversion"].pop("const")
        self.assertEqual(aab.breaking(self.base, head), [])

    def test_format_and_pattern_introduced_or_changed_is_narrowing(self):
        head = self.booked(lambda s: s["properties"]["memo"].update(format="uuid", pattern="^m-"))
        out = found(self.base, head)
        self.assertIn("memo: format introduced", out)
        self.assertIn("memo: pattern introduced", out)
        base = edit(self.base, lambda d: schema(d, "BookedData")["properties"]["memo"].update(pattern="^a"))
        self.assertIn("memo: pattern changed", found(base, edit(base, lambda d: schema(d, "BookedData")["properties"]["memo"].update(pattern="^b"))))

    def test_bounds_tightened_are_narrowing_and_loosened_are_not(self):
        for key, val in (("minimum", 1), ("maximum", 5), ("minLength", 2), ("maxLength", 3),
                         ("minItems", 1), ("maxItems", 2), ("exclusiveMinimum", 0), ("multipleOf", 5)):
            head = self.booked(lambda s, k=key, v=val: s["properties"]["weight"].update({k: v}))
            self.assertIn(f"weight: {key} tightened", found(self.base, head), key)
        base = self.booked(lambda s: s["properties"]["weight"].update(minimum=10, maximum=20, maxLength=9))
        loosened = edit(base, lambda d: schema(d, "BookedData")["properties"]["weight"].update(minimum=0, maximum=30, maxLength=12))
        self.assertEqual(aab.breaking(base, loosened), [])
        dropped = edit(base, lambda d: [schema(d, "BookedData")["properties"]["weight"].pop(k) for k in ("minimum", "maximum")])
        self.assertEqual(aab.breaking(base, dropped), [])
        self.assertEqual(aab.breaking(base, self.booked(lambda s: s["properties"]["weight"].update(minLength=0))), [])

    def test_unique_items_and_closed_additional_properties_are_narrowing(self):
        head = self.booked(lambda s: s["properties"].update(tags={"type": "array", "uniqueItems": True}))
        base = self.booked(lambda s: s["properties"].update(tags={"type": "array"}))
        self.assertIn("tags: uniqueItems tightened", found(base, head))
        head = self.booked(lambda s: s.update(additionalProperties=False))
        self.assertIn("Booked.data: additionalProperties closed", found(self.base, head))

    def test_nested_object_and_array_item_changes_are_judged(self):
        nested = {"type": "object", "required": ["sku"], "properties": {"sku": {"type": "string"}, "qty": {"type": "integer"}}}
        base = self.booked(lambda s: s["properties"].update(lines={"type": "array", "items": nested}))
        head = edit(base, lambda d: schema(d, "BookedData")["properties"]["lines"]["items"]["required"].clear())
        self.assertIn("Booked.data.lines[].sku: no longer required", found(base, head))
        head = edit(base, lambda d: schema(d, "BookedData")["properties"]["lines"]["items"]["properties"]["qty"].update(type="string"))
        self.assertIn("Booked.data.lines[].qty: type narrowed", found(base, head))

    def test_envelope_fields_are_judged_too(self):
        head = self.head(lambda d: schema(d, "Envelope")["required"].remove("specversion"))
        self.assertIn("Booked.specversion: no longer required", found(self.base, head))

    def test_additive_changes_are_clean(self):
        def additive(d):
            d["channels"]["warehouse.dock.events"]["subscribe"]["message"]["oneOf"].append(
                {"$ref": "#/components/messages/Suspended"})
            d["components"]["messages"]["Suspended"] = {"name": "Suspended", "payload": {"$ref": "#/components/schemas/Envelope"}}
            d["channels"]["warehouse.dock.analytics"] = {"subscribe": {"message": {"oneOf": []}}}
            booked = schema(d, "BookedData")
            booked["properties"]["extra"] = {"type": "string"}
            booked["properties"]["weight"]["type"] = ["number", "null"]
            booked["description"] = "edited"
            schema(d, "CancelledData")["properties"]["reason"]["enum"] = ["A", "B", None, "C"]
        self.assertEqual(aab.breaking(self.base, edit(self.base, additive)), [])


class Consumed(unittest.TestCase):
    """Documentation of another context's contract (tagged `consumed`, or a 3.0 `receive`) warns, never blocks."""

    def drop_required(self, d):
        schema(d, "CancelledData")["required"].remove("reason")

    def test_a_message_tagged_consumed_only_warns(self):
        base = edit(doc26(), lambda d: d["components"]["messages"]["Cancelled"].update(tags=[{"name": "consumed"}]))
        head = edit(base, self.drop_required)
        findings, notes = aab.analyze(base, head)
        self.assertEqual(findings, [])
        self.assertEqual(len(notes), 1)
        self.assertIn("consumed contract (not blocking): Cancelled.data.reason: no longer required", notes[0])
        self.assertIn("Booked.data.site_id: required field removed or renamed",
                      found(base, edit(base, lambda d: schema(d, "BookedData")["properties"].pop("site_id"))))

    def test_a_channel_whose_operations_are_all_tagged_consumed_only_warns(self):
        def tag(d):
            d["channels"]["warehouse.dock.events"]["subscribe"]["tags"] = [{"name": "consumed"}]
        base = edit(doc26(), tag)
        self.assertEqual(aab.analyze(base, edit(base, lambda d: d["channels"].clear()))[0], [])
        self.assertTrue(aab.analyze(base, edit(base, lambda d: d["channels"].clear()))[1])

    def test_a_30_receive_operation_marks_its_channel_consumed_and_a_send_does_not(self):
        base = doc30()
        base["operations"] = {"in": {"action": "receive", "channel": {"$ref": "#/channels/events"}}}
        self.assertEqual(aab.breaking(base, edit(base, self.drop_required)), [])
        base["operations"]["in"]["action"] = "send"
        self.assertIn("Cancelled.data.reason: no longer required", found(base, edit(base, self.drop_required)))

    def test_a_message_shared_with_an_owned_channel_stays_owned(self):
        base = doc26()
        base["channels"]["warehouse.in.events"] = {"publish": {"tags": [{"name": "consumed"}], "message": {"oneOf": [
            {"$ref": "#/components/messages/Cancelled"}]}}}
        self.assertIn("Cancelled.data.reason: no longer required", found(base, edit(base, self.drop_required)))

    def test_ownership_is_read_from_the_base_so_retagging_cannot_hide_a_break(self):
        base = doc26()

        def retag_and_break(d):
            d["components"]["messages"]["Cancelled"]["tags"] = [{"name": "consumed"}]
            self.drop_required(d)
        self.assertIn("Cancelled.data.reason: no longer required", found(base, edit(base, retag_and_break)))

    @unittest.skipIf(yaml is None, "PyYAML not installed")
    def test_the_cli_prints_a_warning_and_exits_0(self):
        base = edit(doc26(), lambda d: d["components"]["messages"]["Cancelled"].update(tags=[{"name": "consumed"}]))
        with tempfile.TemporaryDirectory() as tmp:
            paths = []
            for name, d in (("b.yaml", base), ("h.yaml", edit(base, self.drop_required))):
                paths.append(os.path.join(tmp, name))
                with open(paths[-1], "w", encoding="utf-8") as fh:
                    yaml.safe_dump(d, fh)
            proc = subprocess.run([sys.executable, SCRIPT, *paths], capture_output=True, text=True)
        self.assertEqual(proc.returncode, 0, proc.stdout)
        self.assertIn("::warning::asyncapi: consumed contract (not blocking)", proc.stdout)


class Main(unittest.TestCase):
    def run_main(self, argv, env=None):
        out = io.StringIO()
        old = dict(os.environ)
        os.environ.pop("CONSUMERS", None)
        os.environ.update(env or {})
        try:
            with contextlib.redirect_stdout(out):
                code = aab.main(argv)
        finally:
            os.environ.clear()
            os.environ.update(old)
        return code, out.getvalue()

    def write(self, tmp, name, doc):
        path = os.path.join(tmp, name)
        with open(path, "w", encoding="utf-8") as fh:
            yaml.safe_dump(doc, fh)
        return path

    @unittest.skipIf(yaml is None, "PyYAML not installed")
    def test_files_exit_codes_and_consumer_names(self):
        base = doc26()
        head = edit(base, lambda d: schema(d, "BookedData")["required"].remove("version"))
        with tempfile.TemporaryDirectory() as tmp:
            b, h = self.write(tmp, "b.yaml", base), self.write(tmp, "h.yaml", head)
            code, out = self.run_main([b, b])
            self.assertEqual(code, 0, out)
            code, out = self.run_main([b, h, "--consumers", "alpha,beta"])
            self.assertEqual((code, "alpha, beta" in out, "::error::" in out), (1, True, True), out)
            code, out = self.run_main([b, h], env={"CONSUMERS": "gamma delta"})
            self.assertEqual((code, "gamma, delta" in out), (1, True), out)
            code, out = self.run_main([b, h])
            self.assertIn("contracts/pinned.json@develop in the consumer repos", out)

    @unittest.skipIf(yaml is None, "PyYAML not installed")
    def test_unreadable_or_unparseable_specs_are_exit_2_not_a_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            good = self.write(tmp, "g.yaml", doc26())
            bad = os.path.join(tmp, "bad.yaml")
            with open(bad, "w", encoding="utf-8") as fh:
                fh.write("a: [unclosed\n")
            scalar = os.path.join(tmp, "scalar.yaml")
            with open(scalar, "w", encoding="utf-8") as fh:
                fh.write("just a string\n")
            for args in ([os.path.join(tmp, "nope.yaml"), good], [good, bad], [scalar, good]):
                self.assertEqual(self.run_main(args)[0], 2, args)
            self.assertEqual(self.run_main(["--check", bad])[0], 2)
            self.assertEqual(self.run_main(["--check", good])[0], 0)

    def test_the_self_check_passes_in_process_and_as_a_script(self):
        self.assertEqual(aab.self_check(), 0)
        proc = subprocess.run([sys.executable, SCRIPT, "--self-check"], capture_output=True, text=True)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("self-check OK", proc.stdout)

    def test_wrong_arguments_are_a_usage_error(self):
        proc = subprocess.run([sys.executable, SCRIPT, "only-one.yaml"], capture_output=True, text=True)
        self.assertEqual(proc.returncode, 2)
        self.assertIn("BASE.yaml HEAD.yaml", proc.stderr)


@unittest.skipIf(yaml is None, "PyYAML not installed")
class RealSpec(unittest.TestCase):
    """Runs in a producer repo that carries the kit (apis/asyncapi.yaml present); skipped in the template."""

    PATH = os.path.join(HERE, "..", "..", "apis", "asyncapi.yaml")

    def setUp(self):
        if not os.path.exists(self.PATH):
            self.skipTest("no apis/asyncapi.yaml in this repo")
        with open(self.PATH, encoding="utf-8") as fh:
            self.doc = yaml.safe_load(fh)

    def test_every_catalogued_message_resolves_a_payload(self):
        _, messages = aab.catalog(self.doc)
        for mid, node in messages.items():
            props = aab.payload_schema(node, self.doc).get("properties")
            self.assertIsInstance(props, dict, f"{mid}: payload did not resolve through allOf/$ref")

    def test_the_real_spec_against_itself_is_silent(self):
        self.assertEqual(aab.breaking(self.doc, copy.deepcopy(self.doc)), [])


if __name__ == "__main__":
    unittest.main()
