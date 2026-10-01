import { useEffect, useMemo, useRef, useState } from "react";
import type { RefObject } from "react";
import { ActionIcon, Anchor, Badge, Box, Button, FocusTrap, Group, Kbd, NavLink, Stack, Text, Title } from "@mantine/core";
import type { Repository, Session, Worktree } from "../api/client";
import { relativeTime, worktreeLabel } from "../format";
import { csfDestinations } from "../navigation";

type SidebarProps = {
  open: boolean;
  sessions: Session[];
  worktrees: Worktree[];
  loading?: boolean;
  error?: string | null;
  repositories: Repository[];
  selectedSessionId: string | null;
  kanbanActive?: boolean;
  destinationActive?: string;
  menuButtonRef: RefObject<HTMLButtonElement>;
  onClose: () => void;
  onNewSession: () => void;
};

export function Sidebar({
  open,
  sessions,
  worktrees,
  loading = false,
  error = null,
  repositories,
  selectedSessionId,
  kanbanActive = false,
  destinationActive,
  menuButtonRef,
  onClose,
  onNewSession,
}: SidebarProps) {
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const wasOpen = useRef(false);
  const repositoryNames = useMemo(
    () => new Map(repositories.map((repository) => [repository.id, repository.displayName])),
    [repositories],
  );
  const knownWorktrees = new Set(worktrees.map((worktree) => worktree.id));
  const orphaned = sessions.filter((session) => !knownWorktrees.has(session.worktreeId));
  const now = new Date();
  const destinations = csfDestinations();

  useEffect(() => {
    if (open && !wasOpen.current) closeButtonRef.current?.focus();
    if (!open && wasOpen.current) menuButtonRef.current?.focus();
    wasOpen.current = open;
  }, [menuButtonRef, open]);

  function toggle(worktreeId: string) {
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(worktreeId)) next.delete(worktreeId);
      else next.add(worktreeId);
      return next;
    });
  }

  return (
    <FocusTrap active={open}>
      <Box
        component="aside"
        role="complementary"
        aria-label="Tasks and worktrees"
        h="100%"
        onKeyDown={(event) => {
          if (open && event.key === "Escape") onClose();
        }}
        style={{ display: "flex", minHeight: 0 }}
      >
        <Stack gap="sm" p="sm" w="100%" h="100%" style={{ minHeight: 0 }}>
          <Group justify="space-between" wrap="nowrap" px="xs" py={4}>
            <Anchor href="#/" aria-label="Candace workbench home" underline="never" c="inherit">
              <Group gap="sm" wrap="nowrap">
                <Badge color="orange" variant="light" size="lg" circle>C</Badge>
                <Title order={4} size="h4">Workbench</Title>
              </Group>
            </Anchor>
            <ActionIcon
              ref={closeButtonRef}
              type="button"
              variant="subtle"
              color="gray"
              hiddenFrom="sm"
              aria-label="Close sidebar"
              onClick={onClose}
            >
              ×
            </ActionIcon>
          </Group>

          <NavLink component="a" href="#/" label="Home" active={selectedSessionId === null && !kanbanActive && !destinationActive} aria-current={selectedSessionId === null && !kanbanActive && !destinationActive ? "page" : undefined} />
          <NavLink component="a" href="#/kanban" label="Kanban" active={kanbanActive} aria-current={kanbanActive ? "page" : undefined} />
          {destinations.length > 0 && (
            <Stack component="nav" gap={4} aria-label="CSF destinations">
              <Text size="xs" fw={700} c="dimmed" tt="uppercase" px="sm">CSF</Text>
              {destinations.map((destination) => <NavLink key={destination.name} component="a" href={destination.href} label={destination.name} aria-label={destination.name} active={destinationActive === destination.href} aria-current={destinationActive === destination.href ? "page" : undefined} leftSection={<Text span c="orange" aria-hidden="true">{destination.symbol}</Text>} />)}
            </Stack>
          )}

          <Button
            fullWidth
            variant="light"
            leftSection={<Text span fw={700}>＋</Text>}
            rightSection={<Kbd size="xs">⌘ N</Kbd>}
            onClick={onNewSession}
          >
            New task
          </Button>

          <Text size="xs" fw={700} c="dimmed" tt="uppercase" px="sm">
            {error !== null ? "Worktrees unavailable" : loading ? "Loading worktrees…" : `Worktrees · ${worktrees.length}`}
          </Text>
          <Box component="div" style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
            <Stack component="nav" gap="xs" aria-label="Worktree chats">
              {worktrees.map((worktree) => {
                const chats = sessions.filter((session) => session.worktreeId === worktree.id);
                const isOpen = !collapsed.has(worktree.id);
                return (
                  <NavLink
                    key={worktree.id}
                    component="button"
                    type="button"
                    label={worktreeLabel(worktree)}
                    description={repositoryNames.get(worktree.repositoryId) ?? worktree.repositoryId}
                    leftSection={<Text span c="dimmed">↳</Text>}
                    rightSection={<Badge size="xs" variant="dot" color={worktree.clean ? "teal" : "orange"}>{worktree.clean ? "Clean" : "Changes"}</Badge>}
                    disableRightSectionRotation
                    opened={isOpen}
                    aria-expanded={isOpen}
                    title={worktree.path}
                    onClick={() => toggle(worktree.id)}
                  >
                    {chats.length === 0 ? (
                      <Text size="xs" c="dimmed" pl="lg" py={4}>No chats</Text>
                    ) : chats.map((session) => (
                      <NavLink
                        key={session.id}
                        component="a"
                        href={`#/sessions/${session.id}`}
                        label={session.displayName}
                        className={session.id === selectedSessionId ? "selected" : undefined}
                        active={session.id === selectedSessionId}
                        aria-current={session.id === selectedSessionId ? "page" : undefined}
                        rightSection={
                          <Text component="time" size="xs" c="dimmed" dateTime={session.updatedAt}>
                            {relativeTime(session.updatedAt, now)}
                          </Text>
                        }
                      />
                    ))}
                  </NavLink>
                );
              })}
              {orphaned.length > 0 && (
                <Box>
                  <NavLink label="Unavailable worktree" leftSection={<Text span c="dimmed">?</Text>} disabled />
                  {orphaned.map((session) => (
                    <NavLink
                      key={session.id}
                      component="a"
                      href={`#/sessions/${session.id}`}
                      label={session.displayName}
                      pl="xl"
                    />
                  ))}
                </Box>
              )}
            </Stack>
          </Box>
          <Group component="footer" gap="xs" px="sm" pt="xs" style={{ borderTop: "1px solid var(--mantine-color-default-border)" }}>
            <Badge variant="light" color="gray">{sessions.length} {sessions.length === 1 ? "task" : "tasks"}</Badge>
          </Group>
        </Stack>
      </Box>
    </FocusTrap>
  );
}
