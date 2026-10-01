import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Worktree } from "../api/client";
import { renderWithMantine as render } from "../test-utils";
import { WorktreePane } from "./WorktreePane";

const worktreeId = "22222222-2222-4222-8222-222222222222";

function worktree(branch: string, updatedAt = "2026-09-06T00:00:00Z"): Worktree {
  return {
    id: worktreeId,
    repositoryId: "candace",
    repositoryRoot: "/srv/candace",
    path: `/srv/worktrees/${branch}`,
    branch,
    headSha: `${branch}-head`,
    baseRef: "main",
    managed: true,
    clean: true,
    state: "active",
    sessionCount: 1,
    createdAt: "2026-09-05T00:00:00Z",
    updatedAt,
  };
}

afterEach(() => vi.unstubAllGlobals());

describe("WorktreePane", () => {
  it("surfaces a rejected read and lets the operator retry", async () => {
    let attempts = 0;
    vi.stubGlobal("fetch", vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new Error("worktree network unavailable");
      return new Response(JSON.stringify(worktree("recovered")), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }));

    render(<WorktreePane worktreeId={worktreeId} revision={0} />);
    expect((await screen.findByRole("alert")).textContent).toContain("worktree network unavailable");
    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));

    await screen.findByText("recovered");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(attempts).toBe(2);
  });

  it("keeps the newest revision when reads finish out of order", async () => {
    const pending: Array<(response: Response) => void> = [];
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => pending.push(resolve))));
    const { rerender } = render(<WorktreePane worktreeId={worktreeId} revision={1} />);
    await vi.waitFor(() => expect(pending).toHaveLength(1));

    rerender(<WorktreePane worktreeId={worktreeId} revision={2} />);
    await vi.waitFor(() => expect(pending).toHaveLength(2));
    await act(async () => pending[1]?.(new Response(JSON.stringify(worktree("newer", "2026-09-06T00:00:02Z")), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));
    await screen.findByText("newer");

    await act(async () => pending[0]?.(new Response(JSON.stringify(worktree("older", "2026-09-06T00:00:01Z")), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));
    expect(screen.queryByText("older")).toBeNull();
    expect(screen.getByText("newer")).toBeTruthy();
  });
});
