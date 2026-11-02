import { describe, expect, it } from "vitest";
import { server, http, HttpResponse } from "./test/mocks/server";
import { fetchAllItems } from "./useFetchAll";

describe("fetchAllItems", () => {
  it("follows nextCursor until it is absent", async () => {
    const seen: Array<string | null> = [];
    server.use(
      http.get("http://api.test/things", ({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        seen.push(cursor);
        if (!cursor) return HttpResponse.json({ items: [1, 2], nextCursor: "c2" });
        return HttpResponse.json({ items: [3] });
      }),
    );
    expect(await fetchAllItems<number>("http://api.test/things")).toEqual([1, 2, 3]);
    expect(seen).toEqual([null, "c2"]);
  });
});
