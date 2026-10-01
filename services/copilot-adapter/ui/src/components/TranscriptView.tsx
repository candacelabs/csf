import { useEffect, useRef, useState } from "react";
import { useReducedMotion } from "@mantine/hooks";
import { Accordion, Avatar, Badge, Box, Button, Card, Code, Group, Loader, Paper, Stack, Text, ThemeIcon, Title } from "@mantine/core";
import type { SessionRequest } from "../api/client";
import { Markdown } from "../markdown";
import { absoluteTime, authorLabel, initials, relativeTime } from "../format";
import { presentTool } from "../toolPresentation";
import type { TranscriptEntry } from "../transcript";

export type ToolCardProps = {
  entry: Extract<TranscriptEntry, { kind: "tool" }>;
  now: Date;
  pendingApproval?: SessionRequest;
  onReviewRequest?: (requestId: string) => void;
};

function pendingToolApproval(entry: ToolCardProps["entry"], requests: SessionRequest[]): SessionRequest | undefined {
  if (entry.result !== undefined || entry.turnId === undefined || entry.toolCallId === undefined) return undefined;
  return requests.find((request) => {
    if (request.kind !== "permission" || request.status !== "pending" || request.turnId !== entry.turnId) return false;
    try {
      const prompt: unknown = JSON.parse(request.prompt);
      return typeof prompt === "object" && prompt !== null && "toolCallId" in prompt && prompt.toolCallId === entry.toolCallId;
    } catch {
      return false;
    }
  });
}

// One tool invocation: the call and its result paired into a single card, shut
// by default so a long transcript reads as a conversation rather than a log.
export function ToolCard({ entry, now, pendingApproval, onReviewRequest }: ToolCardProps) {
  const [open, setOpen] = useState(false);
  const reducedMotion = useReducedMotion();
  const view = presentTool(entry);
  const statusColor = pendingApproval !== undefined ? "orange" : view.status === "done"
    ? "teal"
    : view.status === "failed" || view.status === "aborted" ? "red" : "blue";
  const statusClass = pendingApproval !== undefined ? "tool-status-running" : view.status === "done"
    ? "tool-status-done"
    : view.status === "failed" || view.status === "aborted" ? "tool-status-terminal" : "tool-status-running";

  return (
    <Card component="li" className={open ? "tool-card open" : "tool-card"} withBorder radius="md" padding={0}>
      <Accordion value={open ? "invocation" : null} onChange={(value) => setOpen(value !== null)} transitionDuration={reducedMotion ? 0 : 180}>
        <Accordion.Item value="invocation">
          <Accordion.Control className="tool-head">
            <Group gap="sm" wrap="nowrap" w="100%">
              <Code>{entry.toolName}</Code>
              <Text size="sm" truncate flex={1} title={view.summary}>{view.summary}</Text>
              <Badge className={`tool-status ${statusClass}`} color={statusColor} variant="light" size="sm">
                {pendingApproval !== undefined ? "Waiting for approval" : view.exitCode === undefined ? view.status : `${view.status} · exit ${view.exitCode}`}
              </Badge>
              <Text size="xs" c="dimmed" title={absoluteTime(entry.occurredAt)}>{relativeTime(entry.occurredAt, now)}</Text>
            </Group>
          </Accordion.Control>
          {pendingApproval !== undefined && onReviewRequest !== undefined && (
            <Group px="md" pb="xs">
              <Button type="button" variant="light" color="orange" size="xs" onClick={() => onReviewRequest(pendingApproval.id)}>
                Review approval
              </Button>
            </Group>
          )}
          <Accordion.Panel className="tool-body">
            <Stack gap="xs">
              {view.description !== undefined && <Text size="sm">{view.description}</Text>}
              <Text size="xs" fw={700} c="dimmed" tt="uppercase">{view.command === undefined ? "Arguments" : "Command"}</Text>
              <Code block className="tool-command">{view.command ?? (view.argumentsText || "(none)")}</Code>
              {view.cwd !== undefined && <Text size="xs" c="dimmed">Working directory <Code>{view.cwd}</Code></Text>}
              {entry.result !== undefined && (
                <>
                  <Text size="xs" fw={700} c="dimmed" tt="uppercase">{view.command === undefined ? "Result" : "Output"}</Text>
                  <Code block className="tool-output" mah={320} tabIndex={0} aria-label="Tool output">{view.resultText || "No output."}</Code>
                </>
              )}
              {entry.result === undefined && (
                <Text size="sm" c={pendingApproval !== undefined ? "orange" : "dimmed"}>
                  {pendingApproval !== undefined
                    ? "Approve or deny the pending request below to continue."
                    : view.status === "running"
                    ? "Waiting for the tool result…"
                    : "The turn ended without a recorded tool result."}
                </Text>
              )}
              <details className="tool-inspector">
                <summary>Raw tool data</summary>
                {entry.toolCallId !== undefined && <Text size="xs" c="dimmed">Call ID <Code>{entry.toolCallId}</Code></Text>}
                <Text size="xs" fw={700} c="dimmed" tt="uppercase">Original arguments</Text>
                <Code block>{entry.args || "(none)"}</Code>
                {entry.result !== undefined && <><Text size="xs" fw={700} c="dimmed" tt="uppercase">Original result</Text><Code block>{entry.result}</Code></>}
              </details>
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Card>
  );
}

