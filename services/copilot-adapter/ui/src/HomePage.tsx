import type { RefObject } from "react";
import { Box, Button, Card, Container, Group, Paper, SimpleGrid, Stack, Text, ThemeIcon, Title } from "@mantine/core";
import type { Model, Session, SessionStatus, Worktree } from "./api/client";
import { SessionBoard } from "./SessionBoard";
import { csfDestinations } from "./navigation";

type HomePageProps = {
  kanban?: boolean;
  sessions: Session[];
  worktrees: Worktree[];
  models: Model[];
  loading: boolean;
  refreshing: boolean;
  error: string | null;
  modelsLoading: boolean;
  modelsError: string | null;
  observedAt: Date | null;
  menuButtonRef: RefObject<HTMLButtonElement>;
  onMenu: () => void;
  onNewSession: () => void;
  onRefresh: () => void;
};

const workingStatus: SessionStatus = "running";
function HomeCounts(props: HomePageProps) {
  const resourceValue = (value: number) => props.error ? "Unavailable" : props.loading ? "…" : value;
  const counts = [
    { label: props.kanban ? "Sessions" : "Tasks", value: resourceValue(props.sessions.length) },
    { label: "Working now", value: resourceValue(props.sessions.filter((session) => session.status === workingStatus).length) },
    { label: "Worktrees", value: resourceValue(props.worktrees.length) },
    { label: "Available models", value: props.modelsError ? "Unavailable" : props.modelsLoading ? "…" : props.models.length },
  ];
  return <SimpleGrid cols={{ base: 2, md: 4 }} spacing="sm">
    {counts.map(({ label, value }) => <Paper key={label} withBorder radius="md" p="md">
      <Text size="xs" c="dimmed" fw={600}>{label}</Text>
      <Text size="xl" fw={700}>{value}</Text>
    </Paper>)}
  </SimpleGrid>;
}

function ExploreCSF() {
  const destinations = csfDestinations();
  if (destinations.length === 0) return null;
  return <Stack component="section" aria-labelledby="home-explore" gap="md">
    <Title id="home-explore" order={2} size="h3">Around the workshop</Title>
    <SimpleGrid cols={{ base: 1, md: destinations.length }}>
      {destinations.map((destination) => <Card component="a" key={destination.name} href={destination.href} withBorder radius="md" p="lg" c="inherit" td="none">
        <Stack gap="sm">
          <ThemeIcon variant="light" color="orange" size="lg" radius="md" aria-hidden="true">{destination.symbol}</ThemeIcon>
          <Text fw={600}>{destination.name}</Text>
          <Text size="sm" c="dimmed">{destination.description}</Text>
          <Text size="sm" c="orange">Open →</Text>
        </Stack>
      </Card>)}
    </SimpleGrid>
  </Stack>;
}

export function HomePage(props: HomePageProps) {
  return <Box w="100%" h="100%" bg="gray.0" style={{ overflowY: "auto" }}>
    <Container size={props.kanban ? "100%" : "lg"} py="xl" px={{ base: "md", sm: "xl" }}>
      <Stack gap="xl">
        <Group justify="space-between">
          <Group gap="xs"><Button ref={props.menuButtonRef} hiddenFrom="sm" variant="subtle" aria-label="Open task sidebar" onClick={props.onMenu}>☰</Button>
            <Text fw={700} size="sm" c="orange">{csfDestinations().length ? "CSF · The Cerebrospinal Fluid" : "Workbench"}</Text>
          </Group>
          <Button variant="default" size="xs" loading={props.refreshing} onClick={props.onRefresh}>Refresh overview</Button>
        </Group>
        {props.kanban ? <Group justify="space-between">
          <Title order={1}>Your work, in view.</Title><Button onClick={props.onNewSession}>＋ New task</Button>
        </Group> : <Paper withBorder radius="lg" p={{ base: "lg", sm: "xl" }} bg="orange.0">
          <Stack gap="sm">
            <Text size="xs" fw={700} c="orange.9" tt="uppercase">Your workspace, connected</Text>
            <Title order={1}>Make yourself at home.</Title>
            <Text c="dimmed" maw={560}>Start a task, return to a conversation, or see how your work is progressing. Everything starts here.</Text>
            <Group mt="xs"><Button onClick={props.onNewSession}>＋ New task</Button><Text size="xs" c="dimmed">Isolated worktrees by default.</Text></Group>
          </Stack>
        </Paper>}
        <Stack gap="xs"><HomeCounts {...props} />
          <Text size="xs" c="dimmed">{props.observedAt ? `Tasks and worktrees checked at ${props.observedAt.toLocaleTimeString()}. Use Refresh overview to check for changes.` : props.error ? "No workspace snapshot available." : "Connecting to your workspace…"}</Text>
          {props.modelsError && <Text size="xs" c="red">Model catalog unavailable. Existing tasks remain accessible.</Text>}
        </Stack>
        {!props.kanban && <ExploreCSF />}
        <SessionBoard {...props} />
      </Stack>
    </Container>
  </Box>;
}
