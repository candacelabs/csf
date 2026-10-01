import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Terminal } from "../api/client";
import { renderWithMantine as render } from "../test-utils";

const xtermState = vi.hoisted(() => ({
  dataHandlers: [] as Array<(data: string) => void>,
  resizeHandlers: [] as Array<(dimensions: { cols: number; rows: number }) => void>,
  writes: [] as string[],
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit() {}
  },
}));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    loadAddon() {}
    open() {}
    focus() {}
    write(data: string) { xtermState.writes.push(data); }
    writeln() {}
    dispose() {}
    onData(handler: (data: string) => void) {
      xtermState.dataHandlers.push(handler);
      return { dispose: () => undefined };
    }
    onResize(handler: (dimensions: { cols: number; rows: number }) => void) {
      xtermState.resizeHandlers.push(handler);
      return { dispose: () => undefined };
    }
  },
}));

import { TerminalPane } from "./TerminalPane";

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent<string>) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  close = vi.fn();

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }
}

const running: Terminal = {
  id: "33333333-3333-4333-8333-333333333333",
  worktreeId: "22222222-2222-4222-8222-222222222222",
  status: "running",
  shell: "/bin/bash",
  columns: 100,
  rows: 30,
  createdAt: "2026-09-05T00:00:00Z",
  updatedAt: "2026-09-05T00:00:00Z",
};

afterEach(() => {
  FakeEventSource.instances = [];
  xtermState.dataHandlers = [];
  xtermState.resizeHandlers = [];
  xtermState.writes = [];
  vi.unstubAllGlobals();
});

