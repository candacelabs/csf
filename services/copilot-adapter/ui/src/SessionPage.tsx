import { useCallback, useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { ActionIcon, Alert, Badge, Box, Button, Center, Flex, Group, Loader, NativeSelect, Paper, Stack, Text, Textarea, Title } from "@mantine/core";
import { api, describeAmbiguousMutation, describeError, newClientUUID } from "./api/client";
import type { Model, PromptMode, Session, SessionRequest, Subagent, TranscriptItem, Worktree } from "./api/client";
import { Dock } from "./components/Dock";
import type { DockPosition, DockTab } from "./components/Dock";
import { RequestsPanel, requestElementId } from "./components/RequestsPanel";
import { TranscriptView } from "./components/TranscriptView";
import { ModelPicker } from "./components/ModelPicker";
import { applySessionEvent, coalesceCallback, decodeSessionEvent, newSessionLiveState } from "./sessionEvents";
import { applyTranscriptItem, emptyTranscript } from "./transcript";

export type SessionPageProps = {
  sessionId: string;
  initialSession: Session | null;
  worktree: Worktree | null;
  models: Model[];
  modelsLoading?: boolean;
  modelsError?: string | null;
  onRefreshModels?: () => void;
  menuButtonRef: RefObject<HTMLButtonElement>;
  onMenu: () => void;
  onResourceChanged: () => void;
};

const resourceRefreshIntervalMillis = 500;
const promptDeliveryFailureCode = "cli_send_failed";

type PromptAttempt = {
  draft: string;
  text: string;
  mode: PromptMode;
  idempotencyKey: string;
};

type AbortAttempt = {
  turnId: string;
  idempotencyKey: string;
};

function promptMayHaveCommitted(status: number, failure: unknown): boolean {
  if (status < 500) return false;
  return !(status === 502 && typeof failure === "object" && failure !== null && "code" in failure && failure.code === promptDeliveryFailureCode);
}

function storedDockPosition(): DockPosition {
  try {
    return window.localStorage.getItem("candace-workbench-dock") === "bottom" ? "bottom" : "right";
  } catch {
    return "right";
  }
}

async function transcriptSnapshot(sessionId: string, signal: AbortSignal): Promise<TranscriptItem[]> {
  const items: TranscriptItem[] = [];
  const seen = new Set<number>();
  let afterSeq: number | undefined;
  do {
    signal.throwIfAborted();
    const { data, error } = await api.GET("/v1/sessions/{sessionId}/transcript", {
      params: {
        path: { sessionId },
        query: { limit: 200, ...(afterSeq === undefined ? {} : { afterSeq }) },
      },
      signal,
    });
    signal.throwIfAborted();
    if (error !== undefined || data === undefined) {
      throw new Error(error === undefined ? "the transcript could not be read" : describeError(error));
    }
    items.push(...data.data);
    afterSeq = data.nextAfterSeq;
    if (afterSeq !== undefined && seen.has(afterSeq)) throw new Error("the transcript repeated a cursor");
    if (afterSeq !== undefined) seen.add(afterSeq);
  } while (afterSeq !== undefined);
  return items;
}

export function SessionPage({ sessionId, initialSession, worktree, models, modelsLoading = false, modelsError = null, onRefreshModels, menuButtonRef, onMenu, onResourceChanged }: SessionPageProps) {
  const [live, setLive] = useState(() => newSessionLiveState(initialSession));
  const [ready, setReady] = useState(false);
  const [draft, setDraft] = useState("");
  const [mode, setMode] = useState<PromptMode>("queue");
  const [promptUncertain, setPromptUncertain] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [aborting, setAborting] = useState(false);
  const [switchingModel, setSwitchingModel] = useState(false);
  const [connection, setConnection] = useState<"connecting" | "live" | "reconnecting">("connecting");
  const [dockTab, setDockTab] = useState<DockTab>("subagents");
  const [dockOpen, setDockOpen] = useState(true);
  const [dockPosition, setDockPosition] = useState<DockPosition>(storedDockPosition);
  const sendingRef = useRef(false);
  const abortingRef = useRef(false);
  const switchingModelRef = useRef(false);
  const promptAttempt = useRef<PromptAttempt | null>(null);
  const abortAttempt = useRef<AbortAttempt | null>(null);
  const requestRevision = useRef(0);
  const requestReloadGeneration = useRef(0);
  const sessionRevision = useRef(0);
  const session = live.session;
  const worktreeId = session?.worktreeId ?? worktree?.id ?? null;

  const loadRequests = useCallback(async (signal?: AbortSignal): Promise<SessionRequest[]> => {
    const { data, error } = await api.GET("/v1/sessions/{sessionId}/requests", {
      params: { path: { sessionId } },
      signal,
    });
    if (error !== undefined || data === undefined) throw new Error(error === undefined ? "pending requests could not be read" : describeError(error));
    return data.data;
  }, [sessionId]);

  const reloadRequests = useCallback(async () => {
    const revision = requestRevision.current;
    const generation = ++requestReloadGeneration.current;
    try {
      const requests = await loadRequests();
      if (generation !== requestReloadGeneration.current || revision !== requestRevision.current) return;
      setLive((current) => ({ ...current, requests }));
    } catch (cause) {
      setFailure(describeError(cause));
    }
  }, [loadRequests]);

  const setSubagents = useCallback((subagents: Subagent[]) => {
    setLive((current) => ({ ...current, subagents }));
  }, []);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    setReady(false);
    setLive(newSessionLiveState(initialSession));
    requestRevision.current = 0;
    requestReloadGeneration.current = 0;
    sessionRevision.current = 0;
    promptAttempt.current = null;
    setPromptUncertain(false);
    abortAttempt.current = null;
    void (async () => {
      try {
        const [sessionResponse, transcript, requests, subagentResponse] = await Promise.all([
          api.GET("/v1/sessions/{sessionId}", { params: { path: { sessionId } }, signal: controller.signal }),
          transcriptSnapshot(sessionId, controller.signal),
          loadRequests(controller.signal),
          api.GET("/v1/sessions/{sessionId}/subagents", { params: { path: { sessionId } }, signal: controller.signal }),
        ]);
        if (cancelled) return;
        if (sessionResponse.error !== undefined || sessionResponse.data === undefined) {
          throw new Error(sessionResponse.error === undefined ? "the session could not be read" : describeError(sessionResponse.error));
        }
        if (subagentResponse.error !== undefined || subagentResponse.data === undefined) {
          throw new Error(subagentResponse.error === undefined ? "subagents could not be read" : describeError(subagentResponse.error));
        }
        const folded = transcript.reduce(applyTranscriptItem, emptyTranscript);
        setLive({
          ...newSessionLiveState(sessionResponse.data),
          transcript: folded,
          requests,
          subagents: subagentResponse.data.data,
        });
        setFailure(null);
        setReady(true);
      } catch (cause) {
        if (cancelled) return;
        setFailure(describeError(cause));
        setReady(true);
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [loadRequests, sessionId]);

  useEffect(() => {
    if (!ready) return;
    const stream = new EventSource(`/v1/sessions/${sessionId}/events`);
    const resourceChanges = coalesceCallback(onResourceChanged, resourceRefreshIntervalMillis);
    stream.onopen = () => setConnection("live");
    stream.onmessage = (frame: MessageEvent<string>) => {
      const event = decodeSessionEvent(frame.data);
      if (event === null) {
        setFailure("the live session stream sent an invalid event");
        return;
      }
      if (event.kind === "requestOpened" || event.kind === "requestResolved") requestRevision.current += 1;
      if (event.kind === "sessionUpdated") sessionRevision.current += 1;
      if (event.kind === "turnCompleted" && abortAttempt.current?.turnId === event.payload.id) {
        abortAttempt.current = null;
      }
      setLive((current) => applySessionEvent(current, event));
      if (event.kind === "sessionUpdated" || event.kind === "turnCompleted") resourceChanges.run();
    };
    stream.onerror = () => setConnection("reconnecting");
    return () => {
      resourceChanges.cancel();
      stream.close();
    };
  }, [onResourceChanged, ready, sessionId]);

  async function send() {
    if (sendingRef.current) return;
    const submittedDraft = draft;
    const text = draft.trim();
    if (text === "") return;
    let attempt: PromptAttempt;
    try {
      const previous = promptAttempt.current;
      attempt = previous ?? { draft: submittedDraft, text, mode, idempotencyKey: newClientUUID() };
    } catch (cause) {
      setFailure(`The prompt could not be prepared. ${describeError(cause)}`);
      return;
    }
    promptAttempt.current = attempt;
    sendingRef.current = true;
    setSending(true);
    setFailure(null);
    try {
      const response = await api.POST("/v1/sessions/{sessionId}/prompts", {
        params: { path: { sessionId } },
        body: { idempotencyKey: attempt.idempotencyKey, text: attempt.text, mode: attempt.mode },
      });
      if (response.error !== undefined) {
        if (promptMayHaveCommitted(response.response.status, response.error)) {
          setDraft(attempt.draft);
          setMode(attempt.mode);
          setPromptUncertain(true);
          setFailure(`The server could not confirm whether the prompt was accepted and it may have succeeded. Retry will reuse the same prompt and idempotency key. ${describeError(response.error)}`);
        } else {
          promptAttempt.current = null;
          setPromptUncertain(false);
          setFailure(describeError(response.error));
        }
        return;
      }
      promptAttempt.current = null;
      setPromptUncertain(false);
      setDraft((current) => current === submittedDraft ? "" : current);
    } catch (cause) {
      setDraft(attempt.draft);
      setMode(attempt.mode);
      setPromptUncertain(true);
      setFailure(`Sending the prompt did not receive a response and may have succeeded. Retry will reuse the same idempotency key. ${describeError(cause)}`);
    } finally {
      sendingRef.current = false;
      setSending(false);
    }
  }

  async function abort() {
    if (abortingRef.current) return;
    abortingRef.current = true;
    setAborting(true);
    setFailure(null);
    try {
      let attempt = abortAttempt.current;
      if (attempt === null) {
        const { data, error } = await api.GET("/v1/sessions/{sessionId}/active-turn", {
          params: { path: { sessionId } },
        });
        if (error !== undefined || data === undefined) {
          setFailure(error === undefined ? "the active turn could not be read" : describeError(error));
          return;
        }
        attempt = { turnId: data.id, idempotencyKey: newClientUUID() };
        abortAttempt.current = attempt;
      }
      const { error } = await api.POST("/v1/sessions/{sessionId}/abort", {
        params: { path: { sessionId } },
        body: { turnId: attempt.turnId, idempotencyKey: attempt.idempotencyKey },
      });
      if (error !== undefined) {
        if (typeof error === "object" && error !== null && "code" in error &&
          (error.code === "abort_target_changed" || error.code === "no_turn_in_flight")) {
          abortAttempt.current = null;
        }
        setFailure(describeError(error));
        return;
      }
      abortAttempt.current = null;
    } catch (cause) {
      setFailure(`Stopping the turn did not receive a response and may have succeeded. Retry will target the same turn with the same idempotency key. ${describeError(cause)}`);
    } finally {
      abortingRef.current = false;
      setAborting(false);
    }
  }

  async function switchModel(model: string) {
    if (switchingModelRef.current || session === null || modelsLoading || modelsError !== null || !models.some((candidate) => candidate.id === model)) return;
    const revision = sessionRevision.current;
    switchingModelRef.current = true;
    setSwitchingModel(true);
    setFailure(null);
    try {
      const { data, error } = await api.PATCH("/v1/sessions/{sessionId}", {
        params: { path: { sessionId } },
        body: { model },
      });
      if (error !== undefined || data === undefined) {
        setFailure(error === undefined ? "the model could not be changed" : describeError(error));
      } else if (revision === sessionRevision.current) {
        setLive((current) => ({ ...current, session: data }));
      }
    } catch (cause) {
      setFailure(describeAmbiguousMutation("Changing the model", cause));
    } finally {
      switchingModelRef.current = false;
      setSwitchingModel(false);
    }
  }

  function changeDockPosition(position: DockPosition) {
    setDockPosition(position);
    try { window.localStorage.setItem("candace-workbench-dock", position); } catch { /* private browsing can deny storage */ }
  }

  return (
    <Stack component="section" className={`session-workspace dock-at-${dockPosition}${dockOpen ? "" : " dock-is-closed"}`} gap={0} h="100%" w="100%" style={{ minHeight: 0, overflow: "hidden" }}>
      <Paper component="header" className="workspace-header" radius={0} withBorder px={{ base: "xs", sm: "md" }} py="xs" style={{ flex: "0 0 auto" }}>
        <Group wrap="nowrap" gap="sm" w="100%">
          <ActionIcon ref={menuButtonRef} type="button" variant="subtle" hiddenFrom="sm" aria-label="Open task sidebar" onClick={onMenu}>☰</ActionIcon>
          <Box style={{ flex: "1 1 auto", minWidth: 0 }}>
            <Group gap="xs" wrap="nowrap">
              <Title order={6} style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{session?.displayName ?? "Loading task…"}</Title>
              {session !== null && <Badge color={session.status === "running" ? "teal" : session.status === "failed" ? "red" : "gray"} variant="light">{session.status}</Badge>}
            </Group>
            <Text size="xs" c="dimmed" truncate>{worktree?.branch || session?.workingDirectory || "Worktree"}</Text>
          </Box>
          <Badge variant="dot" color={connection === "live" ? "teal" : "orange"} visibleFrom="sm">{connection}</Badge>
          <ModelPicker compact models={models} value={session?.model ?? ""} onChange={(model) => void switchModel(model)} loading={modelsLoading} error={modelsError} disabled={switchingModel || session === null} onRefresh={onRefreshModels} />
        </Group>
      </Paper>
      {failure !== null && (
        <Alert color="red" variant="light" role="alert" title={failure} mx="sm" mt="xs">
          <Button type="button" variant="subtle" size="compact-xs" aria-label="Dismiss error" onClick={() => setFailure(null)}>Dismiss</Button>
        </Alert>
      )}
      <Flex
        className="workspace-body"
        direction={{ base: "column", sm: dockPosition === "right" ? "row" : "column" }}
        style={{ flex: "1 1 auto", minWidth: 0, minHeight: 0, overflow: "hidden" }}
      >
        <Stack className="chat-column" gap={0} style={{ flex: "1 1 auto", minWidth: 0, minHeight: 0 }}>
          <Box className="chat-scroll" px={{ base: "sm", md: "xl" }} py="lg" style={{ flex: "1 1 auto", minHeight: 0, overflowY: "auto" }}>
            {!ready && <Center className="chat-loading" mih={160}><Group gap="sm"><Loader size="sm" color="teal" /><Text c="dimmed">Opening session…</Text></Group></Center>}
            <TranscriptView
              entries={live.transcript.items}
              streamingActive={session?.status === "running"}
              now={new Date()}
              requests={live.requests}
              onReviewRequest={(requestId) => {
                const request = document.getElementById(requestElementId(requestId));
                request?.scrollIntoView?.({ block: "nearest" });
                request?.focus({ preventScroll: true });
              }}
            />
          </Box>
          <RequestsPanel sessionId={sessionId} requests={live.requests} onResolved={() => void reloadRequests()} />
          <Paper component="form" className="composer" withBorder shadow="sm" radius="lg" p="sm" w="calc(100% - 1rem)" maw={760} mx="auto" mb="sm" onSubmit={(event) => { event.preventDefault(); void send(); }}>
            <Textarea
              aria-label="Prompt"
              autosize
              minRows={3}
              maxRows={8}
              disabled={sending || promptUncertain}
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key !== "Enter" || event.shiftKey) return;
                event.preventDefault();
                void send();
              }}
              placeholder="Ask Candace to work on something…"
            />
            <Group className="composer-actions" mt="xs" gap="xs" wrap="nowrap">
              <NativeSelect
                aria-label="Prompt mode"
                value={mode}
                onChange={(event) => setMode(event.currentTarget.value as PromptMode)}
                data={[{ value: "queue", label: "Queue" }, { value: "steer", label: "Steer current turn" }]}
                disabled={sending || promptUncertain}
                size="xs"
                style={{ width: 160 }}
              />
              <Text className="composer-hint" size="xs" c="dimmed" visibleFrom="sm">
                {promptUncertain ? "Retry to confirm this prompt was sent" : "Enter to send · Shift+Enter for a new line"}
              </Text>
              <Box style={{ flex: "1 1 auto" }} />
              {session?.status === "running" && <Button type="button" variant="default" color="red" loading={aborting} onClick={() => void abort()}>{aborting ? "Stopping…" : "Stop"}</Button>}
              <Button type="submit" loading={sending} disabled={sending || draft.trim() === ""} aria-label={promptUncertain ? "Retry prompt" : "Send prompt"}>
                {promptUncertain ? "Retry" : "↑"}
              </Button>
            </Group>
          </Paper>
        </Stack>
        {worktreeId !== null && (
          <Dock
            active={dockTab}
            open={dockOpen}
            position={dockPosition}
            sessionId={sessionId}
            worktreeId={worktreeId}
            revision={live.worktreeRevision}
            subagents={live.subagents}
            liveActivity={live.activityBySubagent}
            onTab={setDockTab}
            onOpenChange={setDockOpen}
            onPosition={changeDockPosition}
            onSubagentsLoaded={setSubagents}
          />
        )}
      </Flex>
    </Stack>
  );
}
