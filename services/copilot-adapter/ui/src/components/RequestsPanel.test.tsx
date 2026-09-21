import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionRequest } from "../api/client";
import { renderWithMantine as render } from "../test-utils";
import { RequestsPanel } from "./RequestsPanel";

const sessionId = "11111111-1111-4111-8111-111111111111";

function pending(overrides: Partial<SessionRequest>): SessionRequest {
  return {
    id: "22222222-2222-4222-8222-222222222222",
    sessionId,
    kind: "permission",
    status: "pending",
    prompt: "Run `rm -rf build`?",
    createdAt: "2026-09-04T00:00:00Z",
    ...overrides,
  };
}

// The panel talks to the adapter through the generated client, whose transport
// is window.fetch; a stub there asserts the exact URL and body the contract asks
// for without mocking the client itself.
let calls: Array<{ url: string; body: unknown }>;

beforeEach(() => {
  calls = [];
  // openapi-fetch hands its transport one fully built Request, so the assertion
  // reads the URL and body off that rather than off an init bag.
  vi.stubGlobal("fetch", async (request: Request) => {
    calls.push({ url: request.url, body: await request.clone().json() });
    return new Response(JSON.stringify(pending({ status: "approved" })), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  });
});

afterEach(() => vi.unstubAllGlobals());

describe("RequestsPanel", () => {
  it("renders nothing when no request is pending", () => {
    render(
      <RequestsPanel
        sessionId={sessionId}
        requests={[pending({ status: "approved" })]}
        onResolved={() => undefined}
      />,
    );
    expect(screen.queryByRole("region", { name: "Pending requests" })).toBeNull();
  });

  it("approves a permission request with a decision-only body", async () => {
    const onResolved = vi.fn();
    render(
      <RequestsPanel sessionId={sessionId} requests={[pending({})] } onResolved={onResolved} />,
    );

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Approve" })); });
    await vi.waitFor(() => expect(calls).toHaveLength(1));

    expect(calls[0]?.url).toContain(
      `/v1/sessions/${sessionId}/requests/22222222-2222-4222-8222-222222222222/resolve`,
    );
    expect(calls[0]?.body).toEqual({ decision: "approve" });
    await vi.waitFor(() => expect(onResolved).toHaveBeenCalledOnce());
  });

  it("denies with a decision-only body", async () => {
    render(
      <RequestsPanel
        sessionId={sessionId}
        requests={[pending({})]}
        onResolved={() => undefined}
      />,
    );

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Deny" })); });
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]?.body).toEqual({ decision: "deny" });
  });

  it("allows only one decision in flight and releases the controls after a rejected transport", async () => {
    let rejectRequest: (cause: unknown) => void = () => undefined;
    vi.stubGlobal("fetch", async (request: Request) => {
      calls.push({ url: request.url, body: await request.clone().json() });
      return await new Promise<Response>((_resolve, reject) => { rejectRequest = reject; });
    });
    render(
      <RequestsPanel sessionId={sessionId} requests={[pending({})]} onResolved={() => undefined} />,
    );

    const approve = screen.getByRole("button", { name: "Approve" });
    const deny = screen.getByRole("button", { name: "Deny" });
    act(() => {
      fireEvent.click(approve);
      fireEvent.click(deny);
    });
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(true);

    await act(async () => { rejectRequest(new TypeError("connection reset")); });
    expect((await screen.findByRole("alert")).textContent).toContain(
      "Resolving the request did not receive a response and may have succeeded. Refresh before retrying. connection reset",
    );
    expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(false);
    expect((screen.getByRole("button", { name: "Deny" }) as HTMLButtonElement).disabled).toBe(false);
  });
});