describe("TerminalPane", () => {
  it("surfaces a rejected terminal list and permits a retry", async () => {
    let attempts = 0;
    vi.stubGlobal("fetch", vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new Error("terminal list unavailable");
      return new Response(JSON.stringify({ data: [] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    expect((await screen.findByRole("alert")).textContent).toContain("terminal list unavailable");
    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));

    await screen.findByText("No terminal open");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(attempts).toBe(2);
  });

  it("requires reconciliation after an ambiguous terminal creation", async () => {
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (request.method === "GET") {
        return new Response(JSON.stringify({ data: [] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      throw new Error("connection lost after starting terminal");
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByText("No terminal open");
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Start terminal" }));
      await Promise.resolve();
    });

    expect((await screen.findByRole("alert")).textContent).toContain(
      "Terminal creation could not be confirmed. A terminal may already be running",
    );
    expect((screen.getByRole("button", { name: "Start terminal" }) as HTMLButtonElement).disabled).toBe(true);
    const refresh = screen.getByRole("button", { name: /Refresh/ }) as HTMLButtonElement;
    expect(refresh.disabled).toBe(false);

    await act(async () => {
      fireEvent.click(refresh);
      await Promise.resolve();
    });
    await vi.waitFor(() => expect((screen.getByRole("button", { name: "Start terminal" }) as HTMLButtonElement).disabled).toBe(false));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("admits only one terminal creation while its response is pending", async () => {
    let finishCreate: ((response: Response) => void) | undefined;
    let creates = 0;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (request.method === "GET") {
        return new Response(JSON.stringify({ data: [] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      creates += 1;
      return new Promise<Response>((resolve) => { finishCreate = resolve; });
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByText("No terminal open");
    const start = screen.getByRole("button", { name: "Start terminal" });
    fireEvent.click(start);
    fireEvent.click(start);

    await vi.waitFor(() => expect(creates).toBe(1));
    expect(screen.getByRole("button", { name: "Starting…" }).hasAttribute("disabled")).toBe(true);
    await act(async () => finishCreate?.(new Response(JSON.stringify(running), {
      status: 201,
      headers: { "content-type": "application/json" },
    })));
    await screen.findByRole("tab", { name: /shell 1/ });
  });

  it("does not overlap a stale terminal list with a mutation or clear its ambiguous result", async () => {
    let reads = 0;
    let creates = 0;
    let finishRefresh: ((response: Response) => void) | undefined;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (request.method === "GET") {
        reads += 1;
        if (reads === 1) {
          return new Response(JSON.stringify({ data: [running] }), {
            status: 200,
            headers: { "content-type": "application/json" },
          });
        }
        return new Promise<Response>((resolve) => { finishRefresh = resolve; });
      }
      creates += 1;
      throw new Error("connection lost after starting terminal");
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByRole("tab", { name: /shell 1/ });
    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));
    await vi.waitFor(() => expect(finishRefresh).toBeDefined());
    const create = screen.getByRole("button", { name: "＋ Terminal" }) as HTMLButtonElement;
    expect(create.disabled).toBe(true);
    fireEvent.click(create);
    expect(creates).toBe(0);

    await act(async () => finishRefresh?.(new Response(JSON.stringify({ data: [running] }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "＋ Terminal" }));
      await Promise.resolve();
    });

    expect((await screen.findByRole("alert")).textContent).toContain(
      "Terminal creation could not be confirmed. A terminal may already be running",
    );
    expect(creates).toBe(1);
    expect(reads).toBe(2);
    expect((screen.getByRole("button", { name: "＋ Terminal" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("serializes terminal creation behind a stop reconciliation", async () => {
    const stopped = { ...running, status: "exited" as const, exitCode: 0 };
    const created = {
      ...running,
      id: "44444444-4444-4444-8444-444444444444",
      createdAt: "2026-09-05T00:00:01Z",
      updatedAt: "2026-09-05T00:00:01Z",
    };
    let reads = 0;
    let creates = 0;
    let finishStopRefresh: ((response: Response) => void) | undefined;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (request.method === "GET") {
        reads += 1;
        if (reads === 1) {
          return new Response(JSON.stringify({ data: [running] }), {
            status: 200,
            headers: { "content-type": "application/json" },
          });
        }
        return new Promise<Response>((resolve) => { finishStopRefresh = resolve; });
      }
      if (request.method === "DELETE") return new Response(null, { status: 204 });
      creates += 1;
      return new Response(JSON.stringify(created), {
        status: 201,
        headers: { "content-type": "application/json" },
      });
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByRole("tab", { name: /shell 1/ });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Stop" }));
      await Promise.resolve();
    });
    await vi.waitFor(() => expect(finishStopRefresh).toBeDefined());

    const create = screen.getByRole("button", { name: "＋ Terminal" }) as HTMLButtonElement;
    expect(create.disabled).toBe(true);
    fireEvent.click(create);
    expect(creates).toBe(0);

    await act(async () => finishStopRefresh?.(new Response(JSON.stringify({ data: [stopped] }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));
    await screen.findByRole("tab", { name: /shell 1 \(0\)/ });
    const reconciledCreate = screen.getByRole("button", { name: "＋ Terminal" });
    await act(async () => {
      fireEvent.click(reconciledCreate);
      await Promise.resolve();
    });
    await vi.waitFor(() => expect(creates).toBe(1));
    await screen.findByRole("tab", { name: /shell 1/ });
    expect(screen.getAllByRole("tab", { name: /shell/ })).toHaveLength(2);
  });

  it("requires reconciliation after an ambiguous terminal stop", async () => {
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (request.method === "GET") {
        return new Response(JSON.stringify({ data: [running] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (request.method === "DELETE") throw new Error("connection lost after stopping terminal");
      return new Response(JSON.stringify(running), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByRole("tab", { name: /shell 1/ });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Stop" }));
      await Promise.resolve();
    });

    expect((await screen.findByRole("alert")).textContent).toContain("Terminal stop could not be confirmed");
    expect((screen.getByRole("button", { name: "Stop" }) as HTMLButtonElement).disabled).toBe(true);
    const refresh = screen.getByRole("button", { name: /Refresh/ }) as HTMLButtonElement;
    expect(refresh.disabled).toBe(false);

    await act(async () => {
      fireEvent.click(refresh);
      await Promise.resolve();
    });
    await vi.waitFor(() => expect((screen.getByRole("button", { name: "Stop" }) as HTMLButtonElement).disabled).toBe(false));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("admits only one terminal stop transition at a time", async () => {
    const second = {
      ...running,
      id: "55555555-5555-4555-8555-555555555555",
      createdAt: "2026-09-05T00:00:01Z",
      updatedAt: "2026-09-05T00:00:01Z",
    };
    let deletes = 0;
    let finishFirstStop: ((response: Response) => void) | undefined;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
      if (request.method === "GET") {
        return new Response(JSON.stringify({ data: [running, second] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (request.method === "DELETE") {
        deletes += 1;
        if (deletes === 1) return new Promise<Response>((resolve) => { finishFirstStop = resolve; });
        return new Response(null, { status: 204 });
      }
      return new Response(JSON.stringify(running), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByRole("tab", { name: /shell 1/ });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Stop" }));
      await Promise.resolve();
    });
    await vi.waitFor(() => expect(finishFirstStop).toBeDefined());
    fireEvent.click(screen.getByRole("tab", { name: /shell 2/ }));

    const secondStop = screen.getByRole("button", { name: "Stop" }) as HTMLButtonElement;
    expect(secondStop.disabled).toBe(true);
    fireEvent.click(secondStop);
    expect(deletes).toBe(1);

    await act(async () => finishFirstStop?.(new Response(null, { status: 204 })));
    await vi.waitFor(() => expect((screen.getByRole("button", { name: "Stop" }) as HTMLButtonElement).disabled).toBe(false));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Stop" }));
      await Promise.resolve();
    });
    await vi.waitFor(() => expect(deletes).toBe(2));
  });

  it.each([
    { kind: "exited" as const, endedStatus: "exited" as const, exitCode: 0 },
    { kind: "failed" as const, endedStatus: "failed" as const, data: "shell failed" },
  ])("closes a $kind stream without reporting its expected EOF as a reconnect", async (ended) => {
    let status: Terminal["status"] = "running";
    let reads = 0;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", async () => {
      reads += 1;
      return new Response(JSON.stringify({
        data: [{ ...running, status, ...(status === "exited" ? { exitCode: 0 } : {}) }],
      }), { status: 200, headers: { "content-type": "application/json" } });
    });

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByRole("tab", { name: /shell 1/ });
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    const stream = FakeEventSource.instances[0];
    if (stream === undefined) throw new Error("terminal stream was not opened");

    status = ended.endedStatus;
    await act(async () => {
      stream.onmessage?.(new MessageEvent("message", { data: JSON.stringify({
        seq: 1,
        terminalId: running.id,
        kind: ended.kind,
        occurredAt: "2026-09-05T00:00:01Z",
        ...(ended.kind === "exited" ? { exitCode: ended.exitCode } : { data: ended.data }),
        replayTruncated: false,
      }) }));
    });
    expect(stream.close).toHaveBeenCalled();

    act(() => stream.onerror?.(new Event("error")));
    expect(screen.queryByText("terminal stream disconnected; reconnecting…")).toBeNull();
    await vi.waitFor(() => expect(reads).toBeGreaterThan(1));
    if (ended.kind === "exited") await screen.findByRole("tab", { name: /shell 1 \(0\)/ });
  });

  it.each(["exited", "failed"] as const)("keeps %s terminal history replay-only", async (status) => {
    const historical: Terminal = status === "exited"
      ? { ...running, status, exitCode: 0 }
      : { ...running, status };
    const methods: string[] = [];
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      methods.push(request.method);
      return new Response(JSON.stringify({ data: [historical] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByRole("tab", { name: /shell 1/ });
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));

    expect(xtermState.dataHandlers).toHaveLength(0);
    expect(xtermState.resizeHandlers).toHaveLength(0);
    const stream = FakeEventSource.instances[0];
    if (stream === undefined) throw new Error("terminal history stream was not opened");
    expect(stream.url).toBe(`/v1/worktrees/${running.worktreeId}/terminals/${running.id}/events`);

    act(() => stream.onmessage?.(new MessageEvent("message", { data: JSON.stringify({
      seq: 1,
      terminalId: running.id,
      kind: "output",
      data: `replayed ${status} output`,
      occurredAt: "2026-09-05T00:00:01Z",
      replayTruncated: false,
    }) })));
    expect(xtermState.writes).toContain(`replayed ${status} output`);
    expect(methods).toEqual(["GET"]);
  });

  it("keeps an exit event ahead of a stale running list response and refreshes again", async () => {
    const exited: Terminal = { ...running, status: "exited", exitCode: 23 };
    let reads = 0;
    let finishStale: ((response: Response) => void) | undefined;
    let finishFollowup: ((response: Response) => void) | undefined;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.method !== "GET") throw new Error(`unexpected terminal mutation: ${request.method}`);
      reads += 1;
      if (reads === 1) {
        return new Response(JSON.stringify({ data: [running] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (reads === 2) return new Promise<Response>((resolve) => { finishStale = resolve; });
      if (reads === 3) return new Promise<Response>((resolve) => { finishFollowup = resolve; });
      throw new Error(`unexpected terminal read ${reads}`);
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByRole("tab", { name: /shell 1/ });
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    const stream = FakeEventSource.instances[0];
    if (stream === undefined) throw new Error("terminal stream was not opened");

    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));
    await vi.waitFor(() => expect(finishStale).toBeDefined());
    await act(async () => {
      stream.onmessage?.(new MessageEvent("message", { data: JSON.stringify({
        seq: 1,
        terminalId: running.id,
        kind: "exited",
        exitCode: 23,
        occurredAt: "2026-09-05T00:00:01Z",
        replayTruncated: false,
      }) }));
    });
    await screen.findByRole("tab", { name: /shell 1 \(23\)/ });

    await act(async () => finishStale?.(new Response(JSON.stringify({ data: [running] }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));
    await vi.waitFor(() => expect(reads).toBe(3));
    expect(screen.getByRole("tab", { name: /shell 1 \(23\)/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();

    await act(async () => finishFollowup?.(new Response(JSON.stringify({ data: [exited] }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })));
    expect(await screen.findByRole("tab", { name: /shell 1 \(23\)/ })).toBeTruthy();
  });

  it("serializes resize requests and coalesces a burst to its latest dimensions", async () => {
    type PendingResize = {
      body: { columns: number; rows: number };
      resolve: (response: Response) => void;
    };
    const pending: PendingResize[] = [];
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.method === "GET") {
        return new Response(JSON.stringify({ data: [running] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      const body = await request.clone().json() as PendingResize["body"];
      return new Promise<Response>((resolve) => pending.push({ body, resolve }));
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByRole("tab", { name: /shell 1/ });
    await vi.waitFor(() => expect(xtermState.resizeHandlers).toHaveLength(1));
    const resize = xtermState.resizeHandlers[0];
    if (resize === undefined) throw new Error("terminal resize listener was not installed");

    act(() => {
      resize({ cols: 101, rows: 31 });
      resize({ cols: 102, rows: 32 });
      resize({ cols: 103, rows: 33 });
    });
    await vi.waitFor(() => expect(pending).toHaveLength(1));
    expect(pending[0]?.body).toEqual({ columns: 101, rows: 31 });

    await act(async () => {
      pending[0]?.resolve(new Response(JSON.stringify({ ...running, columns: 101, rows: 31 }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }));
    });
    await vi.waitFor(() => expect(pending).toHaveLength(2));
    expect(pending[1]?.body).toEqual({ columns: 103, rows: 33 });
    pending[1]?.resolve(new Response(JSON.stringify({ ...running, columns: 103, rows: 33 }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
  });

  it.each([
    {
      name: "API",
      result: () => new Response(JSON.stringify({ code: "terminal_state", message: "resize rejected" }), {
        status: 409,
        headers: { "content-type": "application/json" },
      }),
      expected: "terminal_state: resize rejected",
    },
    {
      name: "network",
      result: () => Promise.reject(new Error("resize network unavailable")),
      expected: "resize network unavailable",
    },
  ])("surfaces a resize $name failure", async ({ result, expected }) => {
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("ResizeObserver", class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.method === "GET") {
        return new Response(JSON.stringify({ data: [running] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      return result();
    }));

    render(<TerminalPane worktreeId={running.worktreeId} />);
    await screen.findByRole("tab", { name: /shell 1/ });
    await vi.waitFor(() => expect(xtermState.resizeHandlers).toHaveLength(1));
    const resize = xtermState.resizeHandlers[0];
    if (resize === undefined) throw new Error("terminal resize listener was not installed");
    act(() => resize({ cols: 120, rows: 40 }));
    expect((await screen.findByRole("alert")).textContent).toBe(expected);
  });
});
