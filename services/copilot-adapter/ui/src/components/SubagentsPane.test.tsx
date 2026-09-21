import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Subagent, SubagentActivity } from "../api/client";
import { renderWithMantine as render } from "../test-utils";
import { SubagentsPane } from "./SubagentsPane";

const sessionId = "11111111-1111-4111-8111-111111111111";
const agent: Subagent = {
  id: "component-export",
  sessionId,
  displayName: "Component export audit",
  status: "active",
  summary: "Auditing the export pipeline",
  activityCount: 2,
  startedAt: "2026-09-05T00:00:00Z",
  updatedAt: "2026-09-05T00:00:10Z",
};
const activity: SubagentActivity = {
  seq: 1,
  sessionId,
  subagentId: agent.id,
  kind: "progress",
  occurredAt: "2026-09-05T00:00:01Z",
  text: "Checking the next-version export behavior.",
};

afterEach(() => vi.unstubAllGlobals());

describe("SubagentsPane", () => {
  it("surfaces a rejected list read and permits a retry", async () => {
    let attempts = 0;
    vi.stubGlobal("fetch", vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new Error("subagent list unavailable");
      return new Response(JSON.stringify({ data: [] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }));

    render(<SubagentsPane sessionId={sessionId} subagents={[]} liveActivity={{}} onLoaded={() => undefined} />);
    expect((await screen.findByRole("alert")).textContent).toContain("subagent list unavailable");
    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));

    await screen.findByText("No subagents yet");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(attempts).toBe(2);
  });

  it("renders an API detail failure inside the selected subagent view", async () => {
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (request.url.endsWith("/subagents")) {
        return new Response(JSON.stringify({ data: [agent] }), { status: 200, headers: { "content-type": "application/json" } });
      }
      return new Response(JSON.stringify({ code: "subagent_missing", message: "detail unavailable" }), {
        status: 404,
        headers: { "content-type": "application/json" },
      });
    }));

    render(<SubagentsPane sessionId={sessionId} subagents={[agent]} liveActivity={{}} onLoaded={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: /Component export audit/ }));

    expect((await screen.findByRole("alert")).textContent).toContain("subagent_missing: detail unavailable");
    expect(screen.getByRole("button", { name: "Retry subagent details" })).toBeTruthy();
    expect(screen.queryByText("No activity has been reported yet.")).toBeNull();
  });

  it("surfaces an activity transport failure and retries the detail snapshot", async () => {
    let activityAttempts = 0;
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (request.url.endsWith("/subagents")) {
        return new Response(JSON.stringify({ data: [agent] }), { status: 200, headers: { "content-type": "application/json" } });
      }
      if (request.url.endsWith(`/${agent.id}`)) {
        return new Response(JSON.stringify(agent), { status: 200, headers: { "content-type": "application/json" } });
      }
      activityAttempts += 1;
      if (activityAttempts === 1) throw new Error("activity network unavailable");
      return new Response(JSON.stringify({ data: [activity] }), { status: 200, headers: { "content-type": "application/json" } });
    }));

    render(<SubagentsPane sessionId={sessionId} subagents={[agent]} liveActivity={{}} onLoaded={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: /Component export audit/ }));
    expect((await screen.findByRole("alert")).textContent).toContain("activity network unavailable");
    fireEvent.click(screen.getByRole("button", { name: "Retry subagent details" }));

    await screen.findByText("Checking the next-version export behavior.");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(activityAttempts).toBe(2);
  });

  it("does not replace a newer live subagent with an older list snapshot", async () => {
    let finishList: ((response: Response) => void) | undefined;
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => { finishList = resolve; })));
    const onLoaded = vi.fn();
    const newer = { ...agent, status: "completed" as const, updatedAt: "2026-09-05T00:00:10.1Z", completedAt: "2026-09-05T00:00:10.1Z" };
    const { rerender } = render(<SubagentsPane sessionId={sessionId} subagents={[agent]} liveActivity={{}} onLoaded={onLoaded} />);
    await vi.waitFor(() => expect(finishList).toBeDefined());

    rerender(<SubagentsPane sessionId={sessionId} subagents={[newer]} liveActivity={{}} onLoaded={onLoaded} />);
    await act(async () => finishList?.(new Response(JSON.stringify({ data: [agent] }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));

    await vi.waitFor(() => expect(onLoaded).toHaveBeenCalledWith([newer]));
  });

  it("prefers fractional-second detail that is newer than a whole-second live row", async () => {
    const detail = {
      ...agent,
      displayName: "Fractional detail",
      status: "completed" as const,
      updatedAt: "2026-09-05T00:00:10.000000002Z",
      completedAt: "2026-09-05T00:00:10.000000002Z",
    };
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      const body = request.url.endsWith("/subagents")
        ? { data: [agent] }
        : request.url.endsWith(`/${agent.id}`)
          ? detail
          : { data: [] };
      return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
    }));
    render(<SubagentsPane sessionId={sessionId} subagents={[agent]} liveActivity={{}} onLoaded={() => undefined} />);

    fireEvent.click(screen.getByRole("button", { name: /Component export audit/ }));

    await screen.findByText("Fractional detail");
    expect(screen.getAllByText("completed")).toHaveLength(2);
  });

  it("opens a real activity timeline from the agent row", async () => {
    vi.stubGlobal("fetch", async (request: Request) => {
      const body = request.url.endsWith("/subagents")
        ? { data: [agent] }
        : request.url.endsWith(`/${agent.id}`)
          ? agent
          : { data: [activity] };
      return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
    });
    render(<SubagentsPane sessionId={sessionId} subagents={[agent]} liveActivity={{}} onLoaded={() => undefined} />);

    expect(screen.getByText("Active · 1")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /Component export audit/ }));
    await screen.findByText("Checking the next-version export behavior.");
    expect(screen.getByText(/Working for/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Back to subagents" })).toBeTruthy();
  });

  it("describes a failed subagent duration without a duplicate preposition", async () => {
    const failed = {
      ...agent,
      status: "failed" as const,
      completedAt: "2026-09-05T00:00:10Z",
      updatedAt: "2026-09-05T00:00:10Z",
    };
    vi.stubGlobal("fetch", async (request: Request) => {
      const body = request.url.endsWith("/subagents")
        ? { data: [failed] }
        : request.url.endsWith(`/${failed.id}`)
          ? failed
          : { data: [] };
      return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
    });
    render(<SubagentsPane sessionId={sessionId} subagents={[failed]} liveActivity={{}} onLoaded={() => undefined} />);

    fireEvent.click(screen.getByRole("button", { name: /Component export audit/ }));

    expect(await screen.findByText("Failed after 10s")).toBeTruthy();
    expect(screen.queryByText("Failed after for 10s")).toBeNull();
  });
});
