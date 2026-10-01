import { useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { Alert, Box, Button, Container, Group, Loader, Stack, Text, Title } from "@mantine/core";
import { attachKanbanDragging, kanbanLiveURL, kanbanViewURL, loadGotthRuntime } from "./kanbanIsland";

type KanbanPageProps = { menuButtonRef: RefObject<HTMLButtonElement>; onMenu: () => void; onNewSession: () => void };

export function KanbanPage({ menuButtonRef, onMenu, onNewSession }: KanbanPageProps) {
  const host = useRef<HTMLDivElement>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [announcement, setAnnouncement] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const element = host.current;
    if (!element) return;
    const controller = new AbortController();
    let dispose: (() => void) | undefined;
    let runtime: Awaited<ReturnType<typeof loadGotthRuntime>> | undefined;
    setLoading(true);
    setFailure(null);
    const onError = (event: Event) => setAnnouncement((event as CustomEvent<{ message?: string }>).detail?.message ?? "The board action could not be completed.");
    document.addEventListener("gotth-live:error", onError);
    void (async () => {
      try {
        const [response, loadedRuntime] = await Promise.all([
          fetch(kanbanViewURL, { signal: controller.signal, credentials: "same-origin", headers: { Accept: "text/html" } }),
          loadGotthRuntime(),
        ]);
        if (!response.ok) throw new Error(`The board could not be loaded (${response.status}).`);
        const html = await response.text();
        if (controller.signal.aborted) return;
        // Trusted same-origin server markup. React renders no children here;
        // all subsequent descendant updates belong to the gotth connection.
        element.innerHTML = html;
        runtime = loadedRuntime;
        runtime.start(kanbanLiveURL, element);
        dispose = attachKanbanDragging(element, setAnnouncement, () => loadedRuntime.status() === "live");
        setLoading(false);
      } catch (error) {
        if (controller.signal.aborted) return;
        setFailure(error instanceof Error ? error.message : "The board could not be loaded.");
        setLoading(false);
      }
    })();
    return () => {
      controller.abort();
      document.removeEventListener("gotth-live:error", onError);
      dispose?.();
      runtime?.stop();
      element.replaceChildren();
    };
  }, [attempt]);
  return <Box w="100%" h="100%" bg="gray.0" style={{ overflowY: "auto" }}>
    <Container size="100%" py="xl" px={{ base: "md", sm: "xl" }}><Stack gap="lg">
      <Group justify="space-between"><Group gap="xs">
        <Button ref={menuButtonRef} hiddenFrom="sm" variant="subtle" aria-label="Open task sidebar" onClick={onMenu}>☰</Button>
        <Title order={1}>Your work, in view.</Title>
      </Group><Button onClick={onNewSession}>＋ New task</Button></Group>
      <Text size="sm" role="status" aria-live="polite">{announcement}</Text>
      {loading && <Group><Loader size="sm" /><Text>Connecting to your board…</Text></Group>}
      {failure && <Alert color="red" title="Board unavailable"><Stack gap="sm"><Text>{failure}</Text><Button onClick={() => setAttempt((value) => value + 1)}>Retry board</Button></Stack></Alert>}
      <div ref={host} />
    </Stack></Container>
  </Box>;
}
