import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse, server } from "../test/mocks/server";
import { FACILITY_API_BASE } from "../config";
import { FacilityScreen } from "./FacilityScreen";
import type { SiteLayout } from "../types";

const LAYOUT: SiteLayout = {
  site: { siteCode: "WH1", name: "Fulfilment Centre One", status: "Active", siteRole: "UNSPECIFIED" },
  totals: { zones: 2, aisles: 2, slots: 2 },
  zones: [
    {
      zoneId: "WH1-DOCK-OB",
      siteCode: "WH1",
      areaCode: "DOCK",
      zoneCode: "OB",
      temperatureClass: "Ambient",
      hazmat: false,
      status: "Active",
      aisles: [
        {
          aisleId: "WH1-DOCK-OB-D01",
          zoneId: "WH1-DOCK-OB",
          aisleCode: "D01",
          sequenceHint: 1,
          direction: "TwoWay",
          status: "Active",
          slots: [
            {
              locationCode: "WH1-DOCK-OB-D01-01-01-A",
              zoneId: "WH1-DOCK-OB",
              aisleId: "WH1-DOCK-OB-D01",
              coordinates: {
                site: "WH1",
                area: "DOCK",
                zone: "OB",
                aisle: "D01",
                bay: "01",
                level: "01",
                position: "A",
              },
              locationType: "DockDoor",
              role: "Dock",
              dockFlow: "Outbound",
              capacity: { maxWeightKg: 0, maxVolumeM3: 0 },
              status: "Active",
            },
          ],
        },
      ],
    },
    {
      zoneId: "WH1-STOR-AMB",
      siteCode: "WH1",
      areaCode: "STOR",
      zoneCode: "AMB",
      temperatureClass: "Ambient",
      hazmat: false,
      status: "Active",
      aisles: [
        {
          aisleId: "WH1-STOR-AMB-A07",
          zoneId: "WH1-STOR-AMB",
          aisleCode: "A07",
          sequenceHint: 7,
          direction: "TwoWay",
          status: "Active",
          slots: [
            {
              locationCode: "WH1-STOR-AMB-A07-01-01-A",
              zoneId: "WH1-STOR-AMB",
              aisleId: "WH1-STOR-AMB-A07",
              coordinates: {
                site: "WH1",
                area: "STOR",
                zone: "AMB",
                aisle: "A07",
                bay: "01",
                level: "01",
                position: "A",
              },
              locationType: "PalletRack",
              role: "Storage",
              capacity: { maxWeightKg: 1200, maxVolumeM3: 2.4 },
              status: "Active",
            },
          ],
        },
      ],
    },
  ],
};

function mockSiteAndLayout() {
  server.use(
    http.get(`${FACILITY_API_BASE}/sites`, () =>
      HttpResponse.json([{ siteCode: "WH1", name: "Fulfilment Centre One", status: "Active" }]),
    ),
    http.get(`${FACILITY_API_BASE}/sites/WH1/layout`, () => HttpResponse.json(LAYOUT)),
  );
}

describe("FacilityScreen", () => {
  it("renders both zones with no role filter applied", async () => {
    mockSiteAndLayout();
    render(<FacilityScreen />);

    await userEvent.click(await screen.findByText("WH1"));

    expect(await screen.findByText("WH1-DOCK-OB-D01-01-01-A")).toBeInTheDocument();
    expect(screen.getByText("WH1-STOR-AMB-A07-01-01-A")).toBeInTheDocument();
  });

  it("filters the tree down to only the selected role, hiding non-matching zones", async () => {
    mockSiteAndLayout();
    render(<FacilityScreen />);

    await userEvent.click(await screen.findByText("WH1"));
    await screen.findByText("WH1-DOCK-OB-D01-01-01-A");

    await userEvent.click(screen.getByRole("button", { name: /Dock/ }));

    expect(screen.getByText("WH1-DOCK-OB-D01-01-01-A")).toBeInTheDocument();
    expect(screen.queryByText("WH1-STOR-AMB-A07-01-01-A")).not.toBeInTheDocument();
  });

  it("shows an empty-state message when no location matches the active filter", async () => {
    mockSiteAndLayout();
    render(<FacilityScreen />);

    await userEvent.click(await screen.findByText("WH1"));
    await screen.findByText("WH1-DOCK-OB-D01-01-01-A");

    await userEvent.click(screen.getByRole("button", { name: /Yard/ }));

    expect(screen.getByText("No Yard locations at this site.")).toBeInTheDocument();
    expect(screen.queryByText("WH1-DOCK-OB-D01-01-01-A")).not.toBeInTheDocument();
  });

  it("clearing the filter (All roles) restores every zone", async () => {
    mockSiteAndLayout();
    render(<FacilityScreen />);

    await userEvent.click(await screen.findByText("WH1"));
    await screen.findByText("WH1-DOCK-OB-D01-01-01-A");

    await userEvent.click(screen.getByRole("button", { name: /Dock/ }));
    expect(screen.queryByText("WH1-STOR-AMB-A07-01-01-A")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "All roles" }));
    expect(screen.getByText("WH1-STOR-AMB-A07-01-01-A")).toBeInTheDocument();
  });
});
