import { createRef } from "react";
import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Model, Session, SessionEvent, SessionRequest } from "./api/client";
import { renderWithMantine as render } from "./test-utils";
import { SessionPage } from "./SessionPage";

vi.mock("./components/Dock", () => ({ Dock: () => null }));

const sessionId = "11111111-1111-4111-8111-111111111111";
const turnId = "22222222-2222-4222-8222-222222222222";
const requestId = "33333333-3333-4333-8333-333333333333";
const secondRequestId = "44444444-4444-4444-8444-444444444444";
const timestamp = "2026-09-06T12:00:00Z";

const models: Model[] = [
  { id: "model-a", displayName: "Model A", capabilities: ["chat"] },
  { id: "model-b", displayName: "Model B", capabilities: ["chat"] },
  { id: "model-c", displayName: "Model C", capabilities: ["chat"] },
];

function session(overrides: Partial<Session> = {}): Session {
  return {
    id: sessionId,
    displayName: "Audit",
    model: "model-a",
    worktreeId: "55555555-5555-4555-8555-555555555555",
    workingDirectory: "/workspace",
    status: "idle",
    createdAt: timestamp,
    updatedAt: timestamp,
    turnCount: 0,
    ...overrides,
  };
}

function request(id = requestId, prompt = "Run tests?"): SessionRequest {
  return { id, sessionId, kind: "permission", status: "pending", prompt, createdAt: timestamp };
}

function runningTurn(id = turnId) {
  return { id, sessionId, status: "running" as const, createdAt: timestamp };
}

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json" } });
}

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: MessageEvent<string>) => void) | null = null;
  onerror: (() => void) | null = null;
  readonly close = vi.fn();

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }

  emit(event: SessionEvent) {
    this.onmessage?.({ data: JSON.stringify(event) } as MessageEvent<string>);
  }
}

function bootstrap(path: string, currentSession: Session, requests: SessionRequest[] = []): Response | null {
  if (path === `/v1/sessions/${sessionId}`) return json(currentSession);
  if (path === `/v1/sessions/${sessionId}/transcript`) return json({ data: [] });
  if (path === `/v1/sessions/${sessionId}/requests`) return json({ data: requests });
  if (path === `/v1/sessions/${sessionId}/subagents`) return json({ data: [] });
  return null;
}

function renderSession(currentSession = session(), onResourceChanged = vi.fn()) {
  return render(
    <SessionPage
      sessionId={sessionId}
      initialSession={currentSession}
      worktree={null}
      models={models}
      menuButtonRef={createRef<HTMLButtonElement>()}
      onMenu={() => undefined}
      onResourceChanged={onResourceChanged}
    />,
  );
}

async function ready() {
  await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
}

async function renderReadySession(currentSession = session(), onResourceChanged = vi.fn()) {
  let view: ReturnType<typeof renderSession> | undefined;
  await act(async () => { view = renderSession(currentSession, onResourceChanged); });
  await ready();
  return view as ReturnType<typeof renderSession>;
}

afterEach(() => {
  FakeEventSource.instances = [];
  vi.unstubAllGlobals();
});

