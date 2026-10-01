import { act, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WorkbenchThemeOverride } from "./WorkbenchThemeOverride";

function themeResponse(customCss: string | undefined): Response {
  return Response.json({ theme: customCss === undefined ? {} : { customCss } });
}

function appliedCss(): string | null {
  return document.querySelector<HTMLStyleElement>("style[data-workbench-theme]")?.textContent ?? null;
}

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, "visibilityState", { configurable: true, value: state });
}

afterEach(() => {
  window.history.replaceState({}, "", "/");
  setVisibility("visible");
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("WorkbenchThemeOverride", () => {
  it("loads and applies the shared CSS after mounting", async () => {
    const fetch = vi.fn(async (request: Request) => {
      expect(new URL(request.url).pathname).toBe("/api/workbench/theme/get");
      expect(request.method).toBe("POST");
      expect(await request.json()).toEqual({});
      return themeResponse("body { background: lavender; }");
    });
    vi.stubGlobal("fetch", fetch);

    render(<WorkbenchThemeOverride />);

    await waitFor(() => expect(appliedCss()).toBe("body { background: lavender; }"));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("refreshes shared CSS on the five-second timer and when visibility or focus returns", async () => {
    const css = ["body { color: red; }", "body { color: green; }", "body { color: blue; }"];
    const fetch = vi.fn(async () => themeResponse(css.shift()));
    const timer = vi.spyOn(window, "setInterval");
    vi.stubGlobal("fetch", fetch);
    render(<WorkbenchThemeOverride />);

    await waitFor(() => expect(appliedCss()).toBe("body { color: red; }"));
    expect(timer).toHaveBeenCalledWith(expect.any(Function), 5_000);
    const refresh = timer.mock.calls[0][0] as () => void;

    setVisibility("hidden");
    act(() => refresh());
    expect(fetch).toHaveBeenCalledTimes(1);

    setVisibility("visible");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await waitFor(() => expect(appliedCss()).toBe("body { color: green; }"));

    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(appliedCss()).toBe("body { color: blue; }"));
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("removes the override when the server returns an empty theme", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(themeResponse("body { color: purple; }"))
      .mockResolvedValueOnce(themeResponse(undefined));
    const timer = vi.spyOn(window, "setInterval");
    vi.stubGlobal("fetch", fetch);
    render(<WorkbenchThemeOverride />);

    await waitFor(() => expect(appliedCss()).toBe("body { color: purple; }"));
    act(() => (timer.mock.calls[0][0] as () => void)());
    await waitFor(() => expect(appliedCss()).toBeNull());
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("keeps the last successful CSS after a failed refresh", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(themeResponse("body { color: teal; }"))
      .mockResolvedValueOnce(Response.json({ message: "offline" }, { status: 503 }));
    const timer = vi.spyOn(window, "setInterval");
    vi.stubGlobal("fetch", fetch);
    render(<WorkbenchThemeOverride />);

    await waitFor(() => expect(appliedCss()).toBe("body { color: teal; }"));
    act(() => (timer.mock.calls[0][0] as () => void)());
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(appliedCss()).toBe("body { color: teal; }");
  });

  it("avoids overlapping reads and cleans up the timer, listeners, and fetch on unmount", async () => {
    let outgoingRequest: Request | undefined;
    const fetch = vi.fn((request: Request) => {
      outgoingRequest = request;
      return new Promise<Response>(() => {});
    });
    const setInterval = vi.spyOn(window, "setInterval");
    const clearInterval = vi.spyOn(window, "clearInterval");
    const removeWindowListener = vi.spyOn(window, "removeEventListener");
    const removeDocumentListener = vi.spyOn(document, "removeEventListener");
    vi.stubGlobal("fetch", fetch);
    const view = render(<WorkbenchThemeOverride />);

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    act(() => {
      window.dispatchEvent(new Event("focus"));
      document.dispatchEvent(new Event("visibilitychange"));
      (setInterval.mock.calls[0][0] as () => void)();
    });
    expect(fetch).toHaveBeenCalledTimes(1);

    view.unmount();
    expect(outgoingRequest?.signal.aborted).toBe(true);
    expect(clearInterval).toHaveBeenCalledWith(setInterval.mock.results[0].value);
    expect(removeWindowListener).toHaveBeenCalledWith("focus", expect.any(Function));
    expect(removeDocumentListener).toHaveBeenCalledWith("visibilitychange", expect.any(Function));
  });

  it("bypasses shared CSS for the default-theme recovery query", () => {
    window.history.replaceState({}, "", "/ui/?theme=default");
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);

    render(<WorkbenchThemeOverride />);

    expect(fetch).not.toHaveBeenCalled();
    expect(appliedCss()).toBeNull();
  });
});
