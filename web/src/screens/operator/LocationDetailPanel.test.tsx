import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse, server } from "../../test/mocks/server";
import { FACILITY_API_BASE } from "../../config";
import { LocationDetailPanel } from "./LocationDetailPanel";

const CODE = "WH1-STOR-AMB-A07-03-02-B";

const SLOT = {
  locationCode: CODE,
  zoneId: "WH1-STOR-AMB",
  aisleId: "WH1-STOR-AMB-A07",
  coordinates: {
    site: "WH1",
    area: "STOR",
    zone: "AMB",
    aisle: "A07",
    bay: "03",
    level: "02",
    position: "B",
  },
  locationType: "PalletRack",
  role: "Storage",
  capacity: { maxWeightKg: 1200, maxVolumeM3: 2.4 },
  status: "Active",
  position: { xM: 12.5, yM: 7.25, zM: 0 },
  dimensions: { widthM: 1.2, depthM: 0.9, heightM: 2.4 },
};

async function lookUp(code = CODE) {
  await userEvent.type(screen.getByLabelText("Location code *"), code);
  await userEvent.click(screen.getByRole("button", { name: "Look up" }));
}

/** The detail card's unique "loaded" marker: the slot's location type. */
async function loaded() {
  await screen.findByText("PalletRack");
}

describe("LocationDetailPanel", () => {
  it("shows the slot's classification, zone/aisle and geometry", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/${CODE}`, () => HttpResponse.json(SLOT)),
    );

    render(<LocationDetailPanel />);
    await lookUp();
    await loaded();

    expect(screen.getByText("PalletRack")).toBeInTheDocument();
    expect(screen.getByText("Storage")).toBeInTheDocument();
    expect(screen.getByText("WH1-STOR-AMB-A07")).toBeInTheDocument();
    expect(screen.getByText("12.5 / 7.25 / 0")).toBeInTheDocument();
    expect(screen.getByText("1.2 × 0.9 × 2.4")).toBeInTheDocument();
  });

  it("renders the RFC 7807 detail of a 404 for an unknown code", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/WH1-NOPE-NOPE-A07-03-02-B`, () =>
        HttpResponse.json(
          {
            type: "https://errors.facility-layout.warehouse-systems.dev/location-not-found",
            title: "Location not found",
            status: 404,
            detail: "no location slot with code \"WH1-NOPE-NOPE-A07-03-02-B\"",
          },
          { status: 404 },
        ),
      ),
    );

    render(<LocationDetailPanel />);
    await lookUp("WH1-NOPE-NOPE-A07-03-02-B");

    expect(
      await screen.findByText('no location slot with code "WH1-NOPE-NOPE-A07-03-02-B"'),
    ).toBeInTheDocument();
  });

  it("decommissions behind a confirm and reports the one-way result", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/${CODE}`, () => HttpResponse.json(SLOT)),
      http.post(`${FACILITY_API_BASE}/locations/${CODE}/decommission`, ({ request }) => {
        // The remote's convention: every POST carries a per-submit
        // Idempotency-Key (ADR-0019).
        const key = request.headers.get("Idempotency-Key");
        expect(key && key.length > 0).toBe(true);
        return new HttpResponse(null, { status: 204 });
      }),
    );

    render(<LocationDetailPanel />);
    await lookUp();
    await loaded();

    // First click only arms the confirmation; nothing is sent yet.
    await userEvent.click(screen.getByRole("button", { name: "Decommission…" }));
    expect(screen.getByText(/permanently retires the slot/i)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Confirm decommission" }));

    await waitFor(() =>
      expect(screen.getByText(`Location ${CODE} decommissioned. The transition is one-way.`)).toBeInTheDocument(),
    );
    // The on-screen status flips without a re-fetch.
    expect(screen.getByText("Decommissioned")).toBeInTheDocument();
  });

  it("keeps the confirmation armed away on cancel", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/${CODE}`, () => HttpResponse.json(SLOT)),
    );

    render(<LocationDetailPanel />);
    await lookUp();
    await loaded();

    await userEvent.click(screen.getByRole("button", { name: "Decommission…" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.queryByText(/permanently retires the slot/i)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Decommission…" })).toBeInTheDocument();
  });

  it("renders the 409 problem detail when decommissioning twice", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/${CODE}`, () => HttpResponse.json(SLOT)),
      http.post(`${FACILITY_API_BASE}/locations/${CODE}/decommission`, () =>
        HttpResponse.json(
          {
            type: "https://errors.facility-layout.warehouse-systems.dev/slot-already-decommissioned",
            title: "Location slot already decommissioned",
            status: 409,
            detail: "location slot is already decommissioned",
          },
          { status: 409 },
        ),
      ),
    );

    render(<LocationDetailPanel />);
    await lookUp();
    await loaded();

    await userEvent.click(screen.getByRole("button", { name: "Decommission…" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm decommission" }));

    expect(await screen.findByText("location slot is already decommissioned")).toBeInTheDocument();
  });
});