describe("SessionPage mutation and replay ordering", () => {
  it("keeps approvals beside the composer and links the matching tool to its existing controls", async () => {
    const approval: SessionRequest = {
      ...request(), turnId, toolName: "read", prompt: JSON.stringify({ toolCallId: "call-one", path: "/workspace/handoff.md" }),
    };
    let pending = [approval];
    const decisions: unknown[] = [];
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const path = new URL(incoming.url).pathname;
      if (incoming.method === "POST" && path.endsWith(`/requests/${requestId}/resolve`)) {
        decisions.push(await incoming.clone().json());
        pending = [];
        return json({ ...approval, status: "approved", resolvedAt: timestamp });
      }
      if (path.endsWith("/transcript")) return json({ data: [{
        seq: 1, sessionId, turnId, kind: "toolCall", toolName: "view", toolCallId: "call-one",
        text: JSON.stringify({ path: "/workspace/handoff.md" }), occurredAt: timestamp,
      }] });
      const bootstrapped = bootstrap(path, session(), pending);
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      throw new Error(`unexpected request: ${incoming.method} ${path}`);
    });
    await renderReadySession();
    const panel = screen.getByRole("region", { name: "Pending requests" });
    expect(panel.closest(".chat-scroll")).toBeNull();
    expect(panel.nextElementSibling?.classList.contains("composer")).toBe(true);
    expect(screen.getByText("Waiting for approval")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Review approval" }));
    expect(document.activeElement).toBe(screen.getByRole("article", { name: "Pending request for read" }));
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Approve" })); });
    expect(decisions).toEqual([{ decision: "approve" }]);
    expect(screen.queryByRole("region", { name: "Pending requests" })).toBeNull();
    expect(screen.queryByText("Waiting for approval")).toBeNull();
    expect(screen.getByText("running")).toBeTruthy();
  });

  it("keeps the fresh idle snapshot during historical running replay and accepts a new turn", async () => {
    const current = session({ updatedAt: "2026-09-06T12:00:00.000002Z" });
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const path = new URL(incoming.url).pathname;
      const bootstrapped = bootstrap(path, current);
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      throw new Error(`unexpected request: ${incoming.method} ${path}`);
    });
    await renderReadySession(current);
    act(() => FakeEventSource.instances[0]?.emit({
      seq: 1, sessionId, occurredAt: timestamp, kind: "sessionUpdated",
      payload: session({ status: "running", updatedAt: "2026-09-06T12:00:00.000001Z" }),
    }));
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
    act(() => FakeEventSource.instances[0]?.emit({
      seq: 2, sessionId, occurredAt: timestamp, kind: "sessionUpdated",
      payload: session({ status: "running", updatedAt: "2026-09-06T12:00:00.000003Z" }),
    }));
    expect(screen.getByRole("button", { name: "Stop" })).toBeTruthy();
  });

  it("shows the actual session model even when the catalog omits it", async () => {
    const current = session({ model: "unlisted-current-model" });
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const path = new URL(incoming.url).pathname;
      const bootstrapped = bootstrap(path, current);
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      throw new Error(`unexpected request: ${incoming.method} ${path}`);
    });
    await renderReadySession(current);
    expect((screen.getByLabelText("Model") as HTMLSelectElement).value).toBe(current.model);
    expect(screen.getByRole("option", { name: "unlisted-current-model (current)", selected: true })).toBeTruthy();
    expect(screen.getByText("Current model is not in the available catalog.")).toBeTruthy();
  });

  it("submits only once while in flight and locks the prompt identity", async () => {
    let finishPrompt: (response: Response) => void = () => undefined;
    const bodies: Array<Record<string, unknown>> = [];
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("crypto", {
      getRandomValues: (bytes: Uint8Array) => {
        bytes.forEach((_byte, index) => { bytes[index] = index; });
        return bytes;
      },
    });
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      const bootstrapped = bootstrap(url.pathname, session());
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      if (incoming.method === "POST" && url.pathname.endsWith("/prompts")) {
        bodies.push(await incoming.clone().json() as Record<string, unknown>);
        return await new Promise<Response>((resolve) => { finishPrompt = resolve; });
      }
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    await renderReadySession();
    const composer = screen.getByLabelText("Prompt");
    const mode = screen.getByLabelText("Prompt mode");
    fireEvent.change(composer, { target: { value: "first prompt" } });
    act(() => {
      fireEvent.keyDown(composer, { key: "Enter" });
      fireEvent.keyDown(composer, { key: "Enter" });
    });
    await vi.waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]?.["idempotencyKey"]).toBe("00010203-0405-4607-8809-0a0b0c0d0e0f");
    expect((composer as HTMLTextAreaElement).disabled).toBe(true);
    expect((mode as HTMLSelectElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Send prompt" }) as HTMLButtonElement).disabled).toBe(true);

    await act(async () => {
      finishPrompt(json({ id: turnId, sessionId, status: "queued", createdAt: timestamp }, 202));
    });
    await vi.waitFor(() => expect((composer as HTMLTextAreaElement).value).toBe(""));
    expect((composer as HTMLTextAreaElement).disabled).toBe(false);
    expect((mode as HTMLSelectElement).disabled).toBe(false);
    expect(bodies).toHaveLength(1);
  });

  it("surfaces an ambiguous prompt rejection and reuses its durable key on retry", async () => {
    const bodies: Array<Record<string, unknown>> = [];
    let uuidSeed = 0;
    const getRandomValues = vi.fn((bytes: Uint8Array) => {
      uuidSeed += 1;
      bytes.fill(uuidSeed);
      return bytes;
    });
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("crypto", { getRandomValues });
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      const bootstrapped = bootstrap(url.pathname, session());
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      if (incoming.method === "POST" && url.pathname.endsWith("/prompts")) {
        bodies.push(await incoming.clone().json() as Record<string, unknown>);
        if (bodies.length === 1) throw new TypeError("response lost");
        return json({ id: turnId, sessionId, status: "queued", createdAt: timestamp }, 202);
      }
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    await renderReadySession();
    fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: "retry me" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Send prompt" })); });
    expect((await screen.findByRole("alert")).textContent).toContain(
      "Sending the prompt did not receive a response and may have succeeded. Retry will reuse the same idempotency key. response lost",
    );
    expect((screen.getByLabelText("Prompt") as HTMLTextAreaElement).disabled).toBe(true);
    expect((screen.getByLabelText("Prompt mode") as HTMLSelectElement).disabled).toBe(true);
    expect(screen.getByText("Retry to confirm this prompt was sent")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Retry prompt" }) as HTMLButtonElement).disabled).toBe(false);

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Retry prompt" })); });
    await vi.waitFor(() => expect(bodies).toHaveLength(2));
    expect(bodies[1]?.["idempotencyKey"]).toBe(bodies[0]?.["idempotencyKey"]);
    expect(getRandomValues).toHaveBeenCalledOnce();
    await vi.waitFor(() => expect((screen.getByLabelText("Prompt") as HTMLTextAreaElement).value).toBe(""));
  });

  it("keeps a prompt's exact identity after an ambiguous HTTP 500", async () => {
    const bodies: Array<Record<string, unknown>> = [];
    let uuidSeed = 0;
    const getRandomValues = vi.fn((bytes: Uint8Array) => {
      uuidSeed += 1;
      bytes.fill(uuidSeed);
      return bytes;
    });
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("crypto", { getRandomValues });
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      const bootstrapped = bootstrap(url.pathname, session());
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      if (incoming.method === "POST" && url.pathname.endsWith("/prompts")) {
        bodies.push(await incoming.clone().json() as Record<string, unknown>);
        if (bodies.length === 1) return json({ code: "store_error", message: "commit result unknown" }, 500);
        return json({ id: turnId, sessionId, status: "queued", createdAt: timestamp }, 202);
      }
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    await renderReadySession();
    fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: "committed prompt" } });
    fireEvent.change(screen.getByLabelText("Prompt mode"), { target: { value: "steer" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Send prompt" })); });

    expect((await screen.findByRole("alert")).textContent).toContain(
      "The server could not confirm whether the prompt was accepted and it may have succeeded",
    );
    expect((screen.getByLabelText("Prompt") as HTMLTextAreaElement).disabled).toBe(true);
    expect((screen.getByLabelText("Prompt mode") as HTMLSelectElement).disabled).toBe(true);

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Retry prompt" })); });
    await vi.waitFor(() => expect(bodies).toHaveLength(2));
    expect(bodies[1]).toEqual(bodies[0]);
    expect(getRandomValues).toHaveBeenCalledOnce();
    await vi.waitFor(() => expect((screen.getByLabelText("Prompt") as HTMLTextAreaElement).value).toBe(""));
  });

  it.each([
    ["validation rejection", 400, { code: "invalid_request", message: "prompt is invalid" }],
    ["durable delivery rejection", 502, { code: "cli_send_failed", message: "prompt was not delivered" }],
  ])("releases the composer after a definitive %s", async (_case, status, rejection) => {
    const bodies: Array<Record<string, unknown>> = [];
    let uuidSeed = 0;
    const getRandomValues = vi.fn((bytes: Uint8Array) => {
      uuidSeed += 1;
      bytes.fill(uuidSeed);
      return bytes;
    });
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("crypto", { getRandomValues });
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      const bootstrapped = bootstrap(url.pathname, session());
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      if (incoming.method === "POST" && url.pathname.endsWith("/prompts")) {
        bodies.push(await incoming.clone().json() as Record<string, unknown>);
        if (bodies.length === 1) return json(rejection, status);
        return json({ id: turnId, sessionId, status: "queued", createdAt: timestamp }, 202);
      }
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    await renderReadySession();
    const composer = screen.getByLabelText("Prompt");
    const mode = screen.getByLabelText("Prompt mode");
    fireEvent.change(composer, { target: { value: "rejected prompt" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Send prompt" })); });

    expect((await screen.findByRole("alert")).textContent).toContain(rejection.code);
    expect((composer as HTMLTextAreaElement).disabled).toBe(false);
    expect((mode as HTMLSelectElement).disabled).toBe(false);
    fireEvent.change(composer, { target: { value: "corrected prompt" } });
    fireEvent.change(mode, { target: { value: "steer" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Send prompt" })); });

    await vi.waitFor(() => expect(bodies).toHaveLength(2));
    expect(bodies[1]?.["idempotencyKey"]).not.toBe(bodies[0]?.["idempotencyKey"]);
    expect(bodies[1]).toMatchObject({ text: "corrected prompt", mode: "steer" });
    expect(getRandomValues).toHaveBeenCalledTimes(2);
  });

  it("does not let a stale request reload hide a newer SSE request", async () => {
    let requestGets = 0;
    let finishReload: (response: Response) => void = () => undefined;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      if (incoming.method === "GET" && url.pathname.endsWith("/requests")) {
        requestGets += 1;
        if (requestGets === 1) return json({ data: [request()] });
        return await new Promise<Response>((resolve) => { finishReload = resolve; });
      }
      const bootstrapped = bootstrap(url.pathname, session());
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      if (incoming.method === "POST" && url.pathname.endsWith("/resolve")) {
        return json({ ...request(), status: "approved", resolvedAt: timestamp });
      }
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    await renderReadySession();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Approve" })); });
    await vi.waitFor(() => expect(requestGets).toBe(2));
    const stream = FakeEventSource.instances[0] as FakeEventSource;
    act(() => {
      stream.emit({ seq: 1, sessionId, kind: "requestResolved", occurredAt: timestamp, payload: { ...request(), status: "approved", resolvedAt: timestamp } });
      stream.emit({ seq: 2, sessionId, kind: "requestOpened", occurredAt: timestamp, payload: request(secondRequestId, "Deploy now?") });
    });
    await screen.findByText("Deploy now?");
    await act(async () => { finishReload(json({ data: [] })); });
    expect(screen.getByText("Deploy now?")).toBeTruthy();
    expect(screen.queryByText("Run tests?")).toBeNull();
  });

  it("catches abort failure and refuses an older model response over a newer SSE version", async () => {
    const running = session({ status: "running" });
    let patchCalls = 0;
    let finishModel: (response: Response) => void = () => undefined;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("crypto", {
      getRandomValues: (bytes: Uint8Array) => {
        bytes.forEach((_byte, index) => { bytes[index] = index; });
        return bytes;
      },
    });
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      const bootstrapped = bootstrap(url.pathname, running);
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      if (incoming.method === "GET" && url.pathname.endsWith("/active-turn")) return json(runningTurn());
      if (incoming.method === "POST" && url.pathname.endsWith("/abort")) throw new TypeError("abort response lost");
      if (incoming.method === "PATCH" && url.pathname === `/v1/sessions/${sessionId}`) {
        patchCalls += 1;
        return await new Promise<Response>((resolve) => { finishModel = resolve; });
      }
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    await renderReadySession(running);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Stop" })); });
    expect((await screen.findByRole("alert")).textContent).toContain("Stopping the turn did not receive a response and may have succeeded");
    expect((screen.getByRole("button", { name: "Stop" }) as HTMLButtonElement).disabled).toBe(false);

    const picker = screen.getByLabelText("Model");
    fireEvent.change(picker, { target: { value: "model-b" } });
    fireEvent.change(picker, { target: { value: "model-c" } });
    await vi.waitFor(() => expect(patchCalls).toBe(1));
    const stream = FakeEventSource.instances[0] as FakeEventSource;
    act(() => {
      stream.emit({ seq: 3, sessionId, kind: "sessionUpdated", occurredAt: timestamp, payload: session({ model: "model-c", status: "running" }) });
    });
    await act(async () => { finishModel(json(session({ model: "model-b", status: "running" }))); });
    await vi.waitFor(() => expect((picker as HTMLSelectElement).value).toBe("model-c"));
  });

  it("clears a definitive stale abort target before targeting its successor", async () => {
    const running = session({ status: "running" });
    const successorId = "66666666-6666-4666-8666-666666666666";
    const bodies: Array<Record<string, unknown>> = [];
    let activeGets = 0;
    let uuidSeed = 0;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("crypto", {
      getRandomValues: (bytes: Uint8Array) => {
        uuidSeed += 1;
        bytes.fill(uuidSeed);
        return bytes;
      },
    });
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      const bootstrapped = bootstrap(url.pathname, running);
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      if (incoming.method === "GET" && url.pathname.endsWith("/active-turn")) {
        activeGets += 1;
        return json(runningTurn(activeGets === 1 ? turnId : successorId));
      }
      if (incoming.method === "POST" && url.pathname.endsWith("/abort")) {
        bodies.push(await incoming.clone().json() as Record<string, unknown>);
        if (bodies.length === 1) {
          return json({ code: "abort_target_changed", message: "turn A completed before abort" }, 409);
        }
        return json({ ...runningTurn(successorId), status: "aborted", completedAt: timestamp });
      }
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    await renderReadySession(running);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Stop" })); });
    expect((await screen.findByRole("alert")).textContent).toContain("abort_target_changed");
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Stop" })); });

    expect(activeGets).toBe(2);
    expect(bodies).toHaveLength(2);
    expect(bodies[0]?.["turnId"]).toBe(turnId);
    expect(bodies[1]?.["turnId"]).toBe(successorId);
    expect(bodies[1]?.["idempotencyKey"]).not.toBe(bodies[0]?.["idempotencyKey"]);
  });

  it("clears an ambiguous abort only when its matching completion arrives", async () => {
    const running = session({ status: "running" });
    const successorId = "66666666-6666-4666-8666-666666666666";
    const bodies: Array<Record<string, unknown>> = [];
    let activeGets = 0;
    let uuidSeed = 0;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("crypto", {
      getRandomValues: (bytes: Uint8Array) => {
        uuidSeed += 1;
        bytes.fill(uuidSeed);
        return bytes;
      },
    });
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      const bootstrapped = bootstrap(url.pathname, running);
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      if (incoming.method === "GET" && url.pathname.endsWith("/active-turn")) {
        activeGets += 1;
        return json(runningTurn(activeGets === 1 ? turnId : successorId));
      }
      if (incoming.method === "POST" && url.pathname.endsWith("/abort")) {
        bodies.push(await incoming.clone().json() as Record<string, unknown>);
        if (bodies.length === 1) throw new TypeError("abort response lost");
        return json({ ...runningTurn(successorId), status: "aborted", completedAt: timestamp });
      }
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    await renderReadySession(running);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Stop" })); });
    const stream = FakeEventSource.instances[0] as FakeEventSource;
    act(() => {
      stream.emit({
        seq: 7,
        sessionId,
        kind: "turnCompleted",
        occurredAt: timestamp,
        payload: { ...runningTurn(turnId), status: "aborted", completedAt: timestamp },
      });
    });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Stop" })); });

    expect(activeGets).toBe(2);
    expect(bodies[1]?.["turnId"]).toBe(successorId);
    expect(bodies[1]?.["idempotencyKey"]).not.toBe(bodies[0]?.["idempotencyKey"]);
  });

  it("does not let an unrelated prompt error discard an ambiguous abort attempt", async () => {
    const running = session({ status: "running" });
    const abortBodies: Array<Record<string, unknown>> = [];
    let activeGets = 0;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("crypto", {
      getRandomValues: (bytes: Uint8Array) => {
        bytes.forEach((_byte, index) => { bytes[index] = index; });
        return bytes;
      },
    });
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      const bootstrapped = bootstrap(url.pathname, running);
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      if (incoming.method === "GET" && url.pathname.endsWith("/active-turn")) {
        activeGets += 1;
        return json(runningTurn());
      }
      if (incoming.method === "POST" && url.pathname.endsWith("/abort")) {
        abortBodies.push(await incoming.clone().json() as Record<string, unknown>);
        throw new TypeError("abort response lost");
      }
      if (incoming.method === "POST" && url.pathname.endsWith("/prompts")) {
        return json({ code: "abort_target_changed", message: "unrelated prompt error" }, 409);
      }
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    await renderReadySession(running);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Stop" })); });
    fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: "keep the abort receipt" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Send prompt" })); });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Stop" })); });

    expect(activeGets).toBe(1);
    expect(abortBodies).toHaveLength(2);
    expect(abortBodies[1]).toEqual(abortBodies[0]);
  });

  it("aborts paginated transcript reads when the page is abandoned", async () => {
    let transcriptGets = 0;
    let secondRequestAborted = false;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("fetch", async (incoming: Request) => {
      const url = new URL(incoming.url);
      if (incoming.method === "GET" && url.pathname.endsWith("/transcript")) {
        transcriptGets += 1;
        if (transcriptGets === 1) return json({ data: [], nextAfterSeq: 1 });
        return await new Promise<Response>((_resolve, reject) => {
          incoming.signal.addEventListener("abort", () => {
            secondRequestAborted = true;
            reject(new DOMException("aborted", "AbortError"));
          }, { once: true });
        });
      }
      const bootstrapped = bootstrap(url.pathname, session());
      if (incoming.method === "GET" && bootstrapped !== null) return bootstrapped;
      throw new Error(`unexpected request: ${incoming.method} ${url.pathname}`);
    });

    const view = renderSession();
    await vi.waitFor(() => expect(transcriptGets).toBe(2));
    view.unmount();
    expect(secondRequestAborted).toBe(true);
  });
});
