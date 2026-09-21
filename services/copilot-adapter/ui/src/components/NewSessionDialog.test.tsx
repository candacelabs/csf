import { useState } from "react";
import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Model, Repository, Session, Worktree } from "../api/client";
import { renderWithMantine as render } from "../test-utils";
import { NewSessionDialog } from "./NewSessionDialog";

const repository: Repository = { id: "candace", displayName: "Candace", root: "/srv/candace", defaultRef: "main" };
const model: Model = { id: "gpt-5.6", displayName: "GPT-5.6", capabilities: ["chat", "tools"] };
const worktree: Worktree = {
  id: "22222222-2222-4222-8222-222222222222",
  repositoryId: repository.id,
  repositoryRoot: repository.root,
  path: "/srv/worktrees/review",
  branch: "review",
  headSha: "abc123",
  baseRef: "main",
  managed: true,
  clean: true,
  state: "active",
  sessionCount: 1,
  createdAt: "2026-09-05T00:00:00Z",
  updatedAt: "2026-09-05T00:00:00Z",
};
const created: Session = {
  id: "11111111-1111-4111-8111-111111111111",
  displayName: "Review",
  model: model.id,
  worktreeId: worktree.id,
  workingDirectory: worktree.path,
  status: "starting",
  createdAt: "2026-09-05T00:00:00Z",
  updatedAt: "2026-09-05T00:00:00Z",
  turnCount: 0,
};
const clientKey = "00010203-0405-4607-8809-0a0b0c0d0e0f";

function stubClientUUID() {
  const getRandomValues = vi.fn((bytes: Uint8Array) => {
    bytes.forEach((_byte, index) => { bytes[index] = index; });
    return bytes;
  });
  vi.stubGlobal("crypto", { getRandomValues });
  return getRandomValues;
}

afterEach(() => vi.unstubAllGlobals());

