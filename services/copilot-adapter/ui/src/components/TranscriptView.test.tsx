import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TranscriptView } from "./TranscriptView";
import type { SessionRequest } from "../api/client";
import type { TranscriptEntry } from "../transcript";
import { renderWithMantine as render } from "../test-utils";

const now = new Date("2026-09-04T12:00:00Z");

function message(overrides: Partial<Extract<TranscriptEntry, { kind: "message" }>> = {}) {
  const entry: Extract<TranscriptEntry, { kind: "message" }> = {
    kind: "message",
    key: "item-1",
    seq: 1,
    role: "assistant",
    text: "hello",
    occurredAt: "2026-09-04T11:30:00Z",
    streaming: false,
    ...overrides,
  };
  return entry;
}

function tool(overrides: Partial<Extract<TranscriptEntry, { kind: "tool" }>> = {}) {
  const entry: Extract<TranscriptEntry, { kind: "tool" }> = {
    kind: "tool",
    key: "tool-2",
    seq: 2,
    toolName: "bash",
    args: '{"command":"pytest -x"}',
    occurredAt: "2026-09-04T11:30:00Z",
    ...overrides,
  };
  return entry;
}

describe("TranscriptView", () => {
  it("retains an unfinished historical reply without claiming an idle session is thinking", () => {
    const entry = message({ streaming: true, text: "Retained partial reply" });
    const { rerender } = render(<TranscriptView entries={[entry]} now={now} streamingActive={false} />);
    expect(screen.getByText("Retained partial reply")).toBeTruthy();
    expect(screen.queryByText("thinking")).toBeNull();
    rerender(<TranscriptView entries={[entry]} now={now} streamingActive />);
    expect(screen.getByText("thinking")).toBeTruthy();
    rerender(<TranscriptView entries={[{ ...entry, text: "" }]} now={now} streamingActive={false} />);
    expect(screen.getByText("Nothing said yet")).toBeTruthy();
  });

  const approval: SessionRequest = {
    id: "approval-one", sessionId: "session-one", turnId: "turn-one",
    kind: "permission", status: "pending", toolName: "read",
    prompt: JSON.stringify({ toolCallId: "call-one", path: "/workspace/handoff.md" }),
    createdAt: "2026-09-04T11:30:00Z",
  };
  const guardedTool = tool({ toolName: "view", toolCallId: "call-one", turnId: "turn-one" });

  it("matches approval by call and turn despite different tool names, then clears on resolution", () => {
    const review = vi.fn();
    const view = render(<TranscriptView entries={[guardedTool]} now={now} requests={[approval]} onReviewRequest={review} />);
    expect(screen.getByText("Waiting for approval")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Review approval" }));
    expect(review).toHaveBeenCalledWith(approval.id);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(screen.queryByText("Waiting for the tool result…")).toBeNull();
    view.rerender(<TranscriptView entries={[guardedTool]} now={now} requests={[{ ...approval, status: "approved" }]} onReviewRequest={review} />);
    expect(screen.queryByText("Waiting for approval")).toBeNull();
    expect(screen.queryByRole("button", { name: "Review approval" })).toBeNull();
    expect(screen.getByText("Waiting for the tool result…")).toBeTruthy();
  });

  it.each([
    { turnId: "different-turn" },
    { turnId: undefined },
    { prompt: JSON.stringify({ toolCallId: "different-call" }) },
    { prompt: JSON.stringify({ toolName: "view" }) },
    { prompt: "Read file?" },
    { prompt: "null" },
  ])("does not guess approval identity from incomplete or unrelated requests: %j", (overrides) => {
    render(<TranscriptView entries={[guardedTool]} now={now} requests={[{ ...approval, ...overrides }]} />);
    expect(screen.queryByText("Waiting for approval")).toBeNull();
    expect(screen.getByText("running")).toBeTruthy();
  });

  it("does not mask a recorded result with a stale pending approval", () => {
    render(<TranscriptView entries={[{ ...guardedTool, result: "File contents" }]} now={now} requests={[approval]} />);
    expect(screen.queryByText("Waiting for approval")).toBeNull();
    expect(screen.getByText("done")).toBeTruthy();
  });

  it("tells the reader what to do when there is nothing to show", () => {
    render(<TranscriptView entries={[]} now={now} />);
    expect(screen.getByText("Nothing said yet")).toBeTruthy();
  });

  it("renders assistant text as Markdown and user text literally", () => {
    const { container } = render(
      <TranscriptView
        entries={[
          message({ text: "**bold**" }),
          message({ key: "item-2", seq: 2, role: "user", text: "**not bold**" }),
        ]}
        now={now}
      />,
    );
    expect(container.querySelector(".bubble-assistant strong")?.textContent).toBe("bold");
    expect(container.querySelector(".bubble-user strong")).toBeNull();
    expect(container.querySelector(".bubble-user .literal")?.textContent).toBe("**not bold**");
  });

  it("names the author and dates the row relatively", () => {
    render(<TranscriptView entries={[message({ role: "user", author: "Ada" })]} now={now} />);
    expect(screen.getByText("Ada")).toBeTruthy();
    expect(screen.getByText("30m ago")).toBeTruthy();
  });

  it("shows a streaming assistant row as thinking", () => {
    render(<TranscriptView entries={[message({ streaming: true, text: "Sure —" })]} now={now} />);
    expect(screen.getByText("thinking")).toBeTruthy();
  });

  // The pairing rule is the reducer's; what this asserts is that the card the
  // reducer produced reads as one thing — name, one-line summary, status —
  // with the bodies behind a disclosure rather than always on screen.
  it("collapses a tool call and its result into one expandable card", () => {
    const { container } = render(
      <TranscriptView entries={[tool({ result: "1 failed" })]} now={now} />,
    );
    expect(screen.getByText("bash")).toBeTruthy();
    expect(screen.getByRole("button", { name: /pytest -x/ })).toBeTruthy();
    expect(screen.getByText("done")).toBeTruthy();
    expect(container.querySelector(".tool-card")?.classList.contains("open")).toBe(false);

    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(container.querySelector(".tool-card")?.classList.contains("open")).toBe(true);
    expect(container.querySelector(".tool-body")?.textContent).toContain("1 failed");
  });

  it("leaves out the empty assistant row a tool-only turn stores", () => {
    const { container } = render(
      <TranscriptView entries={[message({ text: "" }), tool()]} now={now} />,
    );
    expect(container.querySelectorAll(".bubble")).toHaveLength(0);
    expect(container.querySelectorAll(".tool-card")).toHaveLength(1);
  });

  it("keeps a streaming row that has not received its first delta", () => {
    const { container } = render(
      <TranscriptView entries={[message({ text: "", streaming: true })]} now={now} />,
    );
    expect(container.querySelectorAll(".bubble")).toHaveLength(1);
  });

  it("reads an unpaired call as still running", () => {
    render(<TranscriptView entries={[tool()]} now={now} />);
    expect(screen.getByText("running")).toBeTruthy();
  });

  it.each(["aborted", "failed"] as const)(
    "reads an interrupted unpaired call as %s",
    (terminalStatus) => {
      render(
        <TranscriptView entries={[tool({ terminalStatus })]} now={now} />,
      );
      expect(screen.getByText(terminalStatus)).toBeTruthy();
      expect(screen.queryByText("running")).toBeNull();
    },
  );

  it("does not claim success when a turn completes without a tool result", () => {
    render(<TranscriptView entries={[tool({ terminalStatus: "completed" })]} now={now} />);
    expect(screen.getByText("no result")).toBeTruthy();
    expect(screen.queryByText("done")).toBeNull();
    expect(screen.queryByText("running")).toBeNull();
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(screen.getByText("The turn ended without a recorded tool result.")).toBeTruthy();
  });

  it("decodes the shell payload while keeping original data in a collapsed inspector", () => {
    const args = '{"command":"printf \\\"hello\\\\n\\\" \\u003e result.txt","description":"Write the result","mode":"sync"}';
    const marker = "\n\n<shellId: 0 completed with exit code 0>";
    const result = JSON.stringify({
      content: `hello\n${marker}`,
      contents: [{ type: "shell_exit", cwd: "/workspace", exitCode: 0, shellId: "0" }],
      detailedContent: `hello\n${marker}`,
      receipt: "retained-in-raw-inspector",
    });
    const { container } = render(
      <TranscriptView entries={[tool({ args, result, toolCallId: "shell-call-1" })]} now={now} />,
    );
    expect(screen.getByRole("button", { name: /Write the result/ })).toBeTruthy();
    expect(screen.getByText("done · exit 0")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { expanded: false }));

    expect(container.querySelector(".tool-command")?.textContent).toBe('printf "hello\\n" > result.txt');
    expect(container.querySelector(".tool-output")?.textContent).toBe("hello");
    expect(screen.getByText("/workspace")).toBeTruthy();
    const inspector = container.querySelector<HTMLDetailsElement>(".tool-inspector");
    expect(inspector?.open).toBe(false);
    expect(inspector?.querySelectorAll("pre")[0]?.textContent).toBe(args);
    expect(inspector?.querySelectorAll("pre")[1]?.textContent).toBe(result);
    expect(inspector?.textContent).toContain("shell-call-1");
  });

  it.each([
    [{ contents: [{ type: "shell_exit", exitCode: 23 }], content: "command failed" }, "failed · exit 23"],
    [{ isError: true, content: [{ type: "text", text: "Access denied" }] }, "failed"],
    [{ success: false, error: { message: "Tool unavailable" } }, "failed"],
  ])("shows explicit tool failures instead of a successful return", (result, status) => {
    const { container } = render(<TranscriptView entries={[tool({ result: JSON.stringify(result) })]} now={now} />);
    expect(screen.getByText(status)).toBeTruthy();
    expect(container.querySelector(".tool-status-terminal")).not.toBeNull();
    expect(screen.queryByText("done")).toBeNull();
  });

  it("unwraps encoded MCP content and renders markup as literal tool output", () => {
    const text = '<script>alert("hello")</script>\nsecond line';
    const result = JSON.stringify(JSON.stringify({ content: [{ type: "text", text }] }));
    const { container } = render(<TranscriptView entries={[tool({ result })]} now={now} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(container.querySelector(".tool-output")?.textContent).toBe(text);
    expect(container.querySelector("script")).toBeNull();
  });

  it.each([
    ["{incomplete JSON", "{incomplete JSON"],
    [JSON.stringify({ files: ["a.go", "b.go"], count: 2 }), '{\n  "files": [\n    "a.go",\n    "b.go"\n  ],\n  "count": 2\n}'],
    ["", "No output."],
    [JSON.stringify({ content: [] }), "No output."],
  ])("preserves unfamiliar or empty results without inventing an exit code", (result, expected) => {
    const { container } = render(<TranscriptView entries={[tool({ result })]} now={now} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(container.querySelector(".tool-output")?.textContent).toBe(expected);
    expect(screen.queryByText(/exit \d/)).toBeNull();
  });

  it("keeps distinct content representations available in the readable result", () => {
    const result = JSON.stringify({ content: "Summary", detailedContent: "Additional detail", contents: [{ type: "image", data: "image-receipt" }] });
    const { container } = render(<TranscriptView entries={[tool({ result })]} now={now} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    const output = container.querySelector(".tool-output")?.textContent;
    expect(output).toContain("Summary");
    expect(output).toContain("Additional detail");
    expect(output).toContain("image-receipt");
  });

  it("preserves repeated content blocks and distinguishes stderr from stdout", () => {
    const result = JSON.stringify({
      content: [{ type: "text", text: "same" }, { type: "text", text: "same" }],
      stdout: "stdout line",
      stderr: "stdout line",
    });
    const { container } = render(<TranscriptView entries={[tool({ result })]} now={now} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(container.querySelector(".tool-output")?.textContent).toBe("stdout line\n\nstderr:\nstdout line\n\nsame\nsame");
  });

  it("renders a durable denial acknowledgement as a system message", () => {
    render(
      <TranscriptView
        entries={[message({ role: "system", text: "Permission denied for bash." })]}
        now={now}
      />,
    );
    expect(screen.getByText("System")).toBeTruthy();
    expect(screen.getByText("Permission denied for bash.")).toBeTruthy();
  });
});
