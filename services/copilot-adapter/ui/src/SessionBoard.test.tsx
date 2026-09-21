import { fireEvent, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { Session } from "./api/client";
import { SessionBoard } from "./SessionBoard";
import { renderWithMantine as render } from "./test-utils";

const session: Session = {
  id: "11111111-1111-4111-8111-111111111111", displayName: "Investigate a run",
  model: "test-model", status: "idle", turnCount: 1,
  worktreeId: "22222222-2222-4222-8222-222222222222", workingDirectory: "/srv/work",
  createdAt: "2026-09-17T00:00:00Z", updatedAt: "2026-09-17T01:00:00Z",
};

afterEach(() => localStorage.clear());

describe("Session Kanban consumer boundary", () => {
  it("uses observed runtime lanes, follows refreshes and links the full session identity", () => {
    const props = { worktrees: [], loading: false, error: null, kanban: true };
    const ended: Session = { ...session, id: "33333333-3333-4333-8333-333333333333", status: "ended", displayName: "Ended conversation" };
    const { rerender } = render(<SessionBoard {...props} sessions={[session, ended]} />);
    fireEvent.click(screen.getByRole("radio", { name: "Runtime status" }));
    const idle = screen.getByRole("region", { name: "Idle sessions" });
    expect(within(idle).getByRole("link", { name: `Session ${session.id}` }).getAttribute("href")).toBe(`#/sessions/${session.id}`);
    expect(within(screen.getByRole("region", { name: "Ended sessions" })).getByRole("article", { name: ended.displayName })).toBeTruthy();
    expect(screen.queryByRole("region", { name: /Done|Complete/ })).toBeNull();
    rerender(<SessionBoard {...props} sessions={[{ ...session, status: "running" }, ended]} />);
    expect(within(idle).queryByRole("article")).toBeNull();
    expect(within(screen.getByRole("region", { name: "Running sessions" })).getByRole("article", { name: session.displayName })).toBeTruthy();
    fireEvent.change(screen.getByRole("textbox", { name: "Find a task" }), { target: { value: ended.id } });
    expect(screen.queryByRole("article", { name: session.displayName })).toBeNull();
    expect(screen.getByRole("article", { name: ended.displayName })).toBeTruthy();
  });

  it("persists manual planning without changing observed runtime status", () => {
    const props = { sessions: [session], worktrees: [], loading: false, error: null, kanban: true };
    const { unmount } = render(<SessionBoard {...props} />);
    expect(screen.getByText(/Saved in this browser only/)).toBeTruthy();
    fireEvent.change(screen.getByRole("combobox", { name: `Move ${session.displayName}` }), { target: { value: "done" } });
    const done = screen.getByRole("region", { name: "Done planning" });
    expect(within(done).getByRole("article", { name: session.displayName })).toBeTruthy();
    expect(within(done).getByText("idle")).toBeTruthy();
    unmount();
    render(<SessionBoard {...props} />);
    expect(within(screen.getByRole("region", { name: "Done planning" })).getByRole("article")).toBeTruthy();
  });

  it("explains unconnected features on hover and activation without fabricating trace links", () => {
    render(<SessionBoard sessions={[session]} worktrees={[]} loading={false} error={null} kanban />);
    const trace = screen.getByRole("button", { name: /Langfuse traces · scaffold/ });
    expect(trace.getAttribute("title")).toContain("SQLC");
    expect(screen.queryByRole("link", { name: /trace/i })).toBeNull();
    fireEvent.click(trace);
    const dialog = screen.getByRole("dialog", { name: "Langfuse traces" });
    expect(within(dialog).getByText("Scaffold · not connected")).toBeTruthy();
    expect(within(dialog).getByText(/never invent a trace URL/)).toBeTruthy();
    expect(within(dialog).getByText("Ready when")).toBeTruthy();
  });

  it("never turns an unreadable initial snapshot into empty lanes", () => {
    const { rerender } = render(<SessionBoard sessions={[]} worktrees={[]} loading error="offline" kanban />);
    expect(screen.queryByRole("region", { name: "Idle sessions" })).toBeNull();
    expect(screen.getByText("Tasks could not be loaded. Refresh to try again.")).toBeTruthy();
    rerender(<SessionBoard sessions={[session]} worktrees={[]} loading={false} error="offline" kanban />);
    expect(screen.getByText("Refresh failed · previous snapshot")).toBeTruthy();
    expect(screen.getByRole("link", { name: `Session ${session.id}` })).toBeTruthy();
  });
});
