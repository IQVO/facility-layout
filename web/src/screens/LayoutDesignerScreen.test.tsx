import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse, server } from "../test/mocks/server";
import { FACILITY_API_BASE } from "../config";
import { LayoutDesignerScreen } from "./LayoutDesignerScreen";
import type { LocationType, Site, TravelDistance, Zone, ZoneGrid } from "../types";

const SITES: Site[] = [{ siteCode: "WH1", name: "Fulfilment Centre One", status: "Active", siteRole: "UNSPECIFIED" }];
const ZONES: Zone[] = [
  {
    zoneId: "WH1-STOR-AMB",
    siteCode: "WH1",
    areaCode: "STOR",
    zoneCode: "AMB",
    temperatureClass: "Ambient",
    hazmat: false,
    status: "Active",
  },
];
const TYPES: LocationType[] = [
  { name: "PalletRack", role: "Storage", defaultCapacity: { maxWeightKg: 1200, maxVolumeM3: 2.4 } },
];

const GRID: ZoneGrid = {
  zone: ZONES[0],
  columns: [
    { aisleId: "WH1-STOR-AMB-A07", aisleCode: "A07", bay: "01", sequenceHint: 1 },
    { aisleId: "WH1-STOR-AMB-A09", aisleCode: "A09", bay: "03", sequenceHint: 2 },
  ],
  levels: ["01"],
  rows: [
    {
      level: "01",
      cells: [
        {
          positions: [
            { locationCode: "WH1-STOR-AMB-A07-01-01-A", position: "A", locationType: "PalletRack", status: "Active" },
          ],
        },
        {
          positions: [
            { locationCode: "WH1-STOR-AMB-A09-03-01-A", position: "A", locationType: "PalletRack", status: "Active" },
          ],
        },
      ],
    },
  ],
};

function mockCommonFetches() {
  server.use(
    http.get(`${FACILITY_API_BASE}/sites`, () => HttpResponse.json(SITES)),
    http.get(`${FACILITY_API_BASE}/sites/WH1/zones`, () => HttpResponse.json(ZONES)),
    http.get(`${FACILITY_API_BASE}/location-types`, () => HttpResponse.json(TYPES)),
    http.get(`${FACILITY_API_BASE}/zones/WH1-STOR-AMB/grid`, () => HttpResponse.json(GRID)),
  );
}

async function selectSiteAndZone() {
  await screen.findByText("WH1 — Fulfilment Centre One");
  await userEvent.selectOptions(screen.getByLabelText("Site *"), "WH1");
  await screen.findByText("WH1-STOR-AMB (Ambient)");
  await userEvent.selectOptions(screen.getByLabelText("Zone *"), "WH1-STOR-AMB");
  await screen.findByText("Grid — WH1-STOR-AMB");
}

