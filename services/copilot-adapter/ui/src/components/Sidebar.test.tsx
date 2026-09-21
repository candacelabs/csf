import { createRef } from "react";
import { fireEvent, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Repository, Session, Worktree } from "../api/client";
import { Sidebar } from "./Sidebar";
import { renderWithMantine as render } from "../test-utils";

const repository: Repository = { id: "candace", displayName: "Candace", root: "/srv/candace", defaultRef: "main" };
const worktree: Worktree = {
  id: "22222222-2222-4222-8222-222222222222", repositoryId: "candace", repositoryRoot: "/srv/candace", path: "/srv/worktrees/export", branch: "export-review", headSha: "abc", baseRef: "main", managed: true, clean: false, state: "active", sessionCount: 1, createdAt: "2026-09-05T00:00:00Z", updatedAt: "2026-09-05T00:00:00Z",
};
const session: Session = {
  id: "11111111-1111-4111-8111-111111111111", displayName: "Component export audit", model: "gpt-5.6", worktreeId: worktree.id, workingDirectory: worktree.path, status: "running", createdAt: "2026-09-05T00:00:00Z", updatedAt: "2026-09-05T00:00:00Z", turnCount: 1,
};

afterEach(() => vi.unstubAllEnvs());

describe("Sidebar", () => {
  it.each([undefined, "", "  "])("omits CSF navigation when no dashboard is configured (%s)", (dashboardURL) => {
    vi.stubEnv("VITE_CSF_DASHBOARD_URL", dashboardURL);
    render(<Sidebar open sessions={[]} worktrees={[]} repositories={[]} selectedSessionId={null} menuButtonRef={createRef<HTMLButtonElement>()} onClose={() => undefined} onNewSession={() => undefined} />);
    expect(screen.queryByRole("link", { name: "CSF dashboard" })).toBeNull();
  });

  it("links to the configured CSF dashboard and simulations while preserving Workbench navigation", () => {
    vi.stubEnv("VITE_CSF_DASHBOARD_URL", "/");
    render(<Sidebar open sessions={[]} worktrees={[]} repositories={[]} selectedSessionId={null} menuButtonRef={createRef<HTMLButtonElement>()} onClose={() => undefined} onNewSession={() => undefined} />);
    expect(screen.getByRole("link", { name: "CSF dashboard" }).getAttribute("href")).toBe("/");
    const simulations = screen.getByRole("link", { name: "Simulation runs" });
    expect(simulations.getAttribute("href")).toBe("#/simulations");
    expect(screen.getByRole("link", { name: "Candace workbench home" }).getAttribute("href")).toBe("#/");
    expect(screen.getByRole("button", { name: /New task/ })).toBeTruthy();
  });

  it("groups chats under their worktree and preserves selection", () => {
    const menuButtonRef = createRef<HTMLButtonElement>();
    render(<Sidebar open sessions={[session]} worktrees={[worktree]} repositories={[repository]} selectedSessionId={session.id} menuButtonRef={menuButtonRef} onClose={() => undefined} onNewSession={() => undefined} />);
    expect(screen.getByText("export-review")).toBeTruthy();
    const chat = screen.getByRole("link", { name: /Component export audit/ });
    expect(chat.className).toContain("selected");
    fireEvent.click(screen.getByRole("button", { name: /export-review/ }));
    expect(screen.queryByRole("link", { name: /Component export audit/ })).toBeNull();
  });

  it("moves focus into the mobile drawer and restores its menu opener", () => {
    const menuButtonRef = createRef<HTMLButtonElement>();
    const onClose = vi.fn();
    const sidebar = (open: boolean) => (
      <>
        <button ref={menuButtonRef} type="button">Open sidebar</button>
        <Sidebar open={open} sessions={[session]} worktrees={[worktree]} repositories={[repository]} selectedSessionId={session.id} menuButtonRef={menuButtonRef} onClose={onClose} onNewSession={() => undefined} />
      </>
    );
    const { rerender } = render(sidebar(false));
    menuButtonRef.current?.focus();

    rerender(sidebar(true));
    const sidebarRegion = screen.getByRole("complementary", { name: "Tasks and worktrees" });
    expect(document.activeElement).toBe(within(sidebarRegion).getByRole("button", { name: "Close sidebar" }));
    fireEvent.keyDown(sidebarRegion, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();

    rerender(sidebar(false));
    expect(document.activeElement).toBe(menuButtonRef.current);
  });
});
