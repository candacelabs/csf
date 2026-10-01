import { createRef } from "react";
import { fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SimulationPage } from "./SimulationPage";
import { ReleasePage } from "./ReleasePage";
import { renderWithMantine as render } from "./test-utils";

const run = { runId: "camera-run", simulator: "isaac", executor: "local", state: "SIMULATION_STATE_SUCCEEDED", steps: 20, completedSteps: 20 };
const shell = { menuButtonRef: createRef<HTMLButtonElement>(), onMenu: vi.fn() };
afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); });

describe("simulation inspection consumer", () => {
  it("uses inspect artifacts and protobuf zero defaults, and loads bounded logs only on demand", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      const path = new URL(request.url).pathname;
      calls.push(path);
      const body = path.endsWith("/list") ? { runs: [run] } : path.endsWith("/inspect") ? { run: { ...run, traceUrl: "https://traces.example.invalid/recorded", artifacts: [{ path: "camera.png", url: "/retained/camera.png", mediaType: "image/png" }], latestMeasurements: [{ metric: "y" }] } } : { content: "simulator output", truncated: true };
      if (path.endsWith("/logs")) expect(await request.json()).toEqual({ runId: "camera-run", maxBytes: 65_536 });
      return Response.json(body);
    }));
    render(<SimulationPage {...shell} runId="camera-run" />);
    expect((await screen.findByRole("img")).getAttribute("src")).toBe("/retained/camera.png");
    expect(screen.getByRole("link", { name: /Open Langfuse trace/ }).getAttribute("href")).toBe("https://traces.example.invalid/recorded");
    expect(screen.getByRole("row", { name: "y 0 0" })).toBeTruthy();
    expect(screen.getByText("0 bytes")).toBeTruthy();
    expect(calls).not.toContain("/api/simulation/logs");
    fireEvent.click(screen.getByRole("button", { name: "Read logs" }));
    expect(await screen.findByDisplayValue("simulator output")).toBeTruthy();
    expect(screen.getByText(/Showing a bounded excerpt/)).toBeTruthy();
  });
  it("shows unavailable data as an error, not an empty successful run list", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ message: "offline" }, { status: 503 })));
    render(<SimulationPage {...shell} runId={null} />);
    expect((await screen.findByRole("alert")).textContent).toContain("offline");
    expect(screen.queryByText("No simulation runs have been recorded.")).toBeNull();
  });
  it("links to the configured consumer guide without embedding another installation's receipts", () => {
    vi.stubEnv("VITE_CSF_RELEASE_URL", "/releases/current");
    render(<ReleasePage {...shell} />);
    expect(screen.getByRole("link", { name: /Configured consumer guide/ }).getAttribute("href")).toBe(new URL("/releases/current", window.location.href).toString());
    expect(screen.queryByRole("link", { name: "Download source" })).toBeNull();
  });
});
