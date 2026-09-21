import { createRef } from "react";
import type { ComponentProps } from "react";
import { fireEvent, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Session } from "./api/client";
import { HomePage } from "./HomePage";
import { renderWithMantine as render } from "./test-utils";

const session: Session = {
  id: "11111111-1111-4111-8111-111111111111", displayName: "Inspect the simulator",
  worktreeId: "22222222-2222-4222-8222-222222222222", model: "model-a",
  workingDirectory: "/srv/worktrees/simulator", status: "running", turnCount: 1,
  createdAt: "2026-09-17T00:00:00Z", updatedAt: "2026-09-17T01:00:00Z",
};

function props(): ComponentProps<typeof HomePage> {
  return {
    sessions: [session, { ...session, id: "33333333-3333-4333-8333-333333333333", displayName: "Review release", status: "idle" }],
    worktrees: [], models: [], loading: false, refreshing: false, error: null,
    modelsLoading: false, modelsError: null, observedAt: new Date("2026-09-17T01:30:00Z"),
    menuButtonRef: createRef<HTMLButtonElement>(), onMenu: vi.fn(), onNewSession: vi.fn(), onRefresh: vi.fn(),
  };
}

afterEach(() => vi.unstubAllEnvs());

describe("Workbench home", () => {
  it("opens the correct session and filters tasks by name and generated status", () => {
    render(<HomePage {...props()} />);
    expect(screen.getByRole("link", { name: "Continue Inspect the simulator" }).getAttribute("href")).toBe(`#/sessions/${session.id}`);
    expect(screen.getByText("Working now").parentElement?.textContent).toBe("Working now1");
    fireEvent.change(screen.getByRole("textbox", { name: "Find a task" }), { target: { value: "release" } });
    expect(screen.queryByRole("link", { name: "Continue Inspect the simulator" })).toBeNull();
    expect(screen.getByRole("link", { name: "Continue Review release" })).toBeTruthy();
    fireEvent.change(screen.getByRole("textbox", { name: "Find a task" }), { target: { value: "" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Filter tasks by status" }), { target: { value: "running" } });
    expect(screen.queryByRole("link", { name: "Continue Review release" })).toBeNull();
  });

  it("distinguishes loading and failed counts from a measured zero and retains old tasks", () => {
    const input = props();
    const { rerender } = render(<HomePage {...input} loading sessions={[]} modelsLoading />);
    expect(screen.getByLabelText("Loading tasks")).toBeTruthy();
    expect(screen.getByText("Tasks").parentElement?.textContent).toBe("Tasks…");
    rerender(<HomePage {...input} error="disconnected" modelsError="catalog offline" />);
    expect(screen.getByText("Tasks").parentElement?.textContent).toBe("TasksUnavailable");
    expect(screen.getByText("Refresh failed · previous snapshot")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Continue Inspect the simulator" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Refresh overview" }));
    expect(input.onRefresh).toHaveBeenCalledOnce();
    rerender(<HomePage {...input} sessions={[]} />);
    expect(screen.getByText("Tasks").parentElement?.textContent).toBe("Tasks0");
    fireEvent.click(screen.getByRole("button", { name: /New task/ }));
    expect(input.onNewSession).toHaveBeenCalledOnce();
  });

  it("exposes only configured CSF destinations without inventing a release URL", () => {
    vi.stubEnv("VITE_CSF_DASHBOARD_URL", "https://csf.example.invalid/");
    vi.stubEnv("VITE_CSF_RELEASE_URL", undefined);
    const { rerender } = render(<HomePage {...props()} />);
    const explore = screen.getByRole("region", { name: "Around the workshop" });
    expect(within(explore).getByRole("link", { name: /Simulation runs/ }).getAttribute("href")).toBe("#/simulations");
    expect(within(explore).queryByRole("link", { name: /Release & evidence/ })).toBeNull();
    vi.stubEnv("VITE_CSF_RELEASE_URL", "/ui/release/readme.html");
    rerender(<HomePage {...props()} />);
    expect(within(explore).getByRole("link", { name: /Release & evidence/ }).getAttribute("href")).toBe("#/release");
    vi.stubEnv("VITE_CSF_DASHBOARD_URL", "");
    rerender(<HomePage {...props()} />);
    expect(screen.queryByRole("region", { name: "Around the workshop" })).toBeNull();
  });
});
