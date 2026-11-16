#!/usr/bin/env python3
"""AsyncAPI producer breaking-change gate (the canonical fleet copy).

oasdiff speaks OpenAPI only. This script is its AsyncAPI counterpart for the
CloudEvents contracts a producer publishes: it compares the PR base's
apis/asyncapi.yaml with the PR head's and fails on every change that breaks a
consumer pinning the file (contracts/pinned.json, byte-copy at the producer's
develop). Docs: docs/spec-breaking-kit.md.

Breaking (exit 1, one finding each):
  1. a channel is removed (matched by Kafka address, else by channel key);
  2. a message is removed from the catalog, or removed from / moved off a
     channel that still exists;
  3. a field of the message payload (envelope AND `data`, nested objects and
     array items included) loses ground:
       - a required field is removed or renamed;
       - a required field is no longer required (readers relied on it);
       - an optional field became required;
       - the type set narrows (number -> integer, `null` dropped, a type
         removed, `nullable` dropped);
       - an enum / const appears where any value was valid, or loses values;
       - a bound tightens (minimum, maximum, exclusive*, minLength, maxLength,
         minItems, maxItems, minProperties, maxProperties, multipleOf,
         uniqueItems), or a pattern / format appears or changes;
       - additionalProperties flips to false.
  4. a payload `$ref` that resolved in the base no longer resolves in the head.

Messages and channels the BASE only consumes (tagged `consumed`, or a 3.0 `action: receive`) document another
context's contract: a change there is reported as a warning, never a failure. Untagged means owned.

Additive change passes: a new message, channel or optional field, a widened
type, a new enum value, a dropped bound, a description edit. A breaking
payload change ships as a NEW `.v2` type + dataschema, never as a mutation.

Shapes read (all four fleet shapes, one normaliser):
  (a) AsyncAPI 2.6, channel perspective: own topics under `subscribe:`, consumed
      topics under `publish:`, `message` or `message.oneOf`, channels keyed by topic;
  (b) AsyncAPI 2.6, application perspective (process-path-management: own
      topic under `publish:`);
  (c) AsyncAPI 3.0, channels keyed by logical name + `address`, channel-level
      `messages` maps, with `operations` (action send/receive) or without (a channel
      is what carries its messages; operations only point at them, so they are not read);
  (d) channel-level `messages` map and no operations.
  CloudEvents envelopes appear as payload.allOf[$ref CloudEvent,
  {properties.data: $ref}] (or data itself an allOf); allOf branches are merged
  (required united, properties merged) before comparing. Local $refs are chased.

Usage:
  asyncapi_breaking.py BASE.yaml HEAD.yaml [--consumers a,b,c]
  asyncapi_breaking.py --check SPEC.yaml [SPEC.yaml ...]   parse + resolve + self-compare
  asyncapi_breaking.py --self-check                        embedded fixtures (--self-test is an alias)

Pinning consumers are named in the failure text: `--consumers` or the CONSUMERS
env var (comma / space / newline separated), supplied by the CI job.
Exit: 0 compatible, 1 breaking, 2 cannot judge (a spec that cannot be read or
parsed is NOT a pass: a gate that cannot look must say so).

PyYAML is imported lazily: the comparison functions work on dicts and need none.
"""
import argparse
import copy
import json
import os
import sys

MAX_DEPTH = 24
LOWER = ("minimum", "exclusiveMinimum", "minLength", "minItems", "minProperties")
UPPER = ("maximum", "exclusiveMaximum", "maxLength", "maxItems", "maxProperties")
ZERO_FLOOR = ("minLength", "minItems", "minProperties")  # absent == 0


class Unresolved(Exception):
    """A local $ref that points nowhere; `root` is the document it failed in."""

    def __init__(self, ref, root):
        super().__init__(ref)
        self.ref, self.root = ref, root


# ----------------------------------------------------------------- $ref / allOf