describe("LayoutDesignerScreen — distance probe", () => {
  it("measures the distance between two clicked slots and shows the result", async () => {
    mockCommonFetches();
    const distance: TravelDistance = {
      metresM: 4.4,
      estimated: false,
      route: [
        { aisleId: "WH1-STOR-AMB-A07", bay: "01" },
        { aisleId: "WH1-STOR-AMB-A09", bay: "03" },
      ],
    };
    server.use(
      http.get(`${FACILITY_API_BASE}/distance`, ({ request }) => {
        const url = new URL(request.url);
        expect(url.searchParams.get("from")).toBe("WH1-STOR-AMB-A07-01-01-A");
        expect(url.searchParams.get("to")).toBe("WH1-STOR-AMB-A09-03-01-A");
        return HttpResponse.json(distance);
      }),
    );

    render(<LayoutDesignerScreen />);
    await selectSiteAndZone();

    await userEvent.click(screen.getByRole("button", { name: "Measure travel distance" }));

    const rects = screen.getAllByTestId("konva-rect");
    await userEvent.click(rects[0]);
    await userEvent.click(rects[1]);

    await userEvent.click(screen.getByRole("button", { name: "Measure" }));

    expect(await screen.findByText("4.4m")).toBeInTheDocument();
    expect(screen.getByText("2 waypoints on the shortest route")).toBeInTheDocument();
    expect(screen.queryByText("Estimated")).not.toBeInTheDocument();
  });

  it("shows the Estimated pill when the route used a bay-pitch fallback", async () => {
    mockCommonFetches();
    server.use(
      http.get(`${FACILITY_API_BASE}/distance`, () =>
        HttpResponse.json({ metresM: 12.0, estimated: true, route: [] } satisfies TravelDistance),
      ),
    );

    render(<LayoutDesignerScreen />);
    await selectSiteAndZone();
    await userEvent.click(screen.getByRole("button", { name: "Measure travel distance" }));

    const rects = screen.getAllByTestId("konva-rect");
    await userEvent.click(rects[0]);
    await userEvent.click(rects[1]);
    await userEvent.click(screen.getByRole("button", { name: "Measure" }));

    expect(await screen.findByText("12.0m")).toBeInTheDocument();
    expect(screen.getByText("Estimated")).toBeInTheDocument();
  });

  it("surfaces the RFC 7807 detail when the two locations have no route between them", async () => {
    mockCommonFetches();
    server.use(
      http.get(`${FACILITY_API_BASE}/distance`, () =>
        HttpResponse.json(
          {
            type: "https://errors.facility-layout.warehouse-systems.dev/no-route-between-zones",
            title: "No route between zones",
            status: 422,
            detail: "from and to are in different zones",
          },
          { status: 422 },
        ),
      ),
    );

    render(<LayoutDesignerScreen />);
    await selectSiteAndZone();
    await userEvent.click(screen.getByRole("button", { name: "Measure travel distance" }));

    const rects = screen.getAllByTestId("konva-rect");
    await userEvent.click(rects[0]);
    await userEvent.click(rects[1]);
    await userEvent.click(screen.getByRole("button", { name: "Measure" }));

    expect(await screen.findByText("from and to are in different zones")).toBeInTheDocument();
  });

  it("Measure stays disabled until both From and To are set", async () => {
    mockCommonFetches();
    render(<LayoutDesignerScreen />);
    await selectSiteAndZone();
    await userEvent.click(screen.getByRole("button", { name: "Measure travel distance" }));

    expect(screen.getByRole("button", { name: "Measure" })).toBeDisabled();

    const rects = screen.getAllByTestId("konva-rect");
    await userEvent.click(rects[0]);
    expect(screen.getByRole("button", { name: "Measure" })).toBeDisabled();

    await userEvent.click(rects[1]);
    expect(screen.getByRole("button", { name: "Measure" })).toBeEnabled();
  });

  it("Reset clears both endpoints and the result", async () => {
    mockCommonFetches();
    server.use(
      http.get(`${FACILITY_API_BASE}/distance`, () =>
        HttpResponse.json({ metresM: 4.4, estimated: false, route: [] } satisfies TravelDistance),
      ),
    );

    render(<LayoutDesignerScreen />);
    await selectSiteAndZone();
    await userEvent.click(screen.getByRole("button", { name: "Measure travel distance" }));

    const rects = screen.getAllByTestId("konva-rect");
    await userEvent.click(rects[0]);
    await userEvent.click(rects[1]);
    await userEvent.click(screen.getByRole("button", { name: "Measure" }));
    await screen.findByText("4.4m");

    await userEvent.click(screen.getByRole("button", { name: "Reset" }));

    expect(screen.queryByText("4.4m")).not.toBeInTheDocument();
    expect(
      screen.getByText((_, el) => el?.textContent === "From: —To: —" && el.tagName === "DIV"),
    ).toBeInTheDocument();
  });

  it("exiting the probe restores the normal register-slot click flow", async () => {
    mockCommonFetches();
    render(<LayoutDesignerScreen />);
    await selectSiteAndZone();

    await userEvent.click(screen.getByRole("button", { name: "Measure travel distance" }));
    await userEvent.click(screen.getByRole("button", { name: "Exit distance probe" }));

    const rects = screen.getAllByTestId("konva-rect");
    await userEvent.click(rects[0]);

    expect(await screen.findByText(/Register a slot/)).toBeInTheDocument();
  });
});
