import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Dock } from "./Dock";
import { renderWithMantine as render } from "../test-utils";

describe("Dock", () => {
  it("uses native library arrow navigation and the existing pane/position callbacks", () => {
    const onTab = vi.fn();
    const onOpenChange = vi.fn();
    const onPosition = vi.fn();
    render(<Dock active="subagents" open={false} position="bottom" sessionId="session" worktreeId="worktree"
      revision={0} subagents={[]} liveActivity={{}} onTab={onTab} onOpenChange={onOpenChange}
      onPosition={onPosition} onSubagentsLoaded={() => undefined} />);
    screen.getByRole("tab", { name: "Worktree" }).focus();
    fireEvent.keyDown(screen.getByRole("tab", { name: "Worktree" }), { key: "ArrowRight" });
    expect(document.activeElement).toBe(screen.getByRole("tab", { name: "Schedules" }));
    expect(onTab).toHaveBeenLastCalledWith("schedules");
    expect(onOpenChange).toHaveBeenLastCalledWith(true);
    fireEvent.click(screen.getByRole("button", { name: "Dock pane at right" }));
    expect(onPosition).toHaveBeenCalledWith("right");
  });

  it("keeps every pane name in the accessibility tree when labels are visually hidden", () => {
    render(
      <Dock
        active="subagents"
        open={false}
        position="right"
        sessionId="11111111-1111-4111-8111-111111111111"
        worktreeId="22222222-2222-4222-8222-222222222222"
        revision={0}
        subagents={[]}
        liveActivity={{}}
        onTab={() => undefined}
        onOpenChange={() => undefined}
        onPosition={() => undefined}
        onSubagentsLoaded={() => undefined}
      />,
    );

    for (const name of ["Terminal", "Changes", "Worktree", "Schedules", "Subagents"]) {
      expect(screen.getByRole("tab", { name })).toBeTruthy();
    }
  });
});
