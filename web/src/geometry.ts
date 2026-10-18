import type { Dimensions, Point3D } from "./types";

/**
 * Client-side geometry validation (ADR-0017), mirroring the server's own
 * rules exactly (internal/domain/shared/geometry.go) so a bad value is
 * caught before the round-trip, while anything the client cannot judge is
 * still answered by the server and surfaced verbatim:
 *
 *   - xM/yM may be any finite number; zM is height above floor and must
 *     be >= 0 ("z coordinate must not be negative");
 *   - widthM/depthM/heightM must all be strictly > 0 ("width, depth, and
 *     height must all be greater than zero");
 *   - a centreline's start and end must differ ("segment start and end
 *     must differ").
 *
 * Empty is treated as omitted: the geometry PUTs replace the full
 * position+dimensions snapshot, so the forms require every field and
 * "empty" simply means the operator has not filled it in yet.
 */

export interface Point3DFields {
  xM: string;
  yM: string;
  zM: string;
}

export interface DimensionsFields {
  widthM: string;
  depthM: string;
  heightM: string;
}

/** Parses one numeric text field; null (with no message) means "empty". */
function parseNumber(label: string, raw: string): { value: number } | { empty: true } | { error: string } {
  const trimmed = raw.trim();
  if (trimmed === "") return { empty: true };
  const value = Number(trimmed);
  if (!Number.isFinite(value)) {
    return { error: `${label} must be a number, got "${trimmed}"` };
  }
  return { value };
}

export function parsePoint3D(prefix: string, fields: Point3DFields): { point: Point3D } | { error: string } {
  const out: Record<string, number> = {};
  for (const key of ["xM", "yM", "zM"] as const) {
    const parsed = parseNumber(`${prefix} ${key}`, fields[key]);
    if ("error" in parsed) return { error: parsed.error };
    if ("empty" in parsed) return { error: `${prefix} ${key} is required` };
    out[key] = parsed.value;
  }
  if (out.zM < 0) {
    return { error: "z coordinate must not be negative" };
  }
  return { point: { xM: out.xM, yM: out.yM, zM: out.zM } };
}

export function parseDimensions(fields: DimensionsFields): { dimensions: Dimensions } | { error: string } {
  const out: Record<string, number> = {};
  for (const key of ["widthM", "depthM", "heightM"] as const) {
    const parsed = parseNumber(key, fields[key]);
    if ("error" in parsed) return { error: parsed.error };
    if ("empty" in parsed) return { error: `${key} is required` };
    out[key] = parsed.value;
  }
  if (out.widthM <= 0 || out.depthM <= 0 || out.heightM <= 0) {
    return { error: "width, depth, and height must all be greater than zero" };
  }
  return { dimensions: { widthM: out.widthM, depthM: out.depthM, heightM: out.heightM } };
}

/** Aisle centreline: two points that must not coincide. */
export function parseCentreline(
  start: Point3DFields,
  end: Point3DFields,
): { start: Point3D; end: Point3D } | { error: string } {
  const s = parsePoint3D("centreline start", start);
  if ("error" in s) return s;
  const e = parsePoint3D("centreline end", end);
  if ("error" in e) return e;
  if (s.point.xM === e.point.xM && s.point.yM === e.point.yM && s.point.zM === e.point.zM) {
    return { error: "segment start and end must differ" };
  }
  return { start: s.point, end: e.point };
}

/** Renders a stored point/dimension back into form-friendly strings. */
export function fmt(n: number | undefined): string {
  return n === undefined ? "" : String(n);
}
