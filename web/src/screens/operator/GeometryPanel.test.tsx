import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse, server } from "../../test/mocks/server";
import { FACILITY_API_BASE } from "../../config";
import { GeometryPanel } from "./GeometryPanel";

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

const AISLE = {
  aisleId: "WH1-STOR-AMB-A07",
  zoneId: "WH1-STOR-AMB",
  aisleCode: "A07",
  sequenceHint: 7,
  direction: "Ascending",
  status: "Active",
};

describe("GeometryPanel — location geometry", () => {
  it("loads the recorded geometry into the correction form", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/${CODE}`, () => HttpResponse.json(SLOT)),
    );

    render(<GeometryPanel />);
    await userEvent.type(screen.getAllByLabelText("Location code *")[0], CODE);
    await userEvent.click(screen.getByRole("button", { name: "Load current geometry" }));

    await waitFor(() => {
      expect((screen.getByLabelText("x (m) *") as HTMLInputElement).value).toBe("12.5");
    });
    expect((screen.getByLabelText("y (m) *") as HTMLInputElement).value).toBe("7.25");
    expect((screen.getByLabelText("width (m, > 0) *") as HTMLInputElement).value).toBe("1.2");
    expect((screen.getByLabelText("height (m, > 0) *") as HTMLInputElement).value).toBe("2.4");
  });

  it("PUTs the corrected snapshot and reports success", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/${CODE}`, () => HttpResponse.json(SLOT)),
      http.put(`${FACILITY_API_BASE}/locations/${CODE}/geometry`, async ({ request }) => {
        const body = (await request.json()) as {
          position: { xM: number; yM: number; zM: number };
          dimensions: { widthM: number; depthM: number; heightM: number };
          pickSequence?: number;
        };
        return HttpResponse.json({ ...SLOT, position: body.position, dimensions: body.dimensions });
      }),
    );

    render(<GeometryPanel />);
    await userEvent.type(screen.getAllByLabelText("Location code *")[0], CODE);
    await userEvent.click(screen.getByRole("button", { name: "Load current geometry" }));
    await waitFor(() => expect((screen.getByLabelText("x (m) *") as HTMLInputElement).value).toBe("12.5"));

    await userEvent.clear(screen.getByLabelText("x (m) *"));
    await userEvent.type(screen.getByLabelText("x (m) *"), "13");
    await userEvent.click(screen.getByRole("button", { name: "Save geometry" }));

    await waitFor(() => expect(screen.getByText(`Geometry saved for ${CODE}.`)).toBeInTheDocument());
  });

  it("rejects a negative z client-side with the spec's own message", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/${CODE}`, () => HttpResponse.json(SLOT)),
    );

    render(<GeometryPanel />);
    await userEvent.type(screen.getAllByLabelText("Location code *")[0], CODE);
    await userEvent.click(screen.getByRole("button", { name: "Load current geometry" }));
    await waitFor(() => expect((screen.getByLabelText("z (m, height ≥ 0) *") as HTMLInputElement).value).toBe("0"));

    await userEvent.clear(screen.getByLabelText("z (m, height ≥ 0) *"));
    await userEvent.type(screen.getByLabelText("z (m, height ≥ 0) *"), "-1");
    await userEvent.click(screen.getByRole("button", { name: "Save geometry" }));

    expect(await screen.findByText("z coordinate must not be negative")).toBeInTheDocument();
  });

  it("rejects a non-positive dimension client-side", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/${CODE}`, () => HttpResponse.json(SLOT)),
    );

    render(<GeometryPanel />);
    await userEvent.type(screen.getAllByLabelText("Location code *")[0], CODE);
    await userEvent.click(screen.getByRole("button", { name: "Load current geometry" }));
    await waitFor(() => expect((screen.getByLabelText("width (m, > 0) *") as HTMLInputElement).value).toBe("1.2"));

    await userEvent.clear(screen.getByLabelText("width (m, > 0) *"));
    await userEvent.type(screen.getByLabelText("width (m, > 0) *"), "0");
    await userEvent.click(screen.getByRole("button", { name: "Save geometry" }));

    expect(await screen.findByText("width, depth, and height must all be greater than zero")).toBeInTheDocument();
  });

  it("surfaces the server's problem detail verbatim when it still rejects", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/locations/${CODE}`, () => HttpResponse.json(SLOT)),
      http.put(`${FACILITY_API_BASE}/locations/${CODE}/geometry`, () =>
        HttpResponse.json(
          {
            type: "https://errors.facility-layout.warehouse-systems.dev/slot-decommissioned",
            title: "Location slot is decommissioned",
            status: 409,
            detail: "cannot set geometry on a decommissioned location slot",
          },
          { status: 409 },
        ),
      ),
    );

    render(<GeometryPanel />);
    await userEvent.type(screen.getAllByLabelText("Location code *")[0], CODE);
    await userEvent.click(screen.getByRole("button", { name: "Load current geometry" }));
    await waitFor(() => expect((screen.getByLabelText("x (m) *") as HTMLInputElement).value).toBe("12.5"));

    await userEvent.click(screen.getByRole("button", { name: "Save geometry" }));

    expect(await screen.findByText("cannot set geometry on a decommissioned location slot")).toBeInTheDocument();
  });
});

describe("GeometryPanel — aisle centreline", () => {
  it("loads an aisle with no centreline and PUTs a new one", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/zones/WH1-STOR-AMB/aisles/A07`, () =>
        HttpResponse.json(AISLE),
      ),
      http.put(`${FACILITY_API_BASE}/zones/WH1-STOR-AMB/aisles/A07/geometry`, async ({ request }) => {
        const body = (await request.json()) as {
          centreline: {
            start: { xM: number; yM: number; zM: number };
            end: { xM: number; yM: number; zM: number };
          };
        };
        return HttpResponse.json({
          ...AISLE,
          centreline: body.centreline,
        });
      }),
    );

    render(<GeometryPanel />);
    await userEvent.type(screen.getByLabelText("Zone id *"), "WH1-STOR-AMB");
    await userEvent.type(screen.getByLabelText("Aisle code *"), "A07");
    await userEvent.click(screen.getByRole("button", { name: "Load current centreline" }));

    await waitFor(() =>
      expect(screen.getByText(/no centreline recorded yet/)).toBeInTheDocument(),
    );

    await userEvent.type(screen.getByLabelText("start x (m) *"), "1");
    await userEvent.type(screen.getByLabelText("start y (m) *"), "2");
    await userEvent.type(screen.getByLabelText("start z (m, ≥ 0) *"), "0");
    await userEvent.type(screen.getByLabelText("end x (m) *"), "30");
    await userEvent.type(screen.getByLabelText("end y (m) *"), "2");
    await userEvent.type(screen.getByLabelText("end z (m, ≥ 0) *"), "0");
    await userEvent.click(screen.getByRole("button", { name: "Save centreline" }));

    await waitFor(() =>
      expect(screen.getByText("Centreline saved for WH1-STOR-AMB-A07.")).toBeInTheDocument(),
    );
  });

  it("rejects coincident centreline endpoints client-side", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/zones/WH1-STOR-AMB/aisles/A07`, () =>
        HttpResponse.json(AISLE),
      ),
    );

    render(<GeometryPanel />);
    await userEvent.type(screen.getByLabelText("Zone id *"), "WH1-STOR-AMB");
    await userEvent.type(screen.getByLabelText("Aisle code *"), "A07");
    await userEvent.click(screen.getByRole("button", { name: "Load current centreline" }));
    await waitFor(() => expect(screen.getByText(/no centreline recorded yet/)).toBeInTheDocument());

    await userEvent.type(screen.getByLabelText("start x (m) *"), "5");
    await userEvent.type(screen.getByLabelText("start y (m) *"), "5");
    await userEvent.type(screen.getByLabelText("start z (m, ≥ 0) *"), "0");
    await userEvent.type(screen.getByLabelText("end x (m) *"), "5");
    await userEvent.type(screen.getByLabelText("end y (m) *"), "5");
    await userEvent.type(screen.getByLabelText("end z (m, ≥ 0) *"), "0");
    await userEvent.click(screen.getByRole("button", { name: "Save centreline" }));

    expect(await screen.findByText("segment start and end must differ")).toBeInTheDocument();
  });

  it("surfaces the aisle 404 problem detail verbatim", async () => {
    server.use(
      http.get(`${FACILITY_API_BASE}/zones/WH1-STOR-AMB/aisles/A99`, () =>
        HttpResponse.json(
          {
            type: "https://errors.facility-layout.warehouse-systems.dev/aisle-not-found",
            title: "Aisle not found",
            status: 404,
            detail: 'no aisle "A99" in zone "WH1-STOR-AMB"',
          },
          { status: 404 },
        ),
      ),
    );

    render(<GeometryPanel />);
    await userEvent.type(screen.getByLabelText("Zone id *"), "WH1-STOR-AMB");
    await userEvent.type(screen.getByLabelText("Aisle code *"), "A99");
    await userEvent.click(screen.getByRole("button", { name: "Load current centreline" }));

    expect(await screen.findByText('no aisle "A99" in zone "WH1-STOR-AMB"')).toBeInTheDocument();
  });
});
