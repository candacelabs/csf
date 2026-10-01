import { describe, expect, it, vi } from "vitest";
import { terminalInput } from "./terminalInput";

describe("terminal input backpressure", () => {
  it("sends the first key immediately and combines pending keys in order", async () => {
    let release!: () => void;
    const first = new Promise<void>((resolve) => { release = resolve; });
    const send = vi.fn().mockImplementationOnce(() => first).mockResolvedValue(undefined);
    const input = terminalInput(send, vi.fn());
    input.write("a");
    input.write("b");
    input.write("\u001b[D");
    input.write("c");
    expect(send.mock.calls).toEqual([["a"]]);
    release();
    await first;
    expect(send.mock.calls).toEqual([["a"], ["b\u001b[Dc"]]);
  });

  it("splits large Unicode pastes at the API character limit without splitting a rune", async () => {
    const send = vi.fn().mockResolvedValue(undefined);
    const input = terminalInput(send, vi.fn());
    input.write("😀".repeat(65_537));
    await vi.waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send.mock.calls.map(([data]) => Array.from(data as string).length)).toEqual([65_536, 1]);
    expect(send.mock.calls.map(([data]) => data).join("")).toBe("😀".repeat(65_537));
  });

  it("stops after an uncertain request without replaying it or sending queued commands", async () => {
    let reject!: (cause: Error) => void;
    const first = new Promise<void>((_, fail) => { reject = fail; });
    const send = vi.fn(() => first);
    const failure = vi.fn();
    const input = terminalInput(send, failure);
    input.write("command\r");
    input.write("another\r");
    reject(new Error("disconnected"));
    await vi.waitFor(() => expect(failure).toHaveBeenCalledOnce());
    input.write("more");
    expect(send).toHaveBeenCalledOnce();
  });

  it("discards unsent input when the pane is closed", async () => {
    let release!: () => void;
    const first = new Promise<void>((resolve) => { release = resolve; });
    const send = vi.fn(() => first);
    const input = terminalInput(send, vi.fn());
    input.write("a");
    input.write("b");
    input.close();
    release();
    await first;
    expect(send.mock.calls).toEqual([["a"]]);
  });

  it("reports overflow and pauses instead of retaining an unbounded paste", () => {
    const send = vi.fn();
    const failure = vi.fn();
    const input = terminalInput(send, failure);
    input.write("x".repeat(262_145));
    input.write("y");
    expect(failure).toHaveBeenCalledOnce();
    expect(send).not.toHaveBeenCalled();
  });
});