def deref(node, root):
    """Follow local '#/a/b' $ref chains to a concrete node (loops are errors)."""
    hops = 0
    while isinstance(node, dict) and isinstance(node.get("$ref"), str):
        ref = node["$ref"]
        if not ref.startswith("#/"):
            return node  # external ref: opaque, never judged
        target = root
        for part in ref[2:].split("/"):
            part = part.replace("~1", "/").replace("~0", "~")
            if isinstance(target, dict) and part in target:
                target = target[part]
            elif isinstance(target, list) and part.isdigit() and int(part) < len(target):
                target = target[int(part)]
            else:
                raise Unresolved(ref, root)
        node, hops = target, hops + 1
        if hops > 50:
            raise Unresolved(ref + " (loop)", root)
    return node


def flatten(node, root, depth=0):
    """A schema with its $ref resolved and its allOf branches merged.

    required is united; a property defined by several branches becomes
    allOf[a, b] (flattened again when compared); other keywords: last wins."""
    node = deref(node, root)
    if not isinstance(node, dict) or depth > MAX_DEPTH:
        return {}
    if not isinstance(node.get("allOf"), list):
        return node
    out = {k: v for k, v in node.items() if k != "allOf"}
    for branch in node["allOf"]:
        part = flatten(branch, root, depth + 1)
        for key, val in part.items():
            if key == "required":
                out["required"] = list(dict.fromkeys(list(out.get("required") or []) + list(val or [])))
            elif key == "properties":
                props = dict(out.get("properties") or {})
                for name, sub in (val or {}).items():
                    props[name] = {"allOf": [props[name], sub]} if name in props else sub
                out["properties"] = props
            else:
                out[key] = val
    return out


# ------------------------------------------------------------------ the catalog


def _tail(ref):
    return ref.rsplit("/", 1)[-1]


def _message_entries(holder):
    """Message nodes of one holder: a message, {oneOf: [...]}, a list, or a map."""
    if holder is None:
        return []
    if isinstance(holder, list):
        return [(None, m) for m in holder]
    if isinstance(holder, dict):
        if isinstance(holder.get("oneOf"), list):
            return [(None, m) for m in holder["oneOf"]]
        return [(None, holder)]
    return []


def _identify(key, node, index):
    """(message id, resolved node source) - the ref tail, else the map key, else name."""
    if isinstance(node, dict) and isinstance(node.get("$ref"), str):
        return _tail(node["$ref"])
    if key is not None:
        return str(key)
    if isinstance(node, dict):
        for k in ("messageId", "name", "title"):
            if isinstance(node.get(k), str):
                return node[k]
    return f"#{index}"


def catalog(doc):
    """-> (channels, messages).

    channels: {channel id (address, else key): {message id: message node}}
    messages: {message id: message node}: every channel message plus components.messages."""
    channels, messages = {}, {}
    comp = (doc.get("components") or {}).get("messages") or {}
    for key, msg in comp.items():
        messages[str(key)] = msg
    for ckey, ch in (doc.get("channels") or {}).items():
        ch = ch if isinstance(ch, dict) else {}
        cid = str(ch.get("address") or ckey)
        members = channels.setdefault(cid, {})
        entries = []
        for op in ("publish", "subscribe"):  # 2.x, either perspective
            holder = ch.get(op)
            if isinstance(holder, dict):
                entries += _message_entries(holder.get("message"))
                entries += _message_entries(holder.get("messages"))
        chmsgs = ch.get("messages")  # 3.0 / shape (d): a map (2.x: a list)
        if isinstance(chmsgs, dict):
            entries += list(chmsgs.items())
        elif isinstance(chmsgs, list):
            entries += [(None, m) for m in chmsgs]
        for i, (key, node) in enumerate(entries):
            mid = _identify(key, node, i)
            members[mid] = node
            messages.setdefault(mid, node)
    return channels, messages


def payload_schema(message, root):
    """The message payload, merged: envelope + `data` (allOf-aware)."""
    msg = deref(message, root)
    return flatten((msg or {}).get("payload") or {}, root) if isinstance(msg, dict) else {}


# --------------------------------------------------------------- schema compare


