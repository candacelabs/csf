import { Anchor, Badge, Card, Code, Group, Stack, Text } from "@mantine/core";
import type { Session, Worktree } from "./api/client";
import { absoluteTime, relativeTime, worktreeLabel } from "./format";
import { sessionLanes } from "./kanban";

export function SessionCard({ session, worktree }: { session: Session; worktree: Worktree | undefined }) {
  const href = `#/sessions/${session.id}`;
  return <Card component="article" aria-label={session.displayName} withBorder radius="md" p="md">
    <Stack gap="xs">
      <Group justify="space-between" wrap="nowrap">
        <Badge variant="light" color={sessionLanes[session.status].color}>{session.status}</Badge>
        <Text component="time" size="xs" c="dimmed" dateTime={session.updatedAt} title={absoluteTime(session.updatedAt)}>
          {relativeTime(session.updatedAt, new Date())}
        </Text>
      </Group>
      <Anchor href={href} c="inherit" fw={600} lineClamp={2} aria-label={`Continue ${session.displayName}`}>{session.displayName}</Anchor>
      <Anchor href={href} size="xs" title="Open this session's conversation and tool activity" style={{ overflowWrap: "anywhere" }} aria-label={`Session ${session.id}`}>{session.id}</Anchor>
      <Text size="xs" c="dimmed" truncate title={worktree?.path}>{worktree ? worktreeLabel(worktree) : "Unavailable worktree"}</Text>
      <Text size="xs" c="dimmed" truncate>{session.model}</Text>
      {worktree && <details>
        <Text component="summary" size="xs" c="orange">Worktree identity</Text>
        <Stack gap={4} mt="xs"><Code style={{ overflowWrap: "anywhere" }}>{worktree.id}</Code>
          <Text size="xs">Recorded HEAD</Text><Code style={{ overflowWrap: "anywhere" }}>{worktree.headSha || "Unknown"}</Code>
        </Stack>
      </details>}
    </Stack>
  </Card>;
}

