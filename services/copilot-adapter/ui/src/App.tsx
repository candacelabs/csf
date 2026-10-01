import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useReducedMotion } from "@mantine/hooks";
import { Affix, AppShell, MantineProvider, Overlay, Alert, Anchor, Button } from "@mantine/core";
import { api, describeError } from "./api/client";
import type { Model, Repository, Session, Worktree } from "./api/client";
import { NewSessionDialog } from "./components/NewSessionDialog";
import { Sidebar } from "./components/Sidebar";
import { SessionPage } from "./SessionPage";
import { SimulationPage } from "./SimulationPage";
import { ReleasePage } from "./ReleasePage";
import { csfDestinations } from "./navigation";
import { KanbanPage } from "./KanbanPage";
import { HomePage } from "./HomePage";
import { workbenchTheme } from "./theme";
import { WorkbenchThemeOverride } from "./WorkbenchThemeOverride";

export function sessionIdFromHash(hash: string): string | null {
  const match = /^#\/sessions\/([^/?#]+)$/.exec(hash);
  return match?.[1] ?? null;
}

async function allSessions(): Promise<Session[]> {
  const sessions: Session[] = [];
  const seen = new Set<string>();
  let cursor: string | undefined;
  do {
    const { data, error } = await api.GET("/v1/sessions", {
      params: { query: { limit: 100, ...(cursor === undefined ? {} : { cursor }) } },
    });
    if (error !== undefined || data === undefined) {
      throw new Error(error === undefined ? "the session list could not be read" : describeError(error));
    }
    sessions.push(...data.data);
    cursor = data.nextCursor;
    if (cursor !== undefined && seen.has(cursor)) throw new Error("the session list repeated a cursor");
    if (cursor !== undefined) seen.add(cursor);
  } while (cursor !== undefined);
  return sessions;
}

export function App() {
  return (
    <>
      <MantineProvider theme={workbenchTheme} forceColorScheme="light">
        <Workbench />
      </MantineProvider>
      <WorkbenchThemeOverride />
    </>
  );
}

