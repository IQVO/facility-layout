import { FACILITY_API_BASE } from "./config";

/** RFC 7807 problem+json body every error response from facility-layout
 *  returns, per CLAUDE.md's "go straight to RFC 7807" mandate. */
export interface ProblemDetails {
  type: string;
  title: string;
  status: number;
  detail: string;
  instance?: string;
}

export class ApiError extends Error {
  problem: ProblemDetails | null;
  status: number;

  constructor(status: number, problem: ProblemDetails | null, fallbackMessage: string) {
    super(problem?.detail || problem?.title || fallbackMessage);
    this.status = status;
    this.problem = problem;
  }
}

/**
 * POST/PATCH-style JSON call shared by every config form on this screen.
 * Parses an RFC 7807 problem+json body on failure so forms can surface the
 * exact domain-error detail (e.g. "placement-rule-violated: ...") instead
 * of a generic "request failed".
 *
 * Every call sends a fresh per-submit `Idempotency-Key` header
 * (crypto.randomUUID(), ADR-0019): the API's resource-creation POSTs are
 * wrapped by the server's idempotency middleware whenever it runs with
 * Postgres, and without a key each create form would be rejected with 400
 * `idempotency-key-required`. The key is minted once per apiPost CALL — a
 * user clicking "save" twice is two submits and must get two keys, while
 * the browser/network retrying one submit reuses that submit's key.
 */
export async function apiPost<TResponse>(
  path: string,
  body: unknown,
): Promise<TResponse> {
  const res = await fetch(`${FACILITY_API_BASE}${path}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": newIdempotencyKey(),
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`);
  }
  if (res.status === 204) return undefined as TResponse;
  return (await res.json()) as TResponse;
}

/**
 * newIdempotencyKey mints the per-submit Idempotency-Key. Uses
 * crypto.randomUUID() where available (every browser this remote targets);
 * the explicit fallback keeps vitest's jsdom-free node environment and any
 * older embedded webview working — it only has to be unique per submit, not
 * cryptographically strong.
 */
function newIdempotencyKey(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (c && typeof c.randomUUID === "function") {
    return c.randomUUID();
  }
  return `mfe-${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
}

/**
 * GET call with the same RFC 7807 error handling as apiPost, for
 * on-demand reads triggered by a user action (e.g. "Measure" on the
 * distance probe) rather than useFetch's declarative load-on-mount/poll
 * shape -- the caller wants the exact domain-error detail (e.g.
 * "no-route-between-zones") the instant the action is taken, not a
 * background refresh's generic FetchError.
 */
export async function apiGet<TResponse>(path: string): Promise<TResponse> {
  const res = await fetch(`${FACILITY_API_BASE}${path}`);
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as TResponse;
}
