import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorktreeChanges } from "../api/client";
import { renderWithMantine as render } from "../test-utils";
import { ChangesPane } from "./ChangesPane";

const worktreeId = "22222222-2222-4222-8222-222222222222";

function snapshot(headSha: string, patch: string): WorktreeChanges {
  return {
    worktreeId,
    headSha,
    clean: false,
    files: [{ path: "README.md", indexState: "unmodified", worktreeState: "modified" }],
    patch,
    truncated: false,
    capturedAt: "2026-09-06T00:00:00Z",
  };
}

afterEach(() => vi.unstubAllGlobals());

describe("ChangesPane", () => {
  it("surfaces a rejected read and retries without leaving the pane loading", async () => {
    let attempts = 0;
    vi.stubGlobal("fetch", vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new Error("changes network unavailable");
      return new Response(JSON.stringify(snapshot("new-head", "new patch")), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }));

    render(<ChangesPane worktreeId={worktreeId} revision={0} />);
    expect((await screen.findByRole("alert")).textContent).toContain("changes network unavailable");
    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));

    await screen.findByText("new patch");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(attempts).toBe(2);
  });

  it("does not let an older revision response replace a newer snapshot", async () => {
    const pending: Array<(response: Response) => void> = [];
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => pending.push(resolve))));
    const { rerender } = render(<ChangesPane worktreeId={worktreeId} revision={1} />);
    await vi.waitFor(() => expect(pending).toHaveLength(1));

    rerender(<ChangesPane worktreeId={worktreeId} revision={2} />);
    await vi.waitFor(() => expect(pending).toHaveLength(2));
    await act(async () => pending[1]?.(new Response(JSON.stringify(snapshot("new-head", "new patch")), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));
    await screen.findByText("new patch");

    await act(async () => pending[0]?.(new Response(JSON.stringify(snapshot("old-head", "old patch")), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));
    expect(screen.queryByText("old patch")).toBeNull();
    expect(screen.getByText("new patch")).toBeTruthy();
  });

  it("selects an exact unquoted space path when another path has the same prefix", async () => {
    const patch = [
      "diff --git a/src/main file.ts.backup b/src/main file.ts.backup",
      "--- a/src/main file.ts.backup",
      "+++ b/src/main file.ts.backup",
      "+prefix collision",
      "diff --git a/src/main file.ts b/src/main file.ts",
      "--- a/src/main file.ts",
      "+++ b/src/main file.ts",
      "+exact path",
    ].join("\n");
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      ...snapshot("new-head", patch),
      files: [
        { path: "src/main file.ts.backup", indexState: "unmodified", worktreeState: "modified" },
        { path: "src/main file.ts", indexState: "unmodified", worktreeState: "modified" },
      ],
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));

    render(<ChangesPane worktreeId={worktreeId} revision={0} />);
    fireEvent.click(await screen.findByTitle("src/main file.ts"));

    await vi.waitFor(() => {
      const renderedPatch = screen.getByLabelText("Git patch").textContent ?? "";
      expect(renderedPatch).toContain("+exact path");
      expect(renderedPatch).not.toContain("+prefix collision");
    });
  });

  it("does not confuse an embedded b marker or rename source with another current path", async () => {
    const patch = [
      "diff --git a/a b/x b/y b/a b/x b/y",
      "--- a/a b/x b/y",
      "+++ b/a b/x b/y",
      "+embedded marker path",
      "diff --git a/y b/renamed y",
      "similarity index 100%",
      "rename from y",
      "rename to renamed y",
      "+rename section",
      "diff --git a/y b/y",
      "--- a/y",
      "+++ b/y",
      "+actual y path",
    ].join("\n");
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      ...snapshot("new-head", patch),
      files: [
        { path: "a b/x b/y", indexState: "unmodified", worktreeState: "modified" },
        { path: "renamed y", indexState: "renamed", worktreeState: "unmodified" },
        { path: "y", indexState: "unmodified", worktreeState: "modified" },
      ],
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));

    render(<ChangesPane worktreeId={worktreeId} revision={0} />);
    fireEvent.click(await screen.findByTitle("y"));

    await vi.waitFor(() => {
      const renderedPatch = screen.getByLabelText("Git patch").textContent ?? "";
      expect(renderedPatch).toContain("+actual y path");
      expect(renderedPatch).not.toContain("+embedded marker path");
      expect(renderedPatch).not.toContain("+rename section");
    });
  });

  it("parses independently quoted rename destinations in both directions", async () => {
    const tabPath = "tab\tname.txt";
    const plainPath = "plain target.txt";
    const patch = [
      "diff --git a/simple.txt \"b/tab\\tname.txt\"",
      "similarity index 90%",
      "rename from simple.txt",
      "rename to \"tab\\tname.txt\"",
      "+quoted destination",
      "diff --git \"a/tab\\tto.txt\" b/plain target.txt",
      "similarity index 90%",
      "rename from \"tab\\tto.txt\"",
      "rename to plain target.txt",
      "+plain destination",
    ].join("\n");
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      ...snapshot("new-head", patch),
      files: [
        { path: tabPath, indexState: "renamed", worktreeState: "unmodified" },
        { path: plainPath, indexState: "renamed", worktreeState: "unmodified" },
      ],
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));

    render(<ChangesPane worktreeId={worktreeId} revision={0} />);
    fireEvent.click(await screen.findByTitle(tabPath, { normalizer: (value) => value }));
    await vi.waitFor(() => {
      const renderedPatch = screen.getByLabelText("Git patch").textContent ?? "";
      expect(renderedPatch).toContain("+quoted destination");
      expect(renderedPatch).not.toContain("+plain destination");
    });
    fireEvent.click(screen.getByTitle(plainPath));
    await vi.waitFor(() => {
      const renderedPatch = screen.getByLabelText("Git patch").textContent ?? "";
      expect(renderedPatch).toContain("+plain destination");
      expect(renderedPatch).not.toContain("+quoted destination");
    });
  });

  it("selects a quoted diff header with C-escaped whitespace and non-ASCII bytes", async () => {
    const selectedPath = "notes/café\tplan.md";
    const patch = [
      "diff --git a/README.md b/README.md",
      "--- a/README.md",
      "+++ b/README.md",
      "+unrelated change",
      "diff --git \"a/notes/caf\\303\\251\\tplan.md\" \"b/notes/caf\\303\\251\\tplan.md\"",
      "--- \"a/notes/caf\\303\\251\\tplan.md\"",
      "+++ \"b/notes/caf\\303\\251\\tplan.md\"",
      "+escaped path",
    ].join("\n");
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      ...snapshot("new-head", patch),
      files: [
        { path: "README.md", indexState: "unmodified", worktreeState: "modified" },
        { path: selectedPath, indexState: "unmodified", worktreeState: "modified" },
      ],
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));

    render(<ChangesPane worktreeId={worktreeId} revision={0} />);
    fireEvent.click(await screen.findByTitle(selectedPath, { normalizer: (value) => value }));

    await vi.waitFor(() => {
      const renderedPatch = screen.getByLabelText("Git patch").textContent ?? "";
      expect(renderedPatch).toContain("+escaped path");
      expect(renderedPatch).not.toContain("+unrelated change");
    });
  });
});
