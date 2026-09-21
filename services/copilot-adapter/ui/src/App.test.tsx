import { StrictMode } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Model, Repository, Session, Worktree } from "./api/client";
import { App } from "./App";

const repository: Repository = {
  id: "candace",
  displayName: "Candace",
  root: "/srv/candace",
  defaultRef: "main",
};
const worktree: Worktree = {
  id: "22222222-2222-4222-8222-222222222222",
  repositoryId: repository.id,
  repositoryRoot: repository.root,
  path: "/srv/worktrees/review",
  branch: "review",
  headSha: "abc123",
  baseRef: "main",
  managed: true,
  clean: true,
  state: "active",
  sessionCount: 1,
  createdAt: "2026-09-05T00:00:00Z",
  updatedAt: "2026-09-05T00:00:00Z",
};
const model: Model = { id: "gpt-5.6", displayName: "GPT-5.6", capabilities: ["chat", "tools"] };

function session(displayName: string): Session {
  return {
    id: displayName === "Current task"
      ? "11111111-1111-4111-8111-111111111111"
      : "33333333-3333-4333-8333-333333333333",
    displayName,
    model: model.id,
    worktreeId: worktree.id,
    workingDirectory: worktree.path,
    status: "idle",
    createdAt: "2026-09-05T00:00:00Z",
    updatedAt: "2026-09-05T00:00:00Z",
    turnCount: 0,
  };
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });
}

afterEach(() => {
  window.location.hash = "#/";
  vi.unstubAllGlobals();
});

describe("App resource reloads", () => {
  it("refreshes an Auto-only catalog into real choices without retaining an unavailable selection", async () => {
    const auto: Model = { id: "auto", displayName: "Auto", capabilities: [] };
    const second: Model = { id: "second-model", displayName: "Second model", capabilities: ["chat"] };
    let catalog = [auto];
    vi.stubGlobal("fetch", async (request: Request) => {
      const path = new URL(request.url).pathname;
      if (path === "/api/workbench/theme/get") return Promise.resolve(json({ theme: {} }));
      if (path === "/v1/sessions") return json({ data: [] });
      if (path === "/v1/repositories") return json({ data: [repository] });
      if (path === "/v1/worktrees") return json({ data: [worktree] });
      if (path === "/v1/models") return json({ data: catalog });
      throw new Error(`unexpected request ${path}`);
    });
    await act(async () => { render(<App />); });
    fireEvent.click(screen.getAllByRole("button", { name: /New task/ })[0]);
    const picker = screen.getByLabelText("Model") as HTMLSelectElement;
    expect(picker.value).toBe("auto");
    expect(screen.getByText("1 model available")).toBeTruthy();

    catalog = [model, second];
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Refresh models" })); });
    expect(screen.getByRole("option", { name: "GPT-5.6 (gpt-5.6)" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "Second model (second-model)" })).toBeTruthy();
    expect(screen.queryByRole("option", { name: /Auto/ })).toBeNull();
    expect(picker.value).toBe("");
    expect((screen.getByRole("button", { name: "Start task" }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.change(picker, { target: { value: second.id } });
    catalog = [second, model];
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Refresh models" })); });
    expect(picker.value).toBe(second.id);
    expect((screen.getByRole("button", { name: "Start task" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("discards an older snapshot when a newer reload is queued", async () => {
    const firstResponses = new Map<string, (response: Response) => void>();
    const counts = new Map<string, number>();
    vi.stubGlobal("fetch", (request: Request) => {
      const path = new URL(request.url).pathname;
      if (path === "/api/workbench/theme/get") return Promise.resolve(json({ theme: {} }));
      const count = (counts.get(path) ?? 0) + 1;
      counts.set(path, count);
      if (count === 1) {
        return new Promise<Response>((resolve) => firstResponses.set(path, resolve));
      }
      if (path === "/v1/sessions") return Promise.resolve(json({ data: [session("Current task")] }));
      if (path === "/v1/repositories") return Promise.resolve(json({ data: [repository] }));
      if (path === "/v1/worktrees") return Promise.resolve(json({ data: [worktree] }));
      if (path === "/v1/models") return Promise.resolve(json({ data: [model] }));
      throw new Error(`unexpected request ${path}`);
    });

    render(<StrictMode><App /></StrictMode>);
    expect(screen.getByText("Loading worktrees…")).toBeTruthy();
    expect(screen.queryByText("Worktrees · 0")).toBeNull();
    await vi.waitFor(() => expect(firstResponses.size).toBe(4));
    await act(async () => {
      firstResponses.get("/v1/sessions")?.(json({ data: [session("Stale task")] }));
      firstResponses.get("/v1/repositories")?.(json({ data: [repository] }));
      firstResponses.get("/v1/worktrees")?.(json({ data: [worktree] }));
      firstResponses.get("/v1/models")?.(json({ data: [model] }));
    });

    expect(await screen.findByRole("link", { name: "Continue Current task" })).toBeTruthy();
    expect(screen.queryByText("Stale task")).toBeNull();
    expect(screen.getByText("Worktrees · 1")).toBeTruthy();
    expect(screen.queryByText("Loading worktrees…")).toBeNull();
    expect(counts.get("/v1/models")).toBe(1);
    expect([counts.get("/v1/sessions"), counts.get("/v1/repositories"), counts.get("/v1/worktrees")]).toEqual([2, 2, 2]);
  });

  it("keeps tasks available through a catalog failure and lets the user refresh models", async () => {
    let modelCalls = 0;
    vi.stubGlobal("fetch", async (request: Request) => {
      const path = new URL(request.url).pathname;
      if (path === "/api/workbench/theme/get") return Promise.resolve(json({ theme: {} }));
      if (path === "/v1/sessions") return json({ data: [session("Current task")] });
      if (path === "/v1/repositories") return json({ data: [repository] });
      if (path === "/v1/worktrees") return json({ data: [worktree] });
      if (path === "/v1/models") {
        modelCalls += 1;
        return modelCalls === 1
          ? json({ code: "models_unavailable", message: "model registry unavailable" }, 503)
          : json({ data: [model] });
      }
      throw new Error(`unexpected request ${path}`);
    });

    render(<App />);
    expect(await screen.findByRole("link", { name: "Continue Current task" })).toBeTruthy();
    fireEvent.click(screen.getAllByRole("button", { name: /New task/ })[0]);
    expect((await screen.findByRole("alert")).textContent).toContain(
      "models_unavailable: model registry unavailable",
    );
    expect((screen.getByLabelText("Model") as HTMLSelectElement).disabled).toBe(true);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Refresh models" })); });
    await vi.waitFor(() => expect((screen.getByLabelText("Model") as HTMLSelectElement).value).toBe(model.id));
    expect((screen.getByLabelText("Model") as HTMLSelectElement).disabled).toBe(false);
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