function Workbench() {
  const [route, setRoute] = useState(() => window.location.hash);
  const sessionId = sessionIdFromHash(route);
  const kanban = route === "#/kanban";
  const destination = csfDestinations().find((entry) => entry.href === route.split("?")[0]);
  const simulations = destination?.href === "#/simulations";
  const release = destination?.href === "#/release";
  const [sessions, setSessions] = useState<Session[]>([]);
  const [worktrees, setWorktrees] = useState<Worktree[]>([]);
  const [resourcesLoaded, setResourcesLoaded] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [observedAt, setObservedAt] = useState<Date | null>(null);
  const [repositories, setRepositories] = useState<Repository[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [modelsLoading, setModelsLoading] = useState(true);
  const [modelsError, setModelsError] = useState<string | null>(null);
  const modelReloadRunning = useRef(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const reducedMotion = useReducedMotion();
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const reloadRunning = useRef(false);
  const reloadQueued = useRef(false);

  const reload = useCallback(async () => {
    if (reloadRunning.current) {
      reloadQueued.current = true;
      return;
    }
    reloadRunning.current = true;
    setRefreshing(true);
    try {
      do {
        reloadQueued.current = false;
        try {
          const [listedSessions, repositoriesResponse, worktreesResponse] =
            await Promise.all([
              allSessions(),
              api.GET("/v1/repositories", {}),
              api.GET("/v1/worktrees", {}),
            ]);
          if (reloadQueued.current) continue;
          if (repositoriesResponse.error !== undefined || repositoriesResponse.data === undefined) {
            throw new Error(
              repositoriesResponse.error === undefined
                ? "the repository list could not be read"
                : describeError(repositoriesResponse.error),
            );
          }
          if (worktreesResponse.error !== undefined || worktreesResponse.data === undefined) {
            throw new Error(
              worktreesResponse.error === undefined
                ? "the worktree list could not be read"
                : describeError(worktreesResponse.error),
            );
          }
          setSessions(listedSessions);
          setRepositories(repositoriesResponse.data.data);
          setWorktrees(worktreesResponse.data.data);
          setResourcesLoaded(true);
          setObservedAt(new Date());
          setFailure(null);
        } catch (cause) {
          if (!reloadQueued.current) setFailure(describeError(cause));
        }
      } while (reloadQueued.current);
    } finally {
      reloadRunning.current = false;
      setRefreshing(false);
    }
  }, []);

  const reloadModels = useCallback(async () => {
    if (modelReloadRunning.current) return;
    modelReloadRunning.current = true;
    setModelsLoading(true);
    setModelsError(null);
    try {
      const { data, error } = await api.GET("/v1/models", {});
      if (error !== undefined || data === undefined) {
        throw new Error(error === undefined ? "the model list could not be read" : describeError(error));
      }
      setModels(data.data);
    } catch (cause) {
      setModels([]);
      setModelsError(describeError(cause));
    } finally {
      modelReloadRunning.current = false;
      setModelsLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
    void reloadModels();
  }, [reload, reloadModels]);

  useEffect(() => {
    const onHashChange = () => {
      setRoute(window.location.hash);
      setSidebarOpen(false);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "n") return;
      event.preventDefault();
      setCreating(true);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const selectedSession = useMemo(
    () => sessions.find((session) => session.id === sessionId) ?? null,
    [sessionId, sessions],
  );
  const selectedWorktree = useMemo(
    () => worktrees.find((worktree) => worktree.id === selectedSession?.worktreeId) ?? null,
    [selectedSession, worktrees],
  );

  return (
    <>
      <Anchor className="skip-link" href="#main-workbench">
        Skip to workspace
      </Anchor>
      <AppShell
        padding={0}
        navbar={{ width: 276, breakpoint: "sm", collapsed: { mobile: !sidebarOpen } }}
        transitionDuration={reducedMotion ? 0 : 180}
        transitionTimingFunction="ease-out"
        style={{ height: "100dvh", overflow: "hidden" }}
      >
        <AppShell.Navbar p={0} zIndex={201}>
          <Sidebar
            open={sidebarOpen}
            sessions={sessions}
            worktrees={worktrees}
            loading={!resourcesLoaded}
            error={resourcesLoaded ? null : failure}
            repositories={repositories}
            selectedSessionId={sessionId}
            kanbanActive={kanban}
            destinationActive={destination?.href}
            menuButtonRef={menuButtonRef}
            onClose={() => setSidebarOpen(false)}
            onNewSession={() => setCreating(true)}
          />
        </AppShell.Navbar>
        {sidebarOpen && <Overlay hiddenFrom="sm" zIndex={200} onClick={() => setSidebarOpen(false)} />}
        <AppShell.Main
          component="main"
          id="main-workbench"
          style={{ display: "flex", height: "100dvh", minWidth: 0, minHeight: 0, overflow: "hidden" }}
        >
        {failure !== null && (
          <Affix position={{ top: 12, left: "50%" }} withinPortal={false} zIndex={300}>
            <Alert color="red" variant="light" role="alert" title={failure} w="min(580px, 90vw)" style={{ transform: "translateX(-50%)" }}>
              <Button type="button" variant="subtle" size="compact-xs" onClick={() => void reload()}>
                Retry
              </Button>
            </Alert>
          </Affix>
        )}
        {simulations ? <SimulationPage runId={new URLSearchParams(route.split("?")[1]).get("run")} menuButtonRef={menuButtonRef} onMenu={() => setSidebarOpen(true)} /> : release ? <ReleasePage menuButtonRef={menuButtonRef} onMenu={() => setSidebarOpen(true)} /> : kanban ? <KanbanPage menuButtonRef={menuButtonRef} onMenu={() => setSidebarOpen(true)} onNewSession={() => setCreating(true)} /> : sessionId === null ? (
          <HomePage kanban={kanban} sessions={sessions} worktrees={worktrees} models={models}
            loading={!resourcesLoaded} refreshing={refreshing} error={failure}
            modelsLoading={modelsLoading} modelsError={modelsError} observedAt={observedAt}
            menuButtonRef={menuButtonRef} onMenu={() => setSidebarOpen(true)}
            onNewSession={() => setCreating(true)}
            onRefresh={() => { void reload(); void reloadModels(); }} />
        ) : (
          <SessionPage
            key={sessionId}
            sessionId={sessionId}
            initialSession={selectedSession}
            worktree={selectedWorktree}
            models={models}
            modelsLoading={modelsLoading}
            modelsError={modelsError}
            onRefreshModels={() => void reloadModels()}
            menuButtonRef={menuButtonRef}
            onMenu={() => setSidebarOpen(true)}
            onResourceChanged={reload}
          />
        )}
        </AppShell.Main>
      </AppShell>
      {creating && (
        <NewSessionDialog
          repositories={repositories}
          worktrees={worktrees}
          models={models}
          modelsLoading={modelsLoading}
          modelsError={modelsError}
          onRefreshModels={() => void reloadModels()}
          onClose={() => setCreating(false)}
          onCreated={(session) => {
            setCreating(false);
            void reload();
            window.location.hash = `#/sessions/${session.id}`;
          }}
        />
      )}
    </>
  );
}
