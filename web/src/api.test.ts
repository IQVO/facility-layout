import { describe, expect, it, vi, afterEach } from "vitest";
import { apiPost, apiGet, ApiError } from "./api";

describe("apiPost", () => {
  const originalFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it("sends a per-submit Idempotency-Key header (ADR-0019)", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: async () => ({ siteCode: "WH1", name: "Test", status: "Active" }),
    }) as unknown as typeof fetch;
    globalThis.fetch = fetchMock;

    await apiPost("/sites", { siteCode: "WH1", name: "Test" });
    const init = (fetchMock as ReturnType<typeof vi.fn>).mock.calls[0][1] as RequestInit;
    const key = (init.headers as Record<string, string>)["Idempotency-Key"];
    if (!key || key.length < 8) {
      throw new Error(`expected a non-empty Idempotency-Key header, got ${JSON.stringify(key)}`);
    }

    // A second submit must carry a DIFFERENT key: one key per logical submit,
    // so a user clicking save twice is two creates, not a replay.
    await apiPost("/sites", { siteCode: "WH1", name: "Test" });
    const secondInit = (fetchMock as ReturnType<typeof vi.fn>).mock.calls[1][1] as RequestInit;
    const secondKey = (secondInit.headers as Record<string, string>)["Idempotency-Key"];
    if (secondKey === key) {
      throw new Error("two submits must not share one Idempotency-Key");
    }
  });

  it("returns the parsed JSON body on success", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: async () => ({ siteCode: "WH1", name: "Test", status: "Active" }),
    }) as unknown as typeof fetch;

    const result = await apiPost("/sites", { siteCode: "WH1", name: "Test" });
    expect(result).toEqual({ siteCode: "WH1", name: "Test", status: "Active" });
  });

  it("returns undefined on a 204 No Content response", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 204,
    }) as unknown as typeof fetch;

    const result = await apiPost("/locations/WH1-STOR-AMB-A07-03-02-B/decommission", {});
    expect(result).toBeUndefined();
  });

  it("throws ApiError with the parsed RFC 7807 problem detail on failure", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      statusText: "Conflict",
      json: async () => ({
        type: "https://errors.facility-layout.warehouse-systems.dev/duplicate-site-code",
        title: "A site with this code already exists",
        status: 409,
        detail: "a site with this code already exists",
      }),
    }) as unknown as typeof fetch;

    await expect(apiPost("/sites", { siteCode: "WH1", name: "Test" })).rejects.toThrow(
      ApiError,
    );
    await expect(apiPost("/sites", { siteCode: "WH1", name: "Test" })).rejects.toThrow(
      "a site with this code already exists",
    );
  });

  it("falls back to statusText when the error body is not JSON", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      statusText: "Internal Server Error",
      json: async () => {
        throw new Error("not json");
      },
    }) as unknown as typeof fetch;

    await expect(apiPost("/sites", {})).rejects.toThrow("500 Internal Server Error");
  });
});

describe("apiGet", () => {
  const originalFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it("returns the parsed JSON body on success", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ metresM: 4.4, estimated: false, route: [] }),
    }) as unknown as typeof fetch;

    const result = await apiGet("/distance?from=A&to=B");
    expect(result).toEqual({ metresM: 4.4, estimated: false, route: [] });
  });

  it("throws ApiError with the parsed RFC 7807 problem detail on failure", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 422,
      statusText: "Unprocessable Entity",
      json: async () => ({
        type: "https://errors.facility-layout.warehouse-systems.dev/no-route-between-zones",
        title: "No route between zones",
        status: 422,
        detail: "from and to are in different zones",
      }),
    }) as unknown as typeof fetch;

    await expect(apiGet("/distance?from=A&to=B")).rejects.toThrow(ApiError);
    await expect(apiGet("/distance?from=A&to=B")).rejects.toThrow(
      "from and to are in different zones",
    );
  });

  it("falls back to statusText when the error body is not JSON", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 404,
      statusText: "Not Found",
      json: async () => {
        throw new Error("not json");
      },
    }) as unknown as typeof fetch;

    await expect(apiGet("/distance?from=A&to=B")).rejects.toThrow("404 Not Found");
  });
});