def _canon(v):
    return json.dumps(v, sort_keys=True, default=str)


def _num(v):
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def types_of(schema):
    t = schema.get("type")
    ts = set(map(str, t)) if isinstance(t, list) else ({str(t)} if isinstance(t, str) else None)
    if ts is not None and schema.get("nullable") is True:
        ts.add("null")
    return ts


def values_of(schema):
    """The closed set of allowed values (enum / const), or None when open."""
    if isinstance(schema.get("enum"), list):
        return {_canon(v) for v in schema["enum"]}
    if "const" in schema:
        return {_canon(schema["const"])}
    return None


def narrowing(label, base, head):
    """Findings for one field whose head schema accepts less than the base's."""
    out = []
    bt, ht = types_of(base), types_of(head)
    if bt is not None and ht is not None:
        covered = ht | ({"integer"} if "number" in ht else set())
        if not bt <= covered:
            out.append(f"{label}: type narrowed {sorted(bt)} -> {sorted(ht)}")
    bv, hv = values_of(base), values_of(head)
    if hv is not None and bv is None:
        out.append(f"{label}: enum/const constraint introduced where any value was valid")
    elif hv is not None and bv is not None and not bv <= hv:
        out.append(f"{label}: enum/const lost {len(bv - hv)} value(s)")
    for key in LOWER + UPPER:
        b, h = base.get(key), head.get(key)
        if isinstance(h, bool) or isinstance(b, bool):
            if h is True and b is not True:
                out.append(f"{label}: {key} tightened")
            continue
        if not _num(h):
            continue
        if not _num(b):
            b = 0 if key in ZERO_FLOOR else None
        if b is None or (key in LOWER and h > b) or (key in UPPER and h < b):
            out.append(f"{label}: {key} tightened ({'unset' if b is None else b} -> {h})")
    for key in ("pattern", "format"):
        if head.get(key) is not None and head.get(key) != base.get(key):
            what = "introduced" if base.get(key) is None else f"changed ({base[key]!r} -> {head[key]!r})"
            out.append(f"{label}: {key} {what}")
    if head.get("uniqueItems") is True and base.get("uniqueItems") is not True:
        out.append(f"{label}: uniqueItems tightened")
    if head.get("additionalProperties") is False and base.get("additionalProperties") is not False:
        out.append(f"{label}: additionalProperties closed (unknown fields now rejected)")
    bm, hm = base.get("multipleOf"), head.get("multipleOf")
    if _num(hm) and hm != 0 and (not _num(bm) or (bm % hm) != 0):
        out.append(f"{label}: multipleOf tightened")
    return out


def compare_schema(label, base, head, broot, hroot, depth=0):
    """Findings between two schema nodes (each resolved + allOf-merged first)."""
    base, head = flatten(base, broot), flatten(head, hroot)
    if depth > MAX_DEPTH or not base:
        return []
    out = narrowing(label, base, head)
    bprops, hprops = base.get("properties") or {}, head.get("properties") or {}
    breq, hreq = set(base.get("required") or []), set(head.get("required") or [])
    for f in sorted(breq):
        # a name listed in `required` but never declared under `properties` (a spec quirk
        # the fleet has) is judged only when the listing itself changes.
        if f not in hprops and (f in bprops or f not in hreq):
            out.append(f"{label}.{f}: required field removed or renamed")
        elif f not in hreq:
            out.append(f"{label}.{f}: no longer required (readers relied on it being present)")
    for f in sorted(set(hprops) & set(bprops)):
        if f in hreq and f not in breq:
            out.append(f"{label}.{f}: optional field became required")
        out += compare_schema(f"{label}.{f}", bprops[f], hprops[f], broot, hroot, depth + 1)
    if isinstance(base.get("items"), dict) and isinstance(head.get("items"), dict):
        out += compare_schema(f"{label}[]", base["items"], head["items"], broot, hroot, depth + 1)
    return out


def breaking(base, head):
    """Human-readable findings for every breaking change base -> head."""
    return analyze(base, head)[0]


