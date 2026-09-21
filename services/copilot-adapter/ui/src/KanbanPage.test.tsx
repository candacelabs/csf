import { createRef, StrictMode } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { afterEach, describe, expect, it, vi } from "vitest";
import { KanbanPage } from "./KanbanPage";
import { loadGotthRuntime } from "./kanbanIsland";

vi.mock("./kanbanIsland", async (original) => ({
  ...await original<typeof import("./kanbanIsland")>(), loadGotthRuntime: vi.fn(),
}));
const runtime = { start: vi.fn(), stop: vi.fn(), status: () => "live" };
const props = { menuButtonRef: createRef<HTMLButtonElement>(), onMenu: vi.fn(), onNewSession: vi.fn() };
const board = '<section data-gotth-region="board"><p id="server-card">Server card</p></section>';

afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });

describe("Kanban React island", () => {
  it("mounts one live runtime, leaves descendant ownership to gotth, and stops on unmount", async () => {
    vi.mocked(loadGotthRuntime).mockResolvedValue(runtime);
    const fetch = vi.fn(async () => new Response(board));
    vi.stubGlobal("fetch", fetch);
    const view = render(<StrictMode><MantineProvider><KanbanPage {...props} /></MantineProvider></StrictMode>);
    await screen.findByText("Server card");
    expect(runtime.start).toHaveBeenCalledTimes(1);
    const [url, host] = runtime.start.mock.calls[0];
    expect(url).toBe("/v1/kanban/live");
    expect(host.querySelector("#server-card")).toBeTruthy();
    host.querySelector("#server-card").textContent = "Updated by gotth";
    await act(async () => document.dispatchEvent(new CustomEvent("gotth-live:error", { detail: { message: "Checkpoint changed" } })));
    expect(screen.getByText("Updated by gotth")).toBeTruthy();
    expect(screen.getByRole("status").textContent).toBe("Checkpoint changed");
    view.unmount();
    expect(runtime.stop).toHaveBeenCalledTimes(1);
    expect(host.childNodes.length).toBe(0);
    expect(fetch.mock.calls.length).toBe(2); // StrictMode mount probe, no polling.
  });

  it("does not start a late response after route unmount", async () => {
    vi.mocked(loadGotthRuntime).mockResolvedValue(runtime);
    let resolve: (response: Response) => void = () => undefined;
    vi.stubGlobal("fetch", () => new Promise<Response>((done) => { resolve = done; }));
    const view = render(<MantineProvider><KanbanPage {...props} /></MantineProvider>);
    view.unmount();
    await act(async () => resolve(new Response(board)));
    expect(runtime.start).not.toHaveBeenCalled();
  });

  it("retries a failed initial view without opening an extra connection", async () => {
    vi.mocked(loadGotthRuntime).mockResolvedValue(runtime);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(new Response("unavailable", { status: 503 })).mockResolvedValueOnce(new Response(board)));
    render(<MantineProvider><KanbanPage {...props} /></MantineProvider>);
    await screen.findByText("The board could not be loaded (503).");
    expect(runtime.start).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Retry board" }));
    await waitFor(() => expect(runtime.start).toHaveBeenCalledTimes(1));
  });
});