describe("NewSessionDialog", () => {
  it("requires an explicit model choice when the catalog has several options", () => {
    const second: Model = { id: "another-model", displayName: "Another model", capabilities: ["chat"] };
    const props = { repositories: [repository], worktrees: [worktree], onClose: () => undefined, onCreated: () => undefined };
    const { rerender } = render(<NewSessionDialog {...props} models={[model, second]} />);
    const picker = screen.getByLabelText("Model") as HTMLSelectElement;
    expect(picker.value).toBe("");
    expect((screen.getByRole("button", { name: "Start task" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(picker, { target: { value: second.id } });
    rerender(<NewSessionDialog {...props} models={[second, model]} />);
    expect(picker.value).toBe(second.id);
    expect((screen.getByRole("button", { name: "Start task" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("does not fall back to arbitrary model IDs when the catalog is empty", () => {
    render(<NewSessionDialog repositories={[repository]} worktrees={[worktree]} models={[]} onClose={() => undefined} onCreated={() => undefined} />);
    expect((screen.getByLabelText("Model") as HTMLSelectElement).disabled).toBe(true);
    expect(screen.queryByPlaceholderText("Model ID")).toBeNull();
    expect((screen.getByRole("button", { name: "Start task" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("creates an isolated worktree by default", async () => {
    stubClientUUID();
    let body: unknown;
    vi.stubGlobal("fetch", async (request: Request) => {
      body = await request.clone().json();
      return new Response(JSON.stringify(created), { status: 201, headers: { "content-type": "application/json" } });
    });
    const onCreated = vi.fn();
    render(<NewSessionDialog repositories={[repository]} worktrees={[worktree]} models={[model]} onClose={() => undefined} onCreated={onCreated} />);

    expect((screen.getByRole("radio", { name: /New isolated worktree/ }) as HTMLInputElement).checked).toBe(true);
    fireEvent.change(screen.getByLabelText("Task name"), { target: { value: "Review" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Start task" })); });
    await vi.waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    expect(body).toEqual({ idempotencyKey: clientKey, repositoryId: "candace", model: "gpt-5.6", worktreeMode: "newWorktree", displayName: "Review" });
  });

  it("addresses a selected managed worktree by ID, never by path", async () => {
    stubClientUUID();
    let body: Record<string, unknown> = {};
    vi.stubGlobal("fetch", async (request: Request) => {
      body = await request.clone().json() as Record<string, unknown>;
      return new Response(JSON.stringify(created), { status: 201, headers: { "content-type": "application/json" } });
    });
    render(<NewSessionDialog repositories={[repository]} worktrees={[worktree]} models={[model]} onClose={() => undefined} onCreated={() => undefined} />);
    fireEvent.click(screen.getByRole("radio", { name: /Existing managed worktree/ }));
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Start task" })); });
    await vi.waitFor(() => expect(body["worktreeMode"]).toBe("reuseExistingWorktree"));
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "Start task" }).hasAttribute("disabled")).toBe(false));

    expect(body["worktreeId"]).toBe(worktree.id);
    expect(JSON.stringify(body)).not.toContain(worktree.path);
  });

  it("reports an ambiguous transport failure and permits a retry", async () => {
    const getRandomValues = stubClientUUID();
    const bodies: Array<Record<string, unknown>> = [];
    vi.stubGlobal("fetch", async (request: Request) => {
      bodies.push(await request.clone().json() as Record<string, unknown>);
      if (bodies.length === 1) throw new TypeError("connection lost after sending");
      return new Response(JSON.stringify(created), { status: 201, headers: { "content-type": "application/json" } });
    });
    const onCreated = vi.fn();
    render(<NewSessionDialog repositories={[repository]} worktrees={[worktree]} models={[model]} onClose={() => undefined} onCreated={onCreated} />);

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Start task" })); });
    expect((await screen.findByRole("alert")).textContent).toContain(
      "Task creation did not receive a response and may have succeeded. Retry will reuse the same idempotency key.",
    );
    expect((screen.getByRole("button", { name: "Start task" }) as HTMLButtonElement).disabled).toBe(false);
    expect(onCreated).not.toHaveBeenCalled();

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Start task" })); });
    await vi.waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    expect(bodies).toHaveLength(2);
    expect(bodies[1]?.["idempotencyKey"]).toBe(bodies[0]?.["idempotencyKey"]);
    expect(getRandomValues).toHaveBeenCalledOnce();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("uses a new creation key when the request body changes", async () => {
    let seed = 0;
    const getRandomValues = vi.fn((bytes: Uint8Array) => {
      seed += 1;
      bytes.fill(seed);
      return bytes;
    });
    const bodies: Array<Record<string, unknown>> = [];
    vi.stubGlobal("crypto", { getRandomValues });
    vi.stubGlobal("fetch", async (request: Request) => {
      bodies.push(await request.clone().json() as Record<string, unknown>);
      throw new TypeError("response lost");
    });
    render(<NewSessionDialog repositories={[repository]} worktrees={[worktree]} models={[model]} onClose={() => undefined} onCreated={() => undefined} />);

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Start task" })); });
    fireEvent.change(screen.getByLabelText("Task name"), { target: { value: "A different task" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Start task" })); });

    expect(bodies).toHaveLength(2);
    expect(bodies[1]?.["idempotencyKey"]).not.toBe(bodies[0]?.["idempotencyKey"]);
    expect(getRandomValues).toHaveBeenCalledTimes(2);
  });

  it("distinguishes local request preparation failure from an ambiguous send", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("crypto", { getRandomValues: () => { throw new Error("secure randomness unavailable"); } });
    vi.stubGlobal("fetch", fetch);
    render(<NewSessionDialog repositories={[repository]} worktrees={[worktree]} models={[model]} onClose={() => undefined} onCreated={() => undefined} />);

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Start task" })); });

    expect((await screen.findByRole("alert")).textContent).toContain("The task request could not be prepared. secure randomness unavailable");
    expect(fetch).not.toHaveBeenCalled();
  });

  it("cannot be dismissed while creation is in flight", async () => {
    stubClientUUID();
    let finishCreation: ((response: Response) => void) | undefined;
    const fetch = vi.fn(() => new Promise<Response>((resolve) => { finishCreation = resolve; }));
    vi.stubGlobal("fetch", fetch);
    const onClose = vi.fn();
    const onCreated = vi.fn();
    render(<NewSessionDialog repositories={[repository]} worktrees={[worktree]} models={[model]} onClose={onClose} onCreated={onCreated} />);

    fireEvent.click(screen.getByRole("button", { name: "Start task" }));
    fireEvent.click(screen.getByRole("button", { name: "Starting…" }));
    await vi.waitFor(() => expect(finishCreation).toBeDefined());
    expect(fetch).toHaveBeenCalledOnce();
    expect((screen.getByRole("button", { name: "Close" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    fireEvent.mouseDown(screen.getByRole("dialog").parentElement as HTMLElement);
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => {
      finishCreation?.(new Response(JSON.stringify(created), {
        status: 201,
        headers: { "content-type": "application/json" },
      }));
    });
    await vi.waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    expect(onClose).not.toHaveBeenCalled();
  });

  it("traps focus and restores the control that opened it", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>Open dialog</button>
          {open && <NewSessionDialog repositories={[repository]} worktrees={[worktree]} models={[model]} onClose={() => setOpen(false)} onCreated={() => undefined} />}
        </>
      );
    }

    render(<Harness />);
    const opener = screen.getByRole("button", { name: "Open dialog" });
    opener.focus();
    fireEvent.click(opener);
    await vi.waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Task name")));

    const first = screen.getByRole("button", { name: "Close" });
    const last = screen.getByRole("button", { name: "Start task" });
    last.focus();
    fireEvent.keyDown(last, { key: "Tab" });
    expect(document.activeElement).toBe(first);
    fireEvent.keyDown(first, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last);

    fireEvent.click(first);
    await vi.waitFor(() => expect(document.activeElement).toBe(opener));
  });
});