def _tagged_consumed(node):
    tags = node.get("tags") if isinstance(node, dict) else None
    return any((t.get("name") if isinstance(t, dict) else t) == "consumed" for t in (tags or []))


def consumed(doc):
    """-> (channel ids, message ids) this document only CONSUMES (documents another context's contract).

    A message is consumed when it is tagged `consumed`, or sits only on consumed channels. A channel is
    consumed when every operation on it is tagged `consumed` (2.x publish/subscribe, 3.0 operations)
    or is a 3.0 `action: receive`. Untagged is owned: the gate stays strict by default."""
    chans, msgs = catalog(doc)
    ops = {}  # channel id -> [operation is consumed?]
    for ckey, ch in (doc.get("channels") or {}).items():
        if isinstance(ch, dict):
            cid = str(ch.get("address") or ckey)
            ops.setdefault(cid, []).extend(_tagged_consumed(ch[o]) for o in ("publish", "subscribe") if isinstance(ch.get(o), dict))
    for op in (doc.get("operations") or {}).values():
        ref = (op.get("channel") or {}).get("$ref") if isinstance(op, dict) and isinstance(op.get("channel"), dict) else None
        ch = (doc.get("channels") or {}).get(str(ref).split("/")[-1]) if isinstance(ref, str) else None
        if isinstance(ch, dict):
            ops.setdefault(str(ch.get("address") or str(ref).split("/")[-1]), []).append(
                op.get("action") == "receive" or _tagged_consumed(op))
    cchans = {cid for cid, flags in ops.items() if flags and all(flags)}
    cmsgs = set()
    for mid, node in msgs.items():
        try:
            tagged = _tagged_consumed(deref(node, doc))
        except Unresolved:
            tagged = False
        homes = [cid for cid, members in chans.items() if mid in members]
        if tagged or (homes and all(c in cchans for c in homes)):
            cmsgs.add(mid)
    return cchans, cmsgs


def analyze(base, head):
    """-> (findings, notes). Findings fail the gate. Notes never do: messages the gate could not judge,
    and changes to messages/channels the base only CONSUMES (another context's contract)."""
    out, notes = [], []  # out: (channel id | None, message id | None, text)
    bch, bmsg = catalog(base)
    hch, hmsg = catalog(head)
    gone = set()
    for mid in sorted(set(bmsg) - set(hmsg)):
        out.append((None, mid, f"message {mid} removed"))
        gone.add(mid)
    for cid in sorted(bch):
        if cid not in hch:
            out.append((cid, None, f"channel {cid} removed (carried {len(bch[cid])} message(s))"))
            continue
        for mid in sorted(set(bch[cid]) - set(hch[cid]) - gone):
            elsewhere = any(mid in v for k, v in hch.items() if k != cid)
            out.append((cid, mid, f"message {mid} " + (f"moved off channel {cid}" if elsewhere
                                                       else f"removed from channel {cid}")))
    for mid in sorted(set(bmsg) & set(hmsg)):
        try:
            bp = payload_schema(bmsg[mid], base)
        except Unresolved as exc:
            notes.append(f"message {mid}: base payload does not resolve ({exc.ref}); not judged")
            continue
        try:
            hp = payload_schema(hmsg[mid], head)
            out += [(None, mid, t) for t in compare_schema(mid, bp, hp, base, head)]
        except Unresolved as exc:
            if exc.root is head:
                out.append((None, mid, f"message {mid}: payload no longer resolvable ({exc.ref} removed or renamed)"))
            else:
                notes.append(f"message {mid}: base payload does not resolve ({exc.ref}); not judged")
    cchans, cmsgs = consumed(base)
    findings = []
    for cid, mid, text in dict.fromkeys(out):
        if (mid is not None and mid in cmsgs) or (mid is None and cid in cchans):
            notes.append(f"consumed contract (not blocking): {text}")
        else:
            findings.append(text)
    return findings, notes


# --------------------------------------------------------------------------- IO


