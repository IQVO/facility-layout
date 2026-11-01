import { useEffect, useState } from "react";

/** Largest page the API serves (REST guideline 3.2: limit 1-500). */
const PAGE_LIMIT = 500;

interface Page<T> {
  items: T[];
  nextCursor?: string;
}

/**
 * fetchAllItems reads a keyset-paginated collection ({items,nextCursor}) to
 * the end: it follows `nextCursor` until it is absent, so a collection that
 * outgrows one page is never silently truncated (REST guideline 5.2).
 */
export async function fetchAllItems<T>(url: string): Promise<T[]> {
  const out: T[] = [];
  let cursor: string | undefined;
  do {
    const sep = url.includes("?") ? "&" : "?";
    let next = `${url}${sep}limit=${PAGE_LIMIT}`;
    if (cursor) next += `&cursor=${encodeURIComponent(cursor)}`;
    const res = await fetch(next);
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
    const body = (await res.json()) as Page<T>;
    out.push(...body.items);
    cursor = body.nextCursor;
  } while (cursor);
  return out;
}

export interface FetchAllState<T> {
  data: T[] | null;
  loading: boolean;
  error: Error | null;
}

interface Settled<T> {
  key: string;
  data: T[] | null;
  error: Error | null;
}

/**
 * useFetchAll loads every page of a collection. A null url disables the
 * request; bumping `refreshKey` reloads it (the old `?_r=` cache-busting
 * query parameter is gone: collections reject undeclared query parameters).
 */
export function useFetchAll<T>(url: string | null, refreshKey = 0): FetchAllState<T> {
  const key = `${refreshKey}|${url ?? ""}`;
  const [settled, setSettled] = useState<Settled<T> | null>(null);
  useEffect(() => {
    if (url === null) return;
    let cancelled = false;
    fetchAllItems<T>(url)
      .then((data) => {
        if (!cancelled) setSettled({ key, data, error: null });
      })
      .catch((e: unknown) => {
        if (!cancelled) setSettled({ key, data: null, error: e instanceof Error ? e : new Error(String(e)) });
      });
    return () => {
      cancelled = true;
    };
  }, [url, key]);
  if (url === null) return { data: null, loading: false, error: null };
  if (settled === null || settled.key !== key) return { data: null, loading: true, error: null };
  return { data: settled.data, loading: false, error: settled.error };
}
