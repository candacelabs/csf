import { FitAddon } from "@xterm/addon-fit";
import { Terminal as XTerm } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, describeError } from "../api/client";
import type { Terminal as TerminalRecord, TerminalEvent } from "../api/client";
import { PaneLoading } from "./PaneLoading";
import { terminalInput } from "../terminalInput";

type TerminalTransition =
  | { kind: "refresh" }
  | { kind: "create" }
  | { kind: "stop"; terminalId: string };

export function TerminalPane({ worktreeId }: { worktreeId: string }) {
  const [terminals, setTerminals] = useState<TerminalRecord[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [transition, setTransition] = useState<TerminalTransition | null>(null);
  const [reconciliationRequired, setReconciliationRequired] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const transitionRef = useRef<symbol | null>(null);
  const reconciliationRequiredRef = useRef(true);
  const queuedRefreshRef = useRef(false);
  const terminalEventRevisionRef = useRef(0);
  const loadRef = useRef<(queueIfBusy?: boolean) => void>(() => undefined);

  const beginTransition = useCallback((next: TerminalTransition): symbol | null => {
    if (transitionRef.current !== null) return null;
    const token = Symbol(next.kind);
    transitionRef.current = token;
    setTransition(next);
    return token;
  }, []);

  const finishTransition = useCallback((token: symbol) => {
    if (transitionRef.current !== token) return;
    transitionRef.current = null;
    setTransition(null);
  }, []);

  const requireReconciliation = useCallback((required: boolean) => {
    reconciliationRequiredRef.current = required;
    setReconciliationRequired(required);
  }, []);

  const acceptTerminals = useCallback((next: TerminalRecord[]) => {
    setTerminals(next);
    setSelectedId((current) => next.some((terminal) => terminal.id === current)
      ? current
      : next.find((terminal) => terminal.status === "running")?.id ?? next[0]?.id ?? null);
  }, []);

  const load = useCallback(async (queueIfBusy = false) => {
    const token = beginTransition({ kind: "refresh" });
    if (token === null) {
      if (queueIfBusy) queuedRefreshRef.current = true;
      return;
    }
    const eventRevision = terminalEventRevisionRef.current;
    requireReconciliation(true);
    setLoading(true);
    try {
      const { data, error } = await api.GET("/v1/worktrees/{worktreeId}/terminals", {
        params: { path: { worktreeId } },
      });
      if (transitionRef.current !== token) return;
      if (error !== undefined || data === undefined) {
        setFailure(error === undefined ? "terminals could not be read" : describeError(error));
        return;
      }
      if (terminalEventRevisionRef.current !== eventRevision) {
        queuedRefreshRef.current = true;
        return;
      }
      acceptTerminals(data.data);
      setFailure(null);
      requireReconciliation(false);
    } catch (cause) {
      if (transitionRef.current === token) {
        setFailure(`Terminals could not be refreshed: ${describeError(cause)}. Retry when the connection is available.`);
      }
    } finally {
      if (transitionRef.current === token) setLoading(false);
      finishTransition(token);
      if (queuedRefreshRef.current) {
        queuedRefreshRef.current = false;
        queueMicrotask(() => loadRef.current(true));
      }
    }
  }, [acceptTerminals, beginTransition, finishTransition, requireReconciliation, worktreeId]);
  loadRef.current = load;

  const acceptTerminalEnd = useCallback((terminalId: string, status: "exited" | "failed", exitCode?: number) => {
    terminalEventRevisionRef.current += 1;
    setTerminals((current) => current.map((terminal) => terminal.id === terminalId
      ? { ...terminal, status, ...(exitCode === undefined ? {} : { exitCode }) }
      : terminal));
    loadRef.current(true);
  }, []);

  useEffect(() => {
    void load();
    return () => {
      transitionRef.current = null;
      reconciliationRequiredRef.current = true;
      queuedRefreshRef.current = false;
    };
  }, [load]);

  async function create() {
    if (reconciliationRequiredRef.current) return;
    const token = beginTransition({ kind: "create" });
    if (token === null) return;
    try {
      const { data, error } = await api.POST("/v1/worktrees/{worktreeId}/terminals", {
        params: { path: { worktreeId } },
        body: { columns: 100, rows: 30 },
      });
      if (transitionRef.current !== token) return;
      if (error !== undefined || data === undefined) {
        setFailure(error === undefined ? "the terminal could not be started" : describeError(error));
        return;
      }
      setTerminals((current) => [data, ...current.filter((terminal) => terminal.id !== data.id)]);
      setSelectedId(data.id);
      setFailure(null);
      requireReconciliation(false);
    } catch {
      if (transitionRef.current === token) {
        setFailure("Terminal creation could not be confirmed. A terminal may already be running; refresh the list before retrying.");
        requireReconciliation(true);
      }
    } finally {
      finishTransition(token);
    }
  }

  async function stop(terminalId: string) {
    if (reconciliationRequiredRef.current) return;
    const token = beginTransition({ kind: "stop", terminalId });
    if (token === null) return;
    let stopConfirmed = false;
    try {
      const { error } = await api.DELETE("/v1/worktrees/{worktreeId}/terminals/{terminalId}", {
        params: { path: { worktreeId, terminalId } },
      });
      if (transitionRef.current !== token) return;
      if (error !== undefined) {
        setFailure(describeError(error));
        return;
      }
      stopConfirmed = true;
      setLoading(true);
      const { data, error: readError } = await api.GET("/v1/worktrees/{worktreeId}/terminals", {
        params: { path: { worktreeId } },
      });
      if (transitionRef.current !== token) return;
      if (readError !== undefined || data === undefined) {
        setFailure(readError === undefined
          ? "Terminal stopped, but the terminal list could not be reconciled. Refresh before starting or stopping another terminal."
          : `Terminal stopped, but the terminal list could not be reconciled: ${describeError(readError)}. Refresh before starting or stopping another terminal.`);
        requireReconciliation(true);
        return;
      }
      acceptTerminals(data.data);
      setFailure(null);
      requireReconciliation(false);
    } catch (cause) {
      if (transitionRef.current !== token) return;
      setFailure(stopConfirmed
        ? `Terminal stopped, but the terminal list could not be reconciled: ${describeError(cause)}. Refresh before starting or stopping another terminal.`
        : "Terminal stop could not be confirmed. Refresh the list to reconcile its current state before retrying.");
      requireReconciliation(true);
    } finally {
      if (transitionRef.current === token) setLoading(false);
      finishTransition(token);
    }
  }

  const selected = terminals.find((terminal) => terminal.id === selectedId) ?? null;
  const busy = transition !== null;
  const creating = transition?.kind === "create";
  const stoppingSelected = transition?.kind === "stop" && transition.terminalId === selected?.id;
  if (loading && terminals.length === 0 && failure === null) return <PaneLoading label="Loading terminals" />;
  return (
    <div className="terminal-pane pane-content">
      <header className="pane-toolbar terminal-toolbar">
        <div className="terminal-tabs" role="tablist" aria-label="Terminal sessions">
          {terminals.map((terminal, index) => (
            <button key={terminal.id} type="button" role="tab" aria-selected={terminal.id === selectedId} className={terminal.id === selectedId ? "terminal-tab selected" : "terminal-tab"} onClick={() => setSelectedId(terminal.id)}>
              <span className={`terminal-status ${terminal.status}`} aria-hidden="true" />
              shell {index + 1}
              {terminal.status === "exited" && terminal.exitCode !== undefined ? ` (${terminal.exitCode})` : ""}
            </button>
          ))}
        </div>
        <button type="button" className="ghost" disabled={busy} onClick={() => void load()}>{transition?.kind === "refresh" ? "Refreshing…" : "↻ Refresh"}</button>
        <button type="button" className="ghost" disabled={busy || reconciliationRequired} onClick={() => void create()}>＋ {creating ? "Starting…" : "Terminal"}</button>
        {selected !== null && (selected.status === "running" || selected.status === "starting") && <button type="button" className="ghost danger-text" disabled={busy || reconciliationRequired} onClick={() => void stop(selected.id)}>{stoppingSelected ? "Stopping…" : "Stop"}</button>}
      </header>
      {failure !== null && <p className="error" role="alert">{failure}</p>}
      {selected === null ? (
        <div className="pane-empty terminal-empty"><span aria-hidden="true">›_</span><strong>No terminal open</strong><small>Start a bounded shell in this worktree.</small><button type="button" disabled={busy || reconciliationRequired} onClick={() => void create()}>{creating ? "Starting…" : "Start terminal"}</button></div>
      ) : (
        <TerminalSurface key={selected.id} worktreeId={worktreeId} terminal={selected} onFailure={setFailure} onEnded={acceptTerminalEnd} />
      )}
    </div>
  );
}

function decodeTerminalEvent(source: string): TerminalEvent | null {
  try {
    const candidate: unknown = JSON.parse(source);
    if (candidate === null || typeof candidate !== "object") return null;
    const event = candidate as Record<string, unknown>;
    if (typeof event["seq"] !== "number" || typeof event["terminalId"] !== "string" || typeof event["kind"] !== "string") return null;
    return candidate as TerminalEvent;
  } catch {
    return null;
  }
}

function TerminalSurface({ worktreeId, terminal, onFailure, onEnded }: { worktreeId: string; terminal: TerminalRecord; onFailure: (failure: string) => void; onEnded: (terminalId: string, status: "exited" | "failed", exitCode?: number) => void }) {
  const host = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const element = host.current;
    if (element === null) return;
    const interactive = terminal.status === "running" || terminal.status === "starting";
    const emulator = new XTerm({
      cursorBlink: true,
      cursorStyle: "bar",
      fontFamily: 'ui-monospace, "SFMono-Regular", "JetBrains Mono", Consolas, monospace',
      fontSize: 13,
      lineHeight: 1.28,
      scrollback: 5_000,
      theme: {
        background: "#10101a",
        foreground: "#d9d9e8",
        cursor: "#e9a35f",
        selectionBackground: "#3d4057",
        black: "#11111b",
        red: "#e78284",
        green: "#a6d189",
        yellow: "#e5c890",
        blue: "#8caaee",
        magenta: "#ca9ee6",
        cyan: "#81c8be",
        white: "#c6d0f5",
      },
    });
    const fit = new FitAddon();
    let finished = false;
    let disposed = false;
    let resizeInFlight = false;
    let pendingResize: { columns: number; rows: number } | null = null;
    emulator.loadAddon(fit);
    emulator.open(element);
    const resize = new ResizeObserver(() => fit.fit());
    resize.observe(element);
    fit.fit();
    emulator.focus();

    const inputBuffer = terminalInput(async (data) => {
      const { error } = await api.POST("/v1/worktrees/{worktreeId}/terminals/{terminalId}/input", {
        params: { path: { worktreeId, terminalId: terminal.id } },
        body: { data },
      });
      if (error !== undefined) throw new Error(describeError(error));
    }, (cause) => onFailure(`Input paused: ${describeError(cause)}. Unsent input was discarded. Inspect the shell and reopen its pane before typing again; uncertain input is never retried.`));
    const input = interactive ? emulator.onData(inputBuffer.write) : null;
    async function resizeTerminal(dimensions: { columns: number; rows: number }) {
      resizeInFlight = true;
      try {
        const { error } = await api.PATCH("/v1/worktrees/{worktreeId}/terminals/{terminalId}", {
          params: { path: { worktreeId, terminalId: terminal.id } },
          body: dimensions,
        });
        if (error !== undefined && !disposed) onFailure(describeError(error));
      } catch (cause: unknown) {
        if (!disposed) onFailure(describeError(cause));
      } finally {
        resizeInFlight = false;
        if (disposed) {
          pendingResize = null;
          return;
        }
        const next = pendingResize;
        pendingResize = null;
        if (next !== null) void resizeTerminal(next);
      }
    }
    const dimensions = interactive ? emulator.onResize(({ cols, rows }) => {
      const next = { columns: cols, rows };
      if (resizeInFlight) {
        pendingResize = next;
        return;
      }
      void resizeTerminal(next);
    }) : null;
    const stream = new EventSource(`/v1/worktrees/${worktreeId}/terminals/${terminal.id}/events`);
    stream.onmessage = (frame: MessageEvent<string>) => {
      const event = decodeTerminalEvent(frame.data);
      if (event === null) {
        onFailure("the terminal sent an invalid event");
        return;
      }
      if (event.replayTruncated) emulator.writeln("\r\n\x1b[33m[older terminal output was evicted]\x1b[0m");
      if (event.kind === "output" && event.data !== undefined) emulator.write(event.data);
      if (event.kind === "exited") {
        inputBuffer.close();
        finished = true;
        stream.close();
        emulator.writeln(`\r\n\x1b[90m[process exited${event.exitCode === undefined ? "" : ` ${event.exitCode}`} ]\x1b[0m`);
        if (interactive) onEnded(terminal.id, "exited", event.exitCode);
      }
      if (event.kind === "failed") {
        inputBuffer.close();
        finished = true;
        stream.close();
        emulator.writeln(`\r\n\x1b[31m[terminal failed${event.data === undefined ? "" : `: ${event.data}`} ]\x1b[0m`);
        if (interactive) onEnded(terminal.id, "failed", event.exitCode);
      }
    };
    stream.onerror = () => {
      if (!finished) onFailure("terminal stream disconnected; reconnecting…");
    };
    return () => {
      disposed = true;
      inputBuffer.close();
      pendingResize = null;
      stream.close();
      resize.disconnect();
      dimensions?.dispose();
      input?.dispose();
      emulator.dispose();
    };
  }, [onEnded, onFailure, terminal.id, terminal.status, worktreeId]);

  return <div className="terminal-surface" ref={host} aria-label={`Terminal ${terminal.id}`} />;
}