def load(path):
    try:
        import yaml  # lazy: the dict API and --self-check need no PyYAML
    except ImportError:
        raise SystemExit("PyYAML missing: python3 -m pip install --require-hashes "
                         "-r .github/requirements/asyncapi-breaking.txt")
    with open(path, encoding="utf-8") as fh:
        doc = yaml.safe_load(fh)
    if not isinstance(doc, dict):
        raise ValueError(f"{path}: not an AsyncAPI document")
    return doc


def consumers_text(arg):
    raw = arg if arg is not None else os.environ.get("CONSUMERS", "")
    names = [n for n in raw.replace(",", " ").split() if n]
    if names:
        return ("Pinning consumers of this apis/asyncapi.yaml (contracts/pinned.json@develop): "
                + ", ".join(names) + ".")
    return "Consumers pin this file byte-for-byte (contracts/pinned.json@develop in the consumer repos)."


def run(base_path, head_path, consumers=None):
    try:
        base, head = load(base_path), load(head_path)
    except (OSError, ValueError) as exc:
        print(f"cannot judge the specs: {exc}")
        return 2
    except Exception as exc:  # yaml.YAMLError and friends
        print(f"cannot judge the specs (unparseable): {exc}")
        return 2
    findings, notes = analyze(base, head)
    for note in notes:
        print(f"::warning::asyncapi: {note}")
    if not findings:
        chans = catalog(head)[0]
        print(f"asyncapi: no breaking change ({len(chans)} channel(s), {len(catalog(head)[1])} message(s) checked).")
        return 0
    print("BREAKING AsyncAPI changes vs the base branch:")
    for line in findings:
        print(f"::error::asyncapi breaking change: {line}")
    print(f"\n{len(findings)} breaking change(s). {consumers_text(consumers)}\n"
          "Make the change additive, or ship it as a new `.v2` type + dataschema (the "
          "envelope rule in apis/asyncapi.yaml); the consumers then refresh their pins "
          "deliberately, never by surprise.")
    return 1


def check(paths):
    """Parse each spec, require every catalogued message to resolve, self-compare == 0."""
    bad = 0
    for path in paths:
        try:
            doc = load(path)
            chans, msgs = catalog(doc)
            no_data = unresolved = 0
            for mid, node in msgs.items():
                try:
                    props = payload_schema(node, doc).get("properties") or {}
                    no_data += "data" not in props
                except Unresolved:
                    unresolved += 1
            self_findings = breaking(doc, copy.deepcopy(doc))
            status = "OK" if not self_findings and not unresolved else "FAIL"
            bad += status != "OK"
            print(f"{status} {path}: asyncapi {doc.get('asyncapi')}, {len(chans)} channel(s), "
                  f"{len(msgs)} message(s), {unresolved} unresolved payload(s), "
                  f"{no_data} without a data property, self-compare {len(self_findings)} finding(s)")
        except SystemExit:
            raise
        except Exception as exc:
            bad += 1
            print(f"CANNOT PARSE {path}: {type(exc).__name__}: {exc}")
    return 2 if bad else 0


# ------------------------------------------------------------------- self-check


def _fixture():
    """A 2.6 channel-perspective spec and the same contract in 3.0 shape."""
    data = {"type": "object", "required": ["id", "qty"],
            "properties": {"id": {"type": "string"}, "qty": {"type": "number", "minimum": 0},
                           "kind": {"type": "string", "enum": ["A", "B"]},
                           "note": {"type": ["string", "null"]}}}
    schemas = {"CloudEvent": {"type": "object", "required": ["specversion", "data"],
                              "properties": {"specversion": {"const": "1.0"}, "data": {"type": "object"}}},
               "FooData": data,
               "FooEvent": {"allOf": [{"$ref": "#/components/schemas/CloudEvent"},
                                      {"properties": {"data": {"$ref": "#/components/schemas/FooData"}}}]}}
    messages = {"Foo": {"payload": {"$ref": "#/components/schemas/FooEvent"}}}
    v2 = {"asyncapi": "2.6.0", "components": {"messages": messages, "schemas": schemas},
          "channels": {"t.events": {"subscribe": {"message": {"oneOf": [{"$ref": "#/components/messages/Foo"}]}}}}}
    v3 = copy.deepcopy(v2)
    v3["asyncapi"] = "3.0.0"
    v3["channels"] = {"events": {"address": "t.events", "messages": {"Foo": {"$ref": "#/components/messages/Foo"}}}}
    return v2, v3


