// Build-time Mantine source. The Go server fills the generated html/templates;
// these components are never hydrated or mounted by React in the browser.
import { renderToStaticMarkup } from "react-dom/server";
import { Anchor, Badge, Box, Button, Card, Group, MantineProvider, NativeSelect, Paper, SimpleGrid, Table, Stack, Text, TextInput, Title } from "@mantine/core";
import type { ReactNode } from "react";
import { workbenchTheme } from "./theme";

const token = (name: string) => `__KANBAN_${name}__`;
const region = token("Region");

function CardTemplate() {
  return <Card component="article" id={region} data-gotth-region={region} data-kanban-card withBorder radius="md" p="md">
    <Stack gap="sm">
      <Group justify="space-between" wrap="nowrap">
        <Anchor href={`/ui/#/sessions/${token("SessionID")}`} fw={600}>{token("Name")}</Anchor>
        {token("ifCanMove")}<Button type="button" variant="subtle" color="gray" size="compact-xs" data-kanban-drag aria-label={`Drag ${token("Name")} to another task status`} title="Drag to a task status column; keyboard users can use Move task below">⠿</Button>{token("else")}<Button type="button" variant="subtle" color="gray" size="compact-xs" disabled aria-label="Link a task with a current checkpoint before dragging">⠿</Button>{token("end")}
      </Group>
      <Group gap="xs"><Badge variant="light">Session: {token("Status")}</Badge><Badge color="gray" variant="light">{token("TaskStatus")}</Badge></Group>
      <Text size="xs" c="dimmed">{token("Model")} · {token("Worktree")}</Text>
      <Text size="sm">{token("Condition")}</Text>
      <Text size="xs">Owner: {token("Owner")}</Text>
      <Text size="sm">Next: {token("NextAction")}</Text>
      <Group gap="sm">
        {token("ifTask")}<Anchor size="xs" href={token("TaskURL")} target="_blank" rel="noreferrer">Task issue ↗</Anchor>{token("end")}
        {token("ifCheckpoint")}<Anchor size="xs" href={token("CheckpointURL")} target="_blank" rel="noreferrer">Checkpoint receipt ↗</Anchor>{token("end")}
        {token("ifEvidence")}<Anchor size="xs" href={token("EvidenceURL")} target="_blank" rel="noreferrer">Evidence ↗</Anchor>{token("end")}
      </Group>
      <Box component="details"><Text component="summary" size="sm" fw={600}>Link a task</Text>
        <Stack component="form" mt="sm" gap="xs" data-gotth-on="submit:kanban.link">
          <input type="hidden" name="expected_generation" value={token("LinkGeneration")} />
          <TextInput id={`${region}-task`} name="task_url" label="Task issue URL" type="url" required defaultValue={token("TaskURL")} placeholder="https://github.com/owner/repository/issues/123" />
          <Button type="submit" variant="light" size="xs">Save task link</Button>
        </Stack>
      </Box>
      <Box component="details" data-kanban-move-details><Text component="summary" size="sm" fw={600}>Move task</Text>
        {token("ifCanMove")}<Stack component="form" mt="sm" gap="xs" data-gotth-on="submit:kanban.move" data-kanban-move>
          <input type="hidden" name="expected_checkpoint" value={token("CheckpointID")} />
          <NativeSelect id={`${region}-status`} name="status" label="Task status">{token("rangeStatuses")}<option value={token("Value")} data-kanban-selected={token("Selected")}>{token("Label")}</option>{token("end")}</NativeSelect>
          <TextInput id={`${region}-next`} name="next_action" label="Next action" required defaultValue={token("NextAction")} />
          <TextInput id={`${region}-reason`} name="reason" label="Reason or blocker" placeholder="Required when blocked or waiting for an operator" />
          <Button type="submit" size="xs">Move task</Button>
        </Stack>{token("else")}<Text size="xs" c="dimmed" mt="sm">Link a task with a current checkpoint before moving it.</Text>{token("end")}
      </Box>
      <Button type="button" variant="subtle" size="compact-xs" data-gotth-on="click:kanban.refresh">Refresh task checkpoint</Button>
    </Stack>
  </Card>;
}

function BoardTemplate() {
  return <Stack id={region} data-gotth-region={region} gap="md">
    {token("ifError")}<Paper withBorder p="sm" radius="md" role="alert" c="red">{token("Error")}</Paper>{token("end")}
    <Group justify="space-between"><Text size="sm" c="dimmed">Task checkpoints are last observed. Refresh to read external GitHub changes.</Text><Button type="button" variant="default" size="xs" data-gotth-on="click:kanban.refresh">Refresh task checkpoints</Button></Group>
    <Text size="sm" c="dimmed">Drag linked cards between task statuses, or use Move task. Moves are confirmed by a saved checkpoint. Session runtime status stays separate.</Text>
    <Table.ScrollContainer type="native" minWidth={1800}><SimpleGrid cols={6} spacing="sm" miw={1800}>
      {token("rangeColumns")}<Paper component="section" withBorder p="sm" radius="md" bg="gray.0" aria-label={token("Title")}>
        <Group justify="space-between" mb="sm"><Title order={2} size="h4">{token("Title")}</Title><Badge variant="light" color="gray">{token("Count")}</Badge></Group>
        <Stack id={token("ID")} data-kanban-column={token("ID")} gap="sm" mih={120}>{token("Cards")}</Stack>
      </Paper>{token("end")}
    </SimpleGrid></Table.ScrollContainer>
  </Stack>;
}

const directives: Record<string, string> = {
  ifTask: "{{if .TaskURL}}", ifCheckpoint: "{{if .CheckpointURL}}", ifEvidence: "{{if .EvidenceURL}}",
  ifCanMove: "{{if .CanMove}}", ifError: "{{if .Error}}", else: "{{else}}", end: "{{end}}",
  rangeColumns: "{{range .Columns}}", rangeStatuses: "{{range .Statuses}}",
};

export function renderKanbanTemplates(): Record<string, string> {
  const render = (content: ReactNode) => {
    let html = renderToStaticMarkup(<MantineProvider theme={workbenchTheme} forceColorScheme="light" withCssVariables={false} withGlobalClasses={false}>{content}</MantineProvider>);
    html = html.replace(`data-kanban-selected="${token("Selected")}"`, "{{if .Selected}}selected{{end}}");
    html = html.replace(/__KANBAN_([A-Za-z][A-Za-z0-9]*)__/g, (_match: string, name: string) => directives[name] ?? `{{.${name}}}`);
    return `<!-- Code generated by ui/scripts/gen-kanban.mjs from Mantine components. DO NOT EDIT. -->\n${html}\n`;
  };
  return { "card_cgen.html": render(<CardTemplate />), "board_cgen.html": render(<BoardTemplate />) };
}