export type MessageBubbleProps = {
  entry: Extract<TranscriptEntry, { kind: "message" }>;
  now: Date;
  streamingActive?: boolean;
};

// One message. Assistant text renders as Markdown; a user's or the system's
// stays literal, because neither is written to be formatted.
export function MessageBubble({ entry, now, streamingActive = true }: MessageBubbleProps) {
  const label = authorLabel(entry.role, entry.author);
  return (
    <Paper
      component="li"
      className={`bubble bubble-${entry.role}`}
      withBorder={entry.role === "user"}
      bg={entry.role === "user" ? "gray.0" : "transparent"}
      p={entry.role === "user" ? "sm" : 0}
      radius="md"
      style={{ alignSelf: entry.role === "user" ? "flex-end" : "flex-start", maxWidth: entry.role === "user" ? "78%" : "92%" }}
    >
      <Group gap="xs" mb={6} wrap="nowrap">
        <Avatar size="sm" radius="sm" color={entry.role === "assistant" ? "teal" : "gray"} variant="light" aria-hidden="true">
          {initials(label)}
        </Avatar>
        <Text size="xs" fw={700} c="dimmed" tt="uppercase">{label}</Text>
        {entry.streaming && streamingActive && <Group gap={4} wrap="nowrap"><Loader type="dots" size="xs" color="orange" /><Text size="xs" c="orange">thinking</Text></Group>}
        <Text ml="auto" size="xs" c="dimmed" title={absoluteTime(entry.occurredAt)}>
          {relativeTime(entry.occurredAt, now)}
        </Text>
      </Group>
      {entry.role === "assistant" ? (
        <Markdown source={entry.text} />
      ) : (
        <Text className="literal" component="p" m={0} style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>{entry.text}</Text>
      )}
    </Paper>
  );
}

export type TranscriptViewProps = {
  entries: TranscriptEntry[];
  now: Date;
  requests?: SessionRequest[];
  onReviewRequest?: (requestId: string) => void;
  streamingActive?: boolean;
};

// A turn that only called tools stores an assistant message with no text. The
// reducer keeps it — it is a real row on the wire and its seq is the fold's
// watermark — but an empty bubble on screen reads as a broken one, so the view
// leaves it out. A row still being streamed into is never empty in this sense:
// it is about to have text, and its "thinking" affordance is the point.
export function visibleEntries(entries: TranscriptEntry[], streamingActive = true): TranscriptEntry[] {
  return entries.filter(
    (entry) => entry.kind !== "message" || (entry.streaming && streamingActive) || entry.text.trim() !== "",
  );
}

export function TranscriptView({ entries: all, now, requests = [], onReviewRequest, streamingActive = true }: TranscriptViewProps) {
  const entries = visibleEntries(all, streamingActive);
  const reducedMotion = useReducedMotion();
  // A chat that does not follow its own stream makes the reader chase it. The
  // sentinel at the foot of the list is scrolled into view whenever the row
  // count or the last row's body changes, which covers both a new message and
  // an assistant message growing delta by delta.
  const foot = useRef<HTMLDivElement | null>(null);
  const tail = entries.at(-1);
  const tailSize =
    tail === undefined ? 0 : tail.kind === "message" ? tail.text.length : (tail.result ?? "").length;
  const watermark = `${entries.length}:${tailSize}`;
  useEffect(() => {
    const anchor = foot.current;
    // jsdom has no layout and so implements no scrollIntoView; the spec suite
    // renders this component and would throw on the call.
    if (anchor === null || typeof anchor.scrollIntoView !== "function") return;
    anchor.scrollIntoView({ block: "end", behavior: reducedMotion ? "auto" : "smooth" });
  }, [reducedMotion, watermark]);

  if (entries.length === 0) {
    return (
      <Stack className="empty" align="center" justify="center" gap="sm" maw={760} w="100%" mx="auto" mih={220} ta="center" px="lg">
        <ThemeIcon variant="light" color="teal" size={44} radius="xl">✦</ThemeIcon>
        <Title order={4}>Nothing said yet</Title>
        <Text c="dimmed" maw={500}>
          Send the first prompt below. Everything Copilot says, every tool it runs and every
          permission it asks for shows up here as it happens.
        </Text>
      </Stack>
    );
  }
  return (
    <>
      <Stack component="ol" className="transcript" gap="lg" maw={760} w="100%" mx="auto" px={{ base: "xs", sm: "lg" }}>
        {entries.map((entry) =>
          entry.kind === "message" ? (
            <MessageBubble key={entry.key} entry={entry} now={now} streamingActive={streamingActive} />
          ) : (
            <ToolCard key={entry.key} entry={entry} now={now} pendingApproval={pendingToolApproval(entry, requests)} onReviewRequest={onReviewRequest} />
          ),
        )}
      </Stack>
      <Box ref={foot} aria-hidden="true" maw={760} w="100%" mx="auto" />
    </>
  );
}
