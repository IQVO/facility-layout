import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse, server } from "../../test/mocks/server";
import { FACILITY_API_BASE } from "../../config";
import { SitesPanel } from "./SitesPanel";

describe("SitesPanel", () => {
  it("lists sites returned by GET /sites", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/sites`, () =>
        HttpResponse.json({ items: [{ siteCode: "WH1", name: "Fulfilment Centre One", status: "Active" }] }),
      ),
    );

    render(<SitesPanel />);

    expect(await screen.findByText("WH1")).toBeInTheDocument();
    expect(screen.getByText("Fulfilment Centre One")).toBeInTheDocument();
  });

  it("registers a new site and shows a success message", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/sites`, () => HttpResponse.json({ items: [] })),
      http.post(`${FACILITY_API_BASE}/sites`, async ({ request }) => {
        const body = (await request.json()) as { siteCode: string; name: string };
        return HttpResponse.json(
          { siteCode: body.siteCode, name: body.name, status: "Active" },
          { status: 201 },
        );
      }),
    );

    render(<SitesPanel />);
    await userEvent.type(screen.getByLabelText("Site code *"), "WH2");
    await userEvent.type(screen.getByLabelText("Name *"), "Reno FC");
    await userEvent.click(screen.getByRole("button", { name: "Register site" }));

    await waitFor(() =>
      expect(screen.getByText("Site WH2 registered.")).toBeInTheDocument(),
    );
  });

  it("shows the RFC 7807 problem detail on a 409 conflict", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/sites`, () => HttpResponse.json({ items: [] })),
      http.post(`${FACILITY_API_BASE}/sites`, () =>
        HttpResponse.json(
          {
            type: "https://errors.facility-layout.warehouse-systems.dev/duplicate-site-code",
            title: "A site with this code already exists",
            status: 409,
            detail: "a site with this code already exists",
          },
          { status: 409 },
        ),
      ),
    );

    render(<SitesPanel />);
    await userEvent.type(screen.getByLabelText("Site code *"), "WH1");
    await userEvent.type(screen.getByLabelText("Name *"), "Dup");
    await userEvent.click(screen.getByRole("button", { name: "Register site" }));

    expect(await screen.findByText("a site with this code already exists")).toBeInTheDocument();
  });
});
