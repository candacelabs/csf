import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ChatSchedule } from "../api/client";
import { renderWithMantine as render } from "../test-utils";
import { SchedulesPane } from "./SchedulesPane";

const sessionId = "11111111-1111-4111-8111-111111111111";
const schedule: ChatSchedule = {
  id: "22222222-2222-4222-8222-222222222222",
  sessionId,
  displayName: "Morning review",
  prompt: "Review open work",
  cronExpression: "0 9 * * 1-5",
  timezone: "America/Chicago",
  status: "active",
  nextRunAt: "2026-09-06T14:00:00Z",
  createdAt: "2026-09-05T00:00:00Z",
  updatedAt: "2026-09-05T00:00:00Z",
};

afterEach(() => vi.unstubAllGlobals());

describe("SchedulesPane", () => {
  it("surfaces a rejected load and recovers through the refresh action", async () => {
    let attempts = 0;
    vi.stubGlobal("fetch", async () => {
      attempts += 1;
      if (attempts === 1) throw new TypeError("offline");
      return new Response(JSON.stringify({ data: [schedule] }), { status: 200, headers: { "content-type": "application/json" } });
    });

    render(<SchedulesPane sessionId={sessionId} />);
    expect((await screen.findByRole("alert")).textContent).toContain("Schedules could not be read. offline");
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Refresh schedules" })); });
    await screen.findByText("Morning review");
    expect(attempts).toBe(2);
  });

  it("creates a recurring prompt through the schedule API", async () => {
    const calls: Array<{ method: string; body?: unknown }> = [];
    let finishSave: ((response: Response) => void) | undefined;
    let saved = false;
    vi.stubGlobal("fetch", async (request: Request) => {
      if (request.method === "POST") {
        calls.push({ method: request.method, body: await request.clone().json() });
        return await new Promise<Response>((resolve) => { finishSave = resolve; });
      }
      return new Response(JSON.stringify({ data: saved ? [schedule] : [] }), { status: 200, headers: { "content-type": "application/json" } });
    });
    render(<SchedulesPane sessionId={sessionId} />);
    await screen.findByText("Nothing scheduled");
    fireEvent.click(screen.getByRole("button", { name: "Create a schedule" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Morning review" } });
    fireEvent.change(screen.getByLabelText("Time zone"), { target: { value: "America/Chicago" } });
    fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: "Review open work" } });
    const saveButton = screen.getByRole("button", { name: "Save schedule" });
    act(() => {
      fireEvent.click(saveButton);
      fireEvent.click(saveButton);
    });

    await vi.waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]?.body).toEqual({
      idempotencyKey: expect.stringMatching(/^[0-9a-f-]{36}$/),
      sessionId,
      displayName: "Morning review",
      prompt: "Review open work",
      cronExpression: "0 9 * * 1-5",
      timezone: "America/Chicago",
    });
    saved = true;
    await act(async () => {
      finishSave?.(new Response(JSON.stringify(schedule), { status: 201, headers: { "content-type": "application/json" } }));
    });
    await screen.findByText("Morning review");
  });

  it("pauses and deletes an existing schedule with explicit confirmation", async () => {
    const methods: string[] = [];
    let finishStatus: ((response: Response) => void) | undefined;
    let current: ChatSchedule | null = schedule;
    vi.stubGlobal("fetch", async (request: Request) => {
      methods.push(request.method);
      if (request.method === "PATCH") {
        return await new Promise<Response>((resolve) => { finishStatus = resolve; });
      }
      if (request.method === "DELETE") {
        current = null;
        return new Response(null, { status: 204 });
      }
      return new Response(JSON.stringify({ data: current === null ? [] : [current] }), { status: 200, headers: { "content-type": "application/json" } });
    });
    render(<SchedulesPane sessionId={sessionId} />);
    await screen.findByText("Morning review");
    const pauseButton = screen.getByRole("button", { name: "Pause" });
    act(() => {
      fireEvent.click(pauseButton);
      fireEvent.click(pauseButton);
    });
    await vi.waitFor(() => expect(methods.filter((method) => method === "PATCH")).toHaveLength(1));
    current = { ...schedule, status: "paused" };
    await act(async () => {
      finishStatus?.(new Response(JSON.stringify(current), { status: 200, headers: { "content-type": "application/json" } }));
    });
    await screen.findByRole("button", { name: "Resume" });
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(methods).not.toContain("DELETE");
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Confirm delete" })); });
    await screen.findByText("Nothing scheduled");
    expect(methods).toContain("DELETE");
  });

  it("retries a lost create response with the same immutable request identity", async () => {
    let current: ChatSchedule | null = null;
    let postAttempts = 0;
    let patchAttempts = 0;
    const postBodies: unknown[] = [];
    vi.stubGlobal("fetch", async (request: Request) => {
      if (request.method === "POST") {
        postAttempts += 1;
        postBodies.push(await request.clone().json());
        current = schedule;
        if (postAttempts === 1) throw new TypeError("create response lost");
        return new Response(JSON.stringify(schedule), { status: 201, headers: { "content-type": "application/json" } });
      }
      if (request.method === "PATCH") {
        patchAttempts += 1;
        if (patchAttempts === 1) throw new TypeError("edit response lost");
        current = { ...schedule, displayName: "Updated review" };
        return new Response(JSON.stringify(current), { status: 200, headers: { "content-type": "application/json" } });
      }
      return new Response(JSON.stringify({ data: current === null ? [] : [current] }), { status: 200, headers: { "content-type": "application/json" } });
    });

    render(<SchedulesPane sessionId={sessionId} />);
    await screen.findByText("Nothing scheduled");
    fireEvent.click(screen.getByRole("button", { name: "Create a schedule" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: schedule.displayName } });
    fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: schedule.prompt } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Save schedule" })); });
    expect((await screen.findByRole("alert")).textContent).toContain("The draft is locked; retrying uses the same request identity. create response lost");
    expect((screen.getByLabelText("Name") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Retry exact create" }) as HTMLButtonElement).disabled).toBe(false);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Refresh schedules" })); });
    await screen.findByText("Morning review");
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Retry exact create" })); });
    await screen.findByText("Morning review");
    expect(postBodies).toHaveLength(2);
    expect(postBodies[1]).toEqual(postBodies[0]);

    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Updated review" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Save schedule" })); });
    expect((await screen.findByRole("alert")).textContent).toContain("edit response lost");
    expect((screen.getByRole("button", { name: "Save schedule" }) as HTMLButtonElement).disabled).toBe(false);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Save schedule" })); });
    await screen.findByText("Updated review");
    expect(postAttempts).toBe(2);
    expect(patchAttempts).toBe(2);
  });

  it("keeps an ambiguous create locked and exactly retryable when refresh also fails", async () => {
    let getAttempts = 0;
    const postBodies: unknown[] = [];
    vi.stubGlobal("fetch", async (request: Request) => {
      if (request.method === "POST") {
        postBodies.push(await request.clone().json());
        if (postBodies.length === 1) throw new TypeError("create response lost");
        return new Response(JSON.stringify(schedule), { status: 201, headers: { "content-type": "application/json" } });
      }
      getAttempts += 1;
      if (getAttempts === 2) throw new TypeError("refresh unavailable");
      return new Response(JSON.stringify({ data: getAttempts > 2 ? [schedule] : [] }), { status: 200, headers: { "content-type": "application/json" } });
    });

    render(<SchedulesPane sessionId={sessionId} />);
    await screen.findByText("Nothing scheduled");
    fireEvent.click(screen.getByRole("button", { name: "Create a schedule" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: schedule.displayName } });
    fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: schedule.prompt } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Save schedule" })); });
    await screen.findByRole("button", { name: "Retry exact create" });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Refresh schedules" })); });
    expect((await screen.findByRole("alert")).textContent).toContain("refresh unavailable");
    expect((screen.getByLabelText("Name") as HTMLInputElement).disabled).toBe(true);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Retry exact create" })); });
    await screen.findByText("Morning review");
    expect(postBodies[1]).toEqual(postBodies[0]);
  });

  it("treats a create 500 as ambiguous and exact-retries its locked body", async () => {
    const postBodies: unknown[] = [];
    let created = false;
    vi.stubGlobal("fetch", async (request: Request) => {
      if (request.method === "POST") {
        postBodies.push(await request.clone().json());
        if (postBodies.length === 1) {
          return new Response(JSON.stringify({ code: "store_error", message: "reconciliation unavailable" }), {
            status: 500,
            headers: { "content-type": "application/json" },
          });
        }
        created = true;
        return new Response(JSON.stringify(schedule), { status: 201, headers: { "content-type": "application/json" } });
      }
      return new Response(JSON.stringify({ data: created ? [schedule] : [] }), { status: 200, headers: { "content-type": "application/json" } });
    });

    render(<SchedulesPane sessionId={sessionId} />);
    await screen.findByText("Nothing scheduled");
    fireEvent.click(screen.getByRole("button", { name: "Create a schedule" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: schedule.displayName } });
    fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: schedule.prompt } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Save schedule" })); });
    expect((await screen.findByRole("alert")).textContent).toContain("reconciliation unavailable");
    expect((screen.getByLabelText("Prompt") as HTMLTextAreaElement).disabled).toBe(true);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Retry exact create" })); });
    await screen.findByText("Morning review");
    expect(postBodies[1]).toEqual(postBodies[0]);
  });

  it("releases status and delete controls after rejected transports", async () => {
    let current: ChatSchedule | null = schedule;
    let patchAttempts = 0;
    let deleteAttempts = 0;
    vi.stubGlobal("fetch", async (request: Request) => {
      if (request.method === "PATCH") {
        patchAttempts += 1;
        if (patchAttempts === 1) throw new TypeError("status response lost");
        current = { ...schedule, status: "paused" };
        return new Response(JSON.stringify(current), { status: 200, headers: { "content-type": "application/json" } });
      }
      if (request.method === "DELETE") {
        deleteAttempts += 1;
        if (deleteAttempts === 1) throw new TypeError("delete response lost");
        current = null;
        return new Response(null, { status: 204 });
      }
      return new Response(JSON.stringify({ data: current === null ? [] : [current] }), { status: 200, headers: { "content-type": "application/json" } });
    });

    render(<SchedulesPane sessionId={sessionId} />);
    await screen.findByText("Morning review");
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Pause" })); });
    expect((await screen.findByRole("alert")).textContent).toContain("status response lost");
    expect((screen.getByRole("button", { name: "Pause" }) as HTMLButtonElement).disabled).toBe(false);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Pause" })); });
    await screen.findByRole("button", { name: "Resume" });

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Confirm delete" })); });
    expect((await screen.findByRole("alert")).textContent).toContain("delete response lost");
    expect((screen.getByRole("button", { name: "Confirm delete" }) as HTMLButtonElement).disabled).toBe(false);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Confirm delete" })); });
    await screen.findByText("Nothing scheduled");
    expect(patchAttempts).toBe(2);
    expect(deleteAttempts).toBe(2);
  });
});
