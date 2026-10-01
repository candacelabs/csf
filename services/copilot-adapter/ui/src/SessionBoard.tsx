import { useState } from "react";
import { Badge, Button, Group, Modal, NativeSelect, Paper, SegmentedControl, SimpleGrid, Skeleton, Stack, Text, TextInput, Title } from "@mantine/core";
import type { Session, SessionStatus, Worktree } from "./api/client";
import { compareInstants } from "./format";
import { SessionCard } from "./SessionCard";
import { PlanningBoardView } from "./PlanningBoard";
import { kanbanScaffolds, sessionLanes } from "./kanban";

type SessionBoardProps = {
  sessions: Session[];
  worktrees: Worktree[];
  loading: boolean;
  error: string | null;
  kanban?: boolean;
};


function PlannedConnections() {
  const [selected, setSelected] = useState<(typeof kanbanScaffolds)[number] | null>(null);
  return <Paper component="section" aria-labelledby="kanban-connections" withBorder p="md" radius="md">
    <Stack gap="sm">
      <Title order={2} size="h4" id="kanban-connections">Connections taking shape</Title>
      <Text size="sm" c="dimmed">Session links work now. Hover for a hint, or select a scaffold to see its wiring plan and acceptance check.</Text>
      <Group gap="xs">{kanbanScaffolds.map((scaffold) => <Button key={scaffold.label} variant="light" color="gray" size="xs"
        title={`${scaffold.summary} ${scaffold.tools}`} onClick={() => setSelected(scaffold)}>
        {scaffold.label} · scaffold ⓘ
      </Button>)}</Group>
    </Stack>
    <Modal opened={selected !== null} onClose={() => setSelected(null)} title={selected?.label} centered size="lg">
      {selected && <Stack gap="md">
        <Badge color="orange" variant="light">Scaffold · not connected</Badge>
        <Text size="sm">{selected.summary}</Text>
        {[
          ["Already available", selected.reuse], ["Wiring plan", selected.plan],
          ["Toolbelt", selected.tools], ["Ready when", selected.acceptance],
        ].map(([label, value]) => <Stack key={label} gap={4}><Text size="sm" fw={700}>{label}</Text><Text size="sm" c="dimmed">{value}</Text></Stack>)}
      </Stack>}
    </Modal>
  </Paper>;
}

export function SessionBoard({ sessions, worktrees, loading, error, kanban = false }: SessionBoardProps) {
  const [view, setView] = useState("planning");
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("");
  const worktreeByID = new Map(worktrees.map((worktree) => [worktree.id, worktree]));
  const matching = sessions.filter((session) =>
    (status === "" || session.status === status) &&
    `${session.id} ${session.displayName} ${session.model} ${worktreeByID.get(session.worktreeId)?.branch ?? ""}`.toLowerCase().includes(query.toLowerCase()),
  ).sort((left, right) => compareInstants(right.updatedAt, left.updatedAt));
  const card = (session: Session) => <SessionCard key={session.id} session={session} worktree={worktreeByID.get(session.worktreeId)} />;
  const lanes = (Object.keys(sessionLanes) as SessionStatus[]).filter((lane) => !status || lane === status);
  return <Stack component="section" aria-labelledby="home-tasks" gap="md">
    <Group justify="space-between"><Title id="home-tasks" order={2} size="h3">{kanban ? "Session board" : "Pick up where you left off"}</Title>
      <Button component="a" href={kanban ? "#/" : "#/kanban"} variant="light" size="xs">{kanban ? "Home overview" : "Open Kanban"}</Button>
      {error && <Badge color="red" variant="light">{loading ? "Workspace unavailable" : "Refresh failed · previous snapshot"}</Badge>}
    </Group>
    {kanban && <SegmentedControl aria-label="Board view" value={view} onChange={setView} data={[{ value: "planning", label: "Planning" }, { value: "runtime", label: "Runtime status" }]} />}
    {kanban && <Text size="sm" c="dimmed">{view === "planning" ? "Drag the grip to plan and reorder. Saved in this browser only; these columns do not start or stop sessions, update tickets, or sync to other users." : "Live session states, grouped automatically. Idle and ended do not mean the task is complete."}</Text>}

    <SimpleGrid cols={{ base: 1, sm: 2 }}>
      <TextInput aria-label="Find a task" placeholder="Find a session ID, task, branch or model…" value={query} onChange={(event) => setQuery(event.currentTarget.value)} />
      <NativeSelect aria-label="Filter tasks by status" value={status} onChange={(event) => setStatus(event.currentTarget.value)} data={[{ value: "", label: "All statuses" }, ...Object.keys(sessionLanes)]} />
    </SimpleGrid>
    {loading && !error ? <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} aria-label="Loading tasks">
      {[1, 2, 3].map((key) => <Skeleton key={key} h={140} radius="md" />)}
    </SimpleGrid> : !loading && kanban && view === "planning" ? <PlanningBoardView sessions={sessions} matching={matching} worktrees={worktrees} /> : !loading && kanban ? <SimpleGrid cols={{ base: 1, sm: 2, xl: 5 }} spacing="sm">
      {lanes.map((lane) => <Paper component="section" aria-label={`${sessionLanes[lane].label} sessions`} key={lane} withBorder radius="md" p="sm" bg="gray.0">
        <Stack gap="sm">
          <Group justify="space-between"><Text fw={700} size="sm" title={sessionLanes[lane].description}>{sessionLanes[lane].label}</Text>
            <Badge color={sessionLanes[lane].color} variant="light" aria-label={`${lane} count`}>{matching.filter((session) => session.status === lane).length}</Badge>
          </Group>
          {matching.filter((session) => session.status === lane).map(card)}
          {!matching.some((session) => session.status === lane) && <Text c="dimmed" size="xs" py="md">{error ? "None in the previous snapshot" : "No matching sessions"}</Text>}
        </Stack>
      </Paper>)}
    </SimpleGrid> : <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }}>{matching.map(card)}</SimpleGrid>}
    {!loading && !kanban && matching.length === 0 && <Text c="dimmed">{sessions.length === 0 ? "Your first task starts here. Choose New task to get going." : "No tasks match these filters."}</Text>}
    {kanban && <PlannedConnections />}
    {loading && error && <Text c="dimmed">Tasks could not be loaded. Refresh to try again.</Text>}
  </Stack>;
}
