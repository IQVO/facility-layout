import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse, server } from "../../test/mocks/server";
import { FACILITY_API_BASE } from "../../config";
import { SiteNetworkPanel } from "./SiteNetworkPanel";

const SITE = {
  siteCode: "WH1",
  name: "Fulfilment Centre One",
  status: "Active",
  siteRole: "NON_SORTABLE_FC",
};

const CAPABILITY = {
  siteCode: "WH1",
  transferOriginEnabled: true,
  transferDestinationEnabled: false,
  capabilityRevision: 3,
};

const DEFAULTS = [
  { siteRole: "NON_SORTABLE_FC", transferOriginEnabled: true, transferDestinationEnabled: true },
  { siteRole: "MICRO_FC", transferOriginEnabled: false, transferDestinationEnabled: true },
  { siteRole: "DELIVERY_STATION", transferOriginEnabled: false, transferDestinationEnabled: false },
];

function mockBasics() {
  server.use(
    http.get(`${FACILITY_API_BASE}/sites`, () => HttpResponse.json([SITE])),
    http.get(`${FACILITY_API_BASE}/site-roles/transfer-defaults`, () => HttpResponse.json(DEFAULTS)),
  );
}

async function loadSite(code = "WH1") {
  await userEvent.type(screen.getByLabelText("Site code *"), code);
  await userEvent.click(screen.getByRole("button", { name: "Load site" }));
  await waitFor(() => expect(screen.getByText("Fulfilment Centre One")).toBeInTheDocument());
}

