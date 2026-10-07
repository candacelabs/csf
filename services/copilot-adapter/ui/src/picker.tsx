import { createContext, useContext, useState } from "react";
import { Badge, Button, Card, Group, Stack, Text, Title } from "@mantine/core";
import { safeHref } from "./href";

// An assistant answer may carry a fenced ```csf-picker block holding this JSON.
// The transcript renders it as buttons, and Send returns the choices to the
// same session as an ordinary prompt. The Go bridge's system message
// (copilotbridge.workbenchPickerInstructions) teaches the agent this shape;
// keep the two in step.
export const PICKER_LANGUAGE = "csf-picker";

export type PickerOption = { id: string; label: string; detail?: string; url?: string };
export type PickerSpec = { title: string; options: PickerOption[]; choices: string[] };

const DEFAULT_CHOICES = ["Explore", "Skip"];
const MAX_OPTIONS = 200;
const MAX_CHOICES = 8;

function text(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value.trim() : undefined;
}

// Returns null for anything that is not a complete, well-formed picker, so a
// block still streaming in, or a malformed one, stays visible as code.
export function parsePicker(source: string): PickerSpec | null {
  let raw: unknown;
  try {
    raw = JSON.parse(source);
  } catch {
    return null;
  }
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) return null;
  const record = raw as Record<string, unknown>;
  if (!Array.isArray(record.options) || record.options.length === 0 || record.options.length > MAX_OPTIONS) return null;
  const options: PickerOption[] = [];
  const seen = new Set<string>();
  for (const [index, entry] of record.options.entries()) {
    if (typeof entry === "string" && entry.trim() !== "") {
      options.push({ id: `option-${index}`, label: entry.trim() });
      continue;
    }
    if (typeof entry !== "object" || entry === null) return null;
    const option = entry as Record<string, unknown>;
    const label = text(option.label);
    if (label === undefined) return null;
    const id = text(option.id) ?? `option-${index}`;
    if (seen.has(id)) return null;
    seen.add(id);
    options.push({ id, label, detail: text(option.detail), url: text(option.url) });
  }
  let choices = DEFAULT_CHOICES;
  if (record.choices !== undefined) {
    if (!Array.isArray(record.choices)) return null;
    const named = record.choices.map(text);
    if (named.length < 2 || named.length > MAX_CHOICES || named.some((choice) => choice === undefined)) return null;
    choices = named as string[];
    if (new Set(choices).size !== choices.length) return null;
  }
  return { title: text(record.title) ?? "Choose", options, choices };
}

// The prompt sent back: one line per choice, listing labels in option order,
// so the agent reads the decision without resolving IDs.
export function pickerReply(spec: PickerSpec, decisions: Record<string, string>): string {
  const lines = [`Choices for "${spec.title}":`];
  for (const choice of spec.choices) {
    const picked = spec.options.filter((option) => decisions[option.id] === choice).map((option) => option.label);
    if (picked.length > 0) lines.push(`- ${choice}: ${picked.join("; ")}`);
  }
  const undecided = spec.options.filter((option) => decisions[option.id] === undefined).map((option) => option.label);
  if (undecided.length > 0) lines.push(`- Undecided: ${undecided.join("; ")}`);
  return lines.join("\n");
}

export type PickerHost = { submit: (text: string) => Promise<boolean>; disabled: boolean };

// Supplied by the session page. Without a host (a subagent summary, a test)
// a picker block stays a plain code block.
export const PickerHostContext = createContext<PickerHost | null>(null);

export function usePickerHost(): PickerHost | null {
  return useContext(PickerHostContext);
}

export type PickerProps = { spec: PickerSpec; host: PickerHost };

export function Picker({ spec, host }: PickerProps) {
  const [decisions, setDecisions] = useState<Record<string, string>>({});
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState(false);
  const decided = Object.keys(decisions).length;

  function choose(id: string, choice: string) {
    setSent(false);
    setDecisions((current) => {
      const next = { ...current };
      if (next[id] === choice) delete next[id];
      else next[id] = choice;
      return next;
    });
  }

  function chooseAll(choice: string) {
    setSent(false);
    setDecisions(Object.fromEntries(spec.options.map((option) => [option.id, choice])));
  }

  async function submit() {
    setSending(true);
    try {
      setSent(await host.submit(pickerReply(spec, decisions)));
    } finally {
      setSending(false);
    }
  }

  return (
    <Card className="csf-picker" withBorder radius="md" padding="md" my="sm" aria-label={spec.title} component="section">
      <Group justify="space-between" mb="sm" wrap="wrap" gap="xs">
        <Title order={5}>{spec.title}</Title>
        <Badge variant="light" color={decided === spec.options.length ? "teal" : "gray"}>{decided}/{spec.options.length} decided</Badge>
      </Group>
      <Stack gap="xs" component="ol" p={0} m={0} style={{ listStyle: "none" }}>
        {spec.options.map((option) => {
          const href = option.url === undefined ? null : safeHref(option.url);
          return (
            <Card key={option.id} component="li" withBorder radius="sm" padding="sm">
              <Group justify="space-between" align="flex-start" wrap="wrap" gap="xs">
                <Stack gap={2} style={{ flex: "1 1 260px", minWidth: 0 }}>
                  <Text fw={600} size="sm">
                    {href === null ? option.label : <a href={href} target="_blank" rel="noreferrer noopener">{option.label}</a>}
                  </Text>
                  {option.detail !== undefined && <Text size="sm" c="dimmed">{option.detail}</Text>}
                </Stack>
                <Group gap={6} wrap="wrap" role="group" aria-label={`Decision for ${option.label}`}>
                  {spec.choices.map((choice, index) => {
                    const active = decisions[option.id] === choice;
                    return (
                      <Button
                        key={choice}
                        type="button"
                        size="xs"
                        variant={active ? "filled" : "default"}
                        color={index === 0 ? "teal" : index === 1 ? "gray" : "blue"}
                        aria-pressed={active}
                        onClick={() => choose(option.id, choice)}
                      >
                        {choice}
                      </Button>
                    );
                  })}
                </Group>
              </Group>
            </Card>
          );
        })}
      </Stack>
      <Group mt="md" justify="space-between" wrap="wrap" gap="xs">
        <Group gap={6} wrap="wrap">
          {spec.choices.map((choice) => (
            <Button key={choice} type="button" size="compact-xs" variant="subtle" onClick={() => chooseAll(choice)}>All {choice}</Button>
          ))}
          <Button type="button" size="compact-xs" variant="subtle" color="gray" onClick={() => { setSent(false); setDecisions({}); }}>Clear</Button>
        </Group>
        <Group gap="xs">
          {sent && <Text size="xs" c="teal" role="status">Sent</Text>}
          <Button type="button" color="red" loading={sending} disabled={host.disabled || sending || decided === 0} onClick={() => void submit()}>
            Send choices
          </Button>
        </Group>
      </Group>
    </Card>
  );
}
