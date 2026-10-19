"""Schemathesis hooks for scripts/contract-test.sh.

Every resource-creation POST of this service sits behind the Idempotency-Key
middleware (ADR 0019), which is enforced in every deployment now that Postgres
is the only runtime (ADR 0039). The OpenAPI document gives the parameter one
fixed `example`, so Schemathesis would send the SAME key with DIFFERENT bodies
and rightly get 422 `idempotency-key-reused`. A real client sends a fresh UUID
per logical submit; this hook does the same for every request that carries the
header. A request that deliberately omits the header (negative testing of the
required parameter) is left alone and must still be answered 400.
"""

import uuid

import schemathesis


@schemathesis.hook
def before_call(ctx, case, **kwargs):
    if case.headers and "Idempotency-Key" in case.headers:
        case.headers["Idempotency-Key"] = str(uuid.uuid4())
    return None