describe("SiteNetworkPanel", () => {
  it("loads a site, its current role and its transfer capability", async () => {
    mockBasics();
    server.use(
      http.get(`${FACILITY_API_BASE}/sites/WH1`, () => HttpResponse.json(SITE)),
      http.get(`${FACILITY_API_BASE}/sites/WH1/transfer-capability`, () =>
        HttpResponse.json(CAPABILITY),
      ),
    );

    render(<SiteNetworkPanel />);
    await loadSite();

    expect(screen.getByText("current role: Non-sortable FC")).toBeInTheDocument();
    expect(screen.getByText("capability revision 3")).toBeInTheDocument();
    expect(
      (screen.getByRole("checkbox", { name: /Transfer origin/ }) as HTMLInputElement).checked,
    ).toBe(true);
    expect(
      (screen.getByRole("checkbox", { name: /Transfer destination/ }) as HTMLInputElement).checked,
    ).toBe(false);
  });

  it("renders the transfer-defaults reference table", async () => {
    mockBasics();

    render(<SiteNetworkPanel />);

    expect(await screen.findByText("Non-sortable FC")).toBeInTheDocument();
    expect(screen.getByText("Micro FC")).toBeInTheDocument();
    expect(screen.getByText("Delivery station")).toBeInTheDocument();
    expect(screen.getAllByText("yes").length).toBeGreaterThanOrEqual(3);
  });

  it("PUTs the chosen role and reports success", async () => {
    mockBasics();
    server.use(
      http.get(`${FACILITY_API_BASE}/sites/WH1`, () => HttpResponse.json(SITE)),
      http.get(`${FACILITY_API_BASE}/sites/WH1/transfer-capability`, () =>
        HttpResponse.json(CAPABILITY),
      ),
      http.put(`${FACILITY_API_BASE}/sites/WH1/role`, async ({ request }) => {
        const body = (await request.json()) as { siteRole: string };
        return HttpResponse.json({ ...SITE, siteRole: body.siteRole });
      }),
    );

    render(<SiteNetworkPanel />);
    await loadSite();

    await userEvent.selectOptions(screen.getByLabelText("Site role (ADR-0037) *"), "DELIVERY_STATION");
    await userEvent.click(screen.getByRole("button", { name: "Set role" }));

    await waitFor(() =>
      expect(screen.getByText("Site WH1 role set to Delivery station.")).toBeInTheDocument(),
    );
    expect(screen.getByText("current role: Delivery station")).toBeInTheDocument();
  });

  it("renders the server's 422 invalid-site-role problem detail verbatim", async () => {
    mockBasics();
    server.use(
      http.get(`${FACILITY_API_BASE}/sites/WH1`, () => HttpResponse.json(SITE)),
      http.get(`${FACILITY_API_BASE}/sites/WH1/transfer-capability`, () =>
        HttpResponse.json(CAPABILITY),
      ),
      http.put(`${FACILITY_API_BASE}/sites/WH1/role`, () =>
        HttpResponse.json(
          {
            type: "https://errors.facility-layout.warehouse-systems.dev/invalid-site-role",
            title: "Site role must be one of the known site roles",
            status: 422,
            detail:
              'site role must be one of the known site roles: "UNSPECIFIED" cannot be assigned',
          },
          { status: 422 },
        ),
      ),
    );

    render(<SiteNetworkPanel />);
    await loadSite();

    await userEvent.selectOptions(screen.getByLabelText("Site role (ADR-0037) *"), "MICRO_FC");
    await userEvent.click(screen.getByRole("button", { name: "Set role" }));

    expect(await screen.findByText(/"UNSPECIFIED" cannot be assigned/)).toBeInTheDocument();
  });

  it("PUTs the full transfer-capability snapshot and reports the new revision", async () => {
    mockBasics();
    server.use(
      http.get(`${FACILITY_API_BASE}/sites/WH1`, () => HttpResponse.json(SITE)),
      http.get(`${FACILITY_API_BASE}/sites/WH1/transfer-capability`, () =>
        HttpResponse.json(CAPABILITY),
      ),
      http.put(`${FACILITY_API_BASE}/sites/WH1/transfer-capability`, async ({ request }) => {
        const body = (await request.json()) as {
          transferOriginEnabled: boolean;
          transferDestinationEnabled: boolean;
        };
        expect(body).toEqual({ transferOriginEnabled: true, transferDestinationEnabled: true });
        return HttpResponse.json({
          siteCode: "WH1",
          transferOriginEnabled: body.transferOriginEnabled,
          transferDestinationEnabled: body.transferDestinationEnabled,
          capabilityRevision: 4,
        });
      }),
    );

    render(<SiteNetworkPanel />);
    await loadSite();

    await userEvent.click(screen.getByRole("checkbox", { name: /Transfer destination/ }));
    await userEvent.click(screen.getByRole("button", { name: "Save capability" }));

    await waitFor(() =>
      expect(
        screen.getByText("Transfer capability saved (origin on, destination on, revision 4)."),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("capability revision 4")).toBeInTheDocument();
  });

  it("Apply role's defaults copies the role's reference row into the checkboxes", async () => {
    mockBasics();
    server.use(
      http.get(`${FACILITY_API_BASE}/sites/WH1`, () => HttpResponse.json(SITE)),
      http.get(`${FACILITY_API_BASE}/sites/WH1/transfer-capability`, () =>
        HttpResponse.json(CAPABILITY),
      ),
    );

    render(<SiteNetworkPanel />);
    await loadSite();

    // Loaded capability: origin on, destination off. NON_SORTABLE_FC's
    // defaults row says both true -- applying it must switch destination on.
    await userEvent.click(screen.getByRole("button", { name: "Apply role's defaults" }));

    const origin = screen.getByRole("checkbox", { name: /Transfer origin/ }) as HTMLInputElement;
    const destination = screen.getByRole("checkbox", {
      name: /Transfer destination/,
    }) as HTMLInputElement;
    expect(origin.checked).toBe(true);
    expect(destination.checked).toBe(true);
  });

  it("renders the 404 problem detail for an unknown site", async () => {
    mockBasics();
    server.use(
      http.get(`${FACILITY_API_BASE}/sites/NOPE`, () =>
        HttpResponse.json(
          {
            type: "https://errors.facility-layout.warehouse-systems.dev/site-not-found",
            title: "Site not found",
            status: 404,
            detail: "no site with code \"NOPE\"",
          },
          { status: 404 },
        ),
      ),
    );

    render(<SiteNetworkPanel />);
    await userEvent.type(screen.getByLabelText("Site code *"), "NOPE");
    await userEvent.click(screen.getByRole("button", { name: "Load site" }));

    expect(await screen.findByText('no site with code "NOPE"')).toBeInTheDocument();
  });
});
