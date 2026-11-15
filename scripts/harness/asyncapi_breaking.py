#!/usr/bin/env python3
"""AsyncAPI payload-schema breaking gate for apis/asyncapi.yaml.

oasdiff speaks OpenAPI only, so this script covers the event contract: it
compares components.messages between the base (target branch) and head specs
and fails on the changes that break consumers pinning this file as fixtures:

  1. a message present in base is gone in head (its subscription is dead);
  2. a required ``data`` field of a surviving message is gone in head (a
     rename surfaces here as removal of the old name);
  3. a shared ``data`` field narrows (a dropped 'null'/type alternative,
     number -> integer, a shrunk or newly added enum) so payloads the base
     spec allowed now fail the head schema.

Additive change (new message, new optional field, relaxation, description
edit) is NOT breaking and passes.

Usage:
    asyncapi_breaking.py base.yaml head.yaml    exit 0 = ok, 1 = breaking
    asyncapi_breaking.py --self-test            exit 0 = self-check passed
"""
import sys


def resolve(node, doc):
    """Follow local $refs ('#/a/b') to their targets; pass everything else through."""
    seen = 0
    while isinstance(node, dict) and "$ref" in node and node["$ref"].startswith("#/") and seen < 20:
        target = doc
        for part in node["$ref"][2:].split("/"):
            target = target[part]
        node = target
        seen += 1
    return node


def merged_data_schema(msg, doc):
    """The message payload's ``data`` subschema with allOf parts merged."""
    payload = resolve(msg.get("payload") or {}, doc)
    parts = payload.get("allOf") if isinstance(payload, dict) else None
    envelope = {}
    for part in (parts if parts else [payload]):
        part = resolve(part, doc)
        if isinstance(part, dict):
            envelope.update(part.get("properties") or {})
    data = resolve(envelope.get("data") or {}, doc)
    dparts = data.get("allOf") if isinstance(data, dict) and data.get("allOf") else [data]
    merged = {"required": [], "properties": {}}
    for part in dparts:
        part = resolve(part, doc)
        if not isinstance(part, dict):
            continue
        merged["required"] += part.get("required") or []
        merged["properties"].update(part.get("properties") or {})
    return merged


def types_of(prop):
    t = prop.get("type") if isinstance(prop, dict) else None
    if not t:
        return None
    return set(t) if isinstance(t, list) else {t}


def narrowing(base_prop, head_prop, field):
    """Why head's data field accepts fewer payloads than base's, or None."""
    bt, ht = types_of(base_prop), types_of(head_prop)
    if bt and ht:
        # head must accept every base type; integer values are also number.
        dropped = {b for b in bt if b not in ht and not (b == "integer" and "number" in ht)}
        if dropped:
            return f"type narrowed {sorted(bt)} -> {sorted(ht)}"
    be, he = base_prop.get("enum"), head_prop.get("enum")
    if isinstance(head_prop, dict) and he is not None:
        if be is None:
            return "enum added where base was unconstrained"
        lost = {str(v) for v in be} - {str(v) for v in he}
        if lost:
            return f"enum lost values {sorted(lost)}"
    return None


def compare(base, head):
    """List the breaking changes between two AsyncAPI documents (empty = ok)."""
    breaks = []
    bmsgs = (base.get("components") or {}).get("messages") or {}
    hmsgs = (head.get("components") or {}).get("messages") or {}
    for name in sorted(bmsgs):
        if name not in hmsgs:
            breaks.append(f"message removed: {name}")
            continue
        bdata, hdata = merged_data_schema(bmsgs[name], base), merged_data_schema(hmsgs[name], head)
        for field in sorted(set(bdata["required"])):
            if field not in hdata["properties"]:
                breaks.append(f"{name}.data: required field removed/renamed: {field}")
        for field in sorted(set(bdata["properties"]) & set(hdata["properties"])):
            why = narrowing(bdata["properties"][field], hdata["properties"][field], field)
            if why:
                breaks.append(f"{name}.data: {field}: {why}")
    return breaks


def _self_test():
    def doc(messages, schemas):
        return {"components": {"messages": messages, "schemas": schemas}}

    base = doc(
        {
            "kept": {"payload": {"allOf": [
                {"$ref": "#/components/schemas/cloudEvent"},
                {"properties": {"data": {"allOf": [
                    {"$ref": "#/components/schemas/eventDataBase"},
                    {"required": ["locationCode", "count"],
                     "properties": {"locationCode": {"type": "string"},
                                    "count": {"type": "number"},
                                    "kind": {"type": ["string", "null"]},
                                    "state": {"type": "string", "enum": ["A", "B"]}}},
                ]}}},
            ]}},
            "gone": {"payload": {"properties": {"data": {"required": ["x"], "properties": {"x": {"type": "string"}}}}}},
        },
        {"cloudEvent": {"properties": {"data": {"type": "object"}}},
         "eventDataBase": {"required": ["eventName"], "properties": {"eventName": {"type": "string"}}}},
    )
    head = doc(
        {
            "kept": {"payload": {"allOf": [
                {"$ref": "#/components/schemas/cloudEvent"},
                {"properties": {"data": {"allOf": [
                    {"$ref": "#/components/schemas/eventDataBase"},
                    {"required": ["locationCode"],
                     "properties": {"locationCode": {"type": "string"},
                                    "count": {"type": "integer"},          # number -> integer
                                    "kind": {"type": "string"},           # null dropped
                                    "state": {"type": "string", "enum": ["A"]},
                                    "extra": {"type": "string"}}},         # additive: fine
                ]}}},
            ]}},
            "renamed": {"payload": {"properties": {"data": {
                "required": ["locationCode"],
                "properties": {"locationCode": {"type": "string"}}}}}},   # slotCode gone
            "added": {"payload": {"properties": {"data": {"required": ["y"], "properties": {"y": {"type": ["integer", "number"]}}}}}},
        },
        base["components"]["schemas"],
    )
    base["components"]["messages"]["renamed"] = {"payload": {"properties": {"data": {
        "required": ["slotCode"], "properties": {"slotCode": {"type": "string"}}}}}}
    got = sorted(compare(base, head))
    want = [
        "kept.data: count: type narrowed ['number'] -> ['integer']",
        "kept.data: kind: type narrowed ['null', 'string'] -> ['string']",
        "kept.data: state: enum lost values ['B']",
        "message removed: gone",
        "renamed.data: required field removed/renamed: slotCode",
    ]
    ok = got == want
    for line in got:
        print("BREAKING:", line)
    print("self-test:", "ok" if ok else "FAILED\nwant:\n  " + "\n  ".join(want))
    return 0 if ok else 1


def main(argv):
    if argv[1:2] == ["--self-test"]:
        return _self_test()
    if len(argv) != 3:
        print(__doc__)
        return 2
    import yaml  # stdlib-free runner input; runners install it (pinned) alongside this gate
    with open(argv[1]) as f:
        base = yaml.safe_load(f)
    with open(argv[2]) as f:
        head = yaml.safe_load(f)
    breaks = compare(base, head)
    for line in breaks:
        print("BREAKING:", line)
    if breaks:
        print(f"{len(breaks)} breaking change(s) in apis/asyncapi.yaml")
        return 1
    print("no breaking AsyncAPI changes")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
