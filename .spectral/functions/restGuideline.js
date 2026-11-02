// Warehouse REST API guideline conformance (Spectral custom function).
//
// Canonical copy: warehouse-docs docs/standards/rest-api-guideline.md (rules R1-R9).
// Applied to the whole document (`given: $`) so it can reason about paths and
// operations together. Every finding names the rule id, e.g. "[R3] ...".

const HTTP = ["get", "post", "put", "patch", "delete"];

// Verbs that must never appear as a path segment (R2). Plural nouns such as
// "receipts", "approvals" or "cancellations" are fine; only the bare verb form is banned.
const VERBS = new Set([
  "accept", "activate", "add", "approve", "arrive", "assign", "authorize", "book", "cancel",
  "capture", "certify", "change", "check-in", "check-out", "checkin", "checkout-now", "claim",
  "claim-next", "close", "complete", "confirm", "decommission", "deactivate", "define",
  "discard", "dispatch", "end", "end-shift", "evaluate", "expire", "expire-leases", "generate",
  "import", "inspect", "issue", "make", "merge", "merge-into", "move", "override",
  "propose", "publish", "quote", "reauthorize", "receive", "record", "reduce", "register",
  "reinstate", "reject", "release", "remove", "rename", "renew", "renew-lease", "reopen",
  "request", "requote", "resolve", "retire", "retry", "retry-allocation", "revise", "revoke",
  "run", "schedule", "seal", "seal-package", "requoted", "select", "set", "set-quantity", "ship", "slam", "start",
  "start-shift", "stow", "submit", "suspend", "sweep", "sweep-cpt-misses", "triage",
  "unassign", "void", "withdraw", "cycle-count", "confirm-pick", "break-case",
]);

// Collection GETs that are not resource collections (R7 exemptions are explicit via
// x-pagination-exempt, plus these structural ones that never page).
const STRUCTURAL = /(^|\/)(healthz|readyz|livez|metrics|openapi\.json|openapi\.yaml)$|\/reports\/|\/current$|freshness$/;

function isCollectionPath(p) {
  const segs = p.split("/").filter(Boolean);
  if (segs.length === 0) return false;
  const last = segs[segs.length - 1];
  return !(last.startsWith("{") && last.endsWith("}"));
}

function hasParam(op, name, where) {
  return (op.parameters || []).some((p) => p && p.name === name && (!where || p.in === where));
}

export default function restGuideline(doc) {
  const out = [];
  const paths = (doc && doc.paths) || {};
  for (const [p, item] of Object.entries(paths)) {
    if (!item || typeof item !== "object") continue;

    // R1: no custom-method colon suffix.
    if (/:[A-Za-z]/.test(p)) {
      out.push({ message: `[R1] path "${p}" uses a :custom-method suffix; model the act as a noun sub-resource or a state change`, path: ["paths", p] });
    }
    // R2: no verb segments.
    for (const seg of p.split("/").filter(Boolean)) {
      if (seg.startsWith("{")) continue;
      const bare = seg.replace(/^v\d+$/, "");
      if (bare && VERBS.has(bare.toLowerCase())) {
        out.push({ message: `[R2] path "${p}" has the verb segment "${seg}"; use a noun (see guideline verb-to-noun table)`, path: ["paths", p] });
        break;
      }
    }

    for (const m of HTTP) {
      const op = item[m];
      if (!op || typeof op !== "object") continue;
      const at = ["paths", p, m];

      if (m === "post" || m === "patch") {
        // R3: Idempotency-Key header declared on every POST and PATCH.
        if (!hasParam(op, "Idempotency-Key", "header")) {
          out.push({ message: `[R3] ${m.toUpperCase()} ${p} does not declare the Idempotency-Key header parameter`, path: [...at, "parameters"] });
        }
        const responses = op.responses || {};
        // R4: key problems: 400 required/invalid, 409 reuse.
        if (!responses["400"]) out.push({ message: `[R4] ${m.toUpperCase()} ${p} lacks a 400 response (idempotency-key-required)`, path: [...at, "responses"] });
        if (!responses["409"]) out.push({ message: `[R4] ${m.toUpperCase()} ${p} lacks a 409 response (idempotency-key-reused)`, path: [...at, "responses"] });
        // R5: every 2xx documents Idempotency-Replayed.
        for (const [code, r] of Object.entries(responses)) {
          if (!/^2/.test(code) || !r) continue;
          const hs = r.headers || {};
          if (!hs["Idempotency-Replayed"]) {
            out.push({ message: `[R5] ${m.toUpperCase()} ${p} ${code} does not document the Idempotency-Replayed response header`, path: [...at, "responses", code] });
          }
        }
      }

      // R6: banned spelling anywhere in response headers.
      for (const [code, r] of Object.entries(op.responses || {})) {
        if (r && r.headers && r.headers["Idempotent-Replayed"]) {
          out.push({ message: `[R6] ${m.toUpperCase()} ${p} ${code} uses the retired header spelling Idempotent-Replayed`, path: [...at, "responses", code, "headers"] });
        }
      }

      // R7: collection GETs page with limit + cursor and answer { items, nextCursor }.
      // A GET is a collection when it is not addressed by a trailing {id} and its 200 body is
      // (or contains) a list. Composite single-resource views and mandatory-window reports are
      // exempt only through x-pagination-exempt: view | report.
      if (m === "get" && isCollectionPath(p) && !STRUCTURAL.test(p)) {
        const ok = op.responses && op.responses["200"];
        const content = ok && ok.content && (ok.content["application/json"] || Object.values(ok.content)[0]);
        const schema = content && content.schema;
        const props = (schema && schema.properties) || {};
        const returnsList = schema && (schema.type === "array" || Object.values(props).some((v) => v && v.type === "array"));
        if (!returnsList) continue;
        const ex = op["x-pagination-exempt"];
        if (ex) {
          if (ex !== "view" && ex !== "report") {
            out.push({ message: `[R7] GET ${p} x-pagination-exempt must be "view" or "report", got "${ex}"`, path: [...at, "x-pagination-exempt"] });
          }
          continue;
        }
        if (!hasParam(op, "limit", "query") || !hasParam(op, "cursor", "query")) {
          out.push({ message: `[R7] GET ${p} is a collection but lacks limit/cursor query parameters (or x-pagination-exempt: view|report)`, path: [...at, "parameters"] });
        }
        const isEnvelope = schema.type === "object" && props.items && props.items.type === "array" && props.nextCursor;
        if (!isEnvelope) {
          out.push({ message: `[R7] GET ${p} 200 response must be an object { items: [...], nextCursor }`, path: [...at, "responses", "200"] });
        }
      }

      // R8: the retired idempotency statuses: 422 must not be the reuse answer.
      if ((m === "post" || m === "patch") && op.responses && op.responses["422"]) {
        const d = JSON.stringify(op.responses["422"]).toLowerCase();
        if (d.includes("idempotency-key-reused") || d.includes("idempotency-key was already used")) {
          out.push({ message: `[R8] ${m.toUpperCase()} ${p} answers Idempotency-Key reuse with 422; it must be 409`, path: [...at, "responses", "422"] });
        }
      }
    }

    // R9: 'DELETE'/'PUT' are idempotent by method; nothing to enforce here (documented only).
  }
  return out;
}