def _drop_foo(doc):
    """Remove message Foo from the catalog AND from its channel (either shape)."""
    doc["components"]["messages"].pop("Foo")
    for ch in doc["channels"].values():
        if "messages" in ch:
            ch["messages"].pop("Foo")
        else:
            ch["subscribe"]["message"]["oneOf"].clear()


def self_check():
    v2, v3 = _fixture()
    mutations = {
        "removed message": (_drop_foo, "message Foo removed"),
        "removed channel": (lambda d: d["channels"].pop(next(iter(d["channels"]))), "t.events removed"),
        "removed required field": (lambda d: d["components"]["schemas"]["FooData"]["properties"].pop("id"),
                                   "id: required field removed"),
        "required -> optional": (lambda d: d["components"]["schemas"]["FooData"]["required"].remove("qty"),
                                 "qty: no longer required"),
        "optional -> required": (lambda d: d["components"]["schemas"]["FooData"]["required"].append("kind"),
                                 "kind: optional field became required"),
        "number -> integer": (lambda d: d["components"]["schemas"]["FooData"]["properties"]["qty"].update(type="integer"),
                              "qty: type narrowed"),
        "null dropped": (lambda d: d["components"]["schemas"]["FooData"]["properties"]["note"].update(type="string"),
                         "note: type narrowed"),
        "enum shrunk": (lambda d: d["components"]["schemas"]["FooData"]["properties"]["kind"].update(enum=["A"]),
                        "kind: enum/const lost"),
        "bound tightened": (lambda d: d["components"]["schemas"]["FooData"]["properties"]["qty"].update(minimum=1),
                            "qty: minimum tightened"),
    }
    for shape, base in (("2.6", v2), ("3.0", v3)):
        assert breaking(base, copy.deepcopy(base)) == [], f"{shape}: identical specs must be clean"
        for case, (edit, want) in mutations.items():
            head = copy.deepcopy(base)
            edit(head)
            got = " | ".join(breaking(base, head))
            assert want in got, f"{shape} {case}: expected {want!r} in {got!r}"
        add = copy.deepcopy(base)
        add["components"]["schemas"]["FooData"]["properties"]["extra"] = {"type": "string"}
        add["components"]["schemas"]["FooData"]["properties"]["kind"]["enum"] = ["A", "B", "C"]
        add["components"]["schemas"]["FooData"]["properties"]["qty"]["type"] = "number"
        add["components"]["messages"]["Bar"] = {"payload": {}}
        assert breaking(base, add) == [], f"{shape}: additive change flagged: {breaking(base, add)}"
    print(f"asyncapi_breaking self-check OK: {len(mutations)} breaking rules x 2 shapes fire, additive passes.")
    return 0


def main(argv):
    ap = argparse.ArgumentParser(description="AsyncAPI breaking-change gate (see the module docstring).")
    ap.add_argument("paths", nargs="*", help="BASE.yaml HEAD.yaml (or SPEC.yaml... with --check)")
    ap.add_argument("--consumers", help="pinning consumer repos, comma separated (default: $CONSUMERS)")
    ap.add_argument("--check", action="store_true", help="parse + resolve + self-compare each spec")
    ap.add_argument("--self-check", "--self-test", dest="self_check", action="store_true")
    args = ap.parse_args(argv)
    if args.self_check:
        return self_check()
    if args.check:
        return check(args.paths) if args.paths else ap.error("--check needs at least one spec")
    if len(args.paths) != 2:
        ap.error("need BASE.yaml HEAD.yaml (or --check SPEC..., or --self-check)")
    return run(args.paths[0], args.paths[1], args.consumers)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
