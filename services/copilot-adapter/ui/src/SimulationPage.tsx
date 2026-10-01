import { useEffect, useState } from "react";
import { Alert, Anchor, Badge, Button, Card, Code, Group, Image, Loader, Paper, Progress, SimpleGrid, Stack, Table, Text, Textarea, TextInput, Title } from "@mantine/core";
import { csf, csfURL } from "./api/csf";
import type { SimulationRun } from "./api/csf";
import { describeError } from "./api/client";
import { InspectionPage } from "./InspectionPage";
import type { InspectionPageProps } from "./InspectionPage";

function stateLabel(run: SimulationRun) {
  return (run.state ?? "SIMULATION_STATE_UNSPECIFIED").replace("SIMULATION_STATE_", "").toLowerCase().replaceAll("_", " ");
}
function stateColor(run: SimulationRun) {
  return run.state === "SIMULATION_STATE_SUCCEEDED" ? "teal" : run.state === "SIMULATION_STATE_FAILED" ? "red" : "orange";
}
function RunProgress({ run }: { run: SimulationRun }) {
  return <Stack gap={4}>
    <Text size="xs" c="dimmed">{run.completedSteps ?? 0} / {run.steps ?? 0} simulation steps</Text>
    {(run.steps ?? 0) > 0 && <Progress value={Math.min(100, 100 * (run.completedSteps ?? 0) / run.steps!)} color={stateColor(run)} aria-label="Simulation steps completed" />}
  </Stack>;
}

export function SimulationPage({ runId, ...shell }: InspectionPageProps & { runId: string | null }) {
  const [runs, setRuns] = useState<SimulationRun[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [revision, setRevision] = useState(0);
  const [observedAt, setObservedAt] = useState<Date | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    let busy = false;
    async function load() {
      if (busy) return;
      busy = true;
      try {
        const { data, error } = await csf.POST("/api/simulation/list", { body: { limit: 100 }, signal: controller.signal });
        if (controller.signal.aborted) return;
        if (error || !data) throw new Error(error?.message || "Simulation runs could not be read");
        setRuns(data.runs ?? []);
        setError(null);
        setObservedAt(new Date());
      } catch (cause) {
        if (!controller.signal.aborted) setError(describeError(cause));
      } finally {
        busy = false;
        if (!controller.signal.aborted) setLoading(false);
      }
    }
    void load();
    const timer = window.setInterval(() => { if (document.visibilityState === "visible") void load(); }, 10_000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [revision]);
  const selectedId = runId ?? runs[0]?.runId;
  const filtered = runs.filter((run) => `${run.runId} ${run.simulator} ${run.executor} ${stateLabel(run)}`.toLowerCase().includes(query.toLowerCase()));
  return <InspectionPage {...shell} title="Simulation runs" description="Follow a run from simulation steps to screenshots, logs and recorded evidence. Pick a run to look inside."
    actions={<Group><Button variant="light" onClick={() => setRevision((value) => value + 1)}>Refresh runs</Button><Button component="a" variant="default" href={csfURL("/ui/simulations.html")}>Operator controls ↗</Button></Group>}>
    {error && <Alert color="red" title="Runs could not be refreshed">{error}{observedAt && " · Showing the previous snapshot."}</Alert>}
    <Group justify="space-between"><Text size="sm" c="dimmed">{observedAt ? `${runs.length} retained runs · checked ${observedAt.toLocaleTimeString()} · refreshes every 10 seconds while visible` : "Connecting to simulations…"}</Text>
      <TextInput aria-label="Find a simulation" placeholder="Find a run, simulator or state…" value={query} onChange={(event) => setQuery(event.currentTarget.value)} /></Group>
    {loading ? <Loader aria-label="Loading simulation runs" /> : <SimpleGrid cols={{ base: 1, md: 2, xl: 3 }}>
      {filtered.map((run) => <Card withBorder radius="md" p="lg" key={run.runId} bg={selectedId === run.runId ? "orange.0" : "white"}>
        <Stack gap="sm"><Group justify="space-between"><Badge variant="light" color={stateColor(run)}>{stateLabel(run)}</Badge><Text size="xs" c="dimmed">{run.simulator} · {run.executor}</Text></Group>
          <Anchor href={`#/simulations?run=${encodeURIComponent(run.runId ?? "")}`} c="inherit" fw={600} style={{ overflowWrap: "anywhere" }}>{run.runId}</Anchor>
          <RunProgress run={run} />
          <Text size="xs" c="dimmed">{run.updatedAt ? new Date(run.updatedAt).toLocaleString() : "Observation time unavailable"}</Text>
        </Stack>
      </Card>)}
    </SimpleGrid>}
    {!loading && !error && filtered.length === 0 && <Text c="dimmed">{runs.length ? "No runs match this search." : "No simulation runs have been recorded."}</Text>}
    {selectedId && <RunDetails key={selectedId} runId={selectedId} revision={revision} />}
  </InspectionPage>;
}

function RunDetails({ runId, revision }: { runId: string; revision: number }) {
  const [run, setRun] = useState<SimulationRun | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    let busy = false;
    async function inspect() {
      if (busy) return;
      busy = true;
      try {
        const { data, error } = await csf.POST("/api/simulation/inspect", { body: { runId }, signal: controller.signal });
        if (controller.signal.aborted) return;
        if (error || !data?.run) throw new Error(error?.message || "Run details are unavailable");
        setRun(data.run);
        setError(null);
      } catch (cause) {
        if (!controller.signal.aborted) setError(describeError(cause));
      } finally { busy = false; }
    }
    void inspect();
    const timer = window.setInterval(() => { if (document.visibilityState === "visible") void inspect(); }, 10_000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [runId, revision]);
  return <Paper withBorder radius="lg" p={{ base: "md", sm: "xl" }} component="section" aria-label="Selected simulation">
    <Stack gap="lg">
      <Group justify="space-between"><Title order={2} size="h3" style={{ overflowWrap: "anywhere" }}>{runId}</Title>{run && <Badge variant="light" color={stateColor(run)}>{stateLabel(run)}</Badge>}</Group>
      {error && <Alert color="red" title="Inspection unavailable">{error}{run && " · Showing previous details."}</Alert>}
      {!run && !error && <Loader aria-label="Inspecting simulation" />}
      {run && <><RunProgress run={run} />
        <Group>{run.traceUrl && <Button component="a" href={run.traceUrl} target="_blank" rel="noreferrer" variant="light">Open Langfuse trace ↗</Button>}
          <Badge color={run.cleanupConfirmed ? "teal" : "gray"} variant="light">{run.cleanupConfirmed ? "Cleanup confirmed" : "Cleanup not confirmed"}</Badge></Group>
        {[run.reason, run.inspectionError, run.traceExportError, run.logProjectionError].filter(Boolean).map((message) => <Alert color="orange" key={message}>{message}</Alert>)}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>{(run.artifacts ?? []).filter((artifact) => artifact.mediaType === "image/png" && artifact.url).map((artifact) =>
          <Card withBorder radius="md" key={artifact.path}><Anchor href={artifact.url} target="_blank" rel="noreferrer"><Image src={artifact.url} alt={`${runId}: ${artifact.path}`} h={200} fit="contain" loading="lazy" /></Anchor><Text size="xs" mt="sm">{artifact.path}</Text></Card>)}</SimpleGrid>
        {(run.latestMeasurements?.length ?? 0) > 0 && <Table.ScrollContainer minWidth={350}><Table striped><Table.Thead><Table.Tr><Table.Th>Measurement</Table.Th><Table.Th>Value</Table.Th><Table.Th>Simulation step</Table.Th></Table.Tr></Table.Thead><Table.Tbody>
          {run.latestMeasurements!.map((measurement, index) => <Table.Tr key={`${measurement.metric}-${index}`}><Table.Td>{measurement.metric}</Table.Td><Table.Td>{measurement.value ?? 0}</Table.Td><Table.Td>{measurement.step ?? 0}</Table.Td></Table.Tr>)}
        </Table.Tbody></Table></Table.ScrollContainer>}
        <Title order={3} size="h4">Artifacts</Title>
        {(run.artifacts?.length ?? 0) === 0 ? <Text size="sm" c="dimmed">No artifact files reported for this run.</Text> : <Stack gap="xs">{run.artifacts!.map((artifact) => <Group key={artifact.path} justify="space-between" align="start">
          <Stack gap={2}>{artifact.url ? <Anchor href={artifact.url} target="_blank" rel="noreferrer" size="sm" style={{ overflowWrap: "anywhere" }}>{artifact.path}</Anchor> : <Text size="sm">{artifact.path} · URL unavailable</Text>}
          <Code style={{ overflowWrap: "anywhere" }}>{artifact.sha256 || "Hash unavailable"}</Code></Stack><Text size="xs" c="dimmed">{artifact.sizeBytes ?? "0"} bytes</Text>
        </Group>)}</Stack>}
        <RunLogs runId={runId} />
        <Text size="xs" c="dimmed">Bounded simulation evidence. This view does not establish neural training, cross-simulator equivalence or physical-robot safety. AWS execution remains a separate check.</Text>
      </>}
    </Stack>
  </Paper>;
}

function RunLogs({ runId }: { runId: string }) {
  const [content, setContent] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [truncated, setTruncated] = useState(false);
  async function load() {
    setLoading(true);
    try {
      const { data, error } = await csf.POST("/api/simulation/logs", { body: { runId, maxBytes: 65_536 } });
      if (error || !data) throw new Error(error?.message || "Logs are unavailable");
      setContent(data.content ?? ""); setTruncated(data.truncated ?? false); setError(null);
    } catch (cause) { setError(describeError(cause)); } finally { setLoading(false); }
  }
  return <Stack gap="xs"><Group><Title order={3} size="h4">Simulator logs</Title><Button variant="light" size="xs" loading={loading} onClick={() => void load()}>{content === null ? "Read logs" : "Refresh logs"}</Button></Group>
    {error && <Alert color="red">{error}</Alert>}{content !== null && <Textarea readOnly aria-label="Simulator logs" value={content} minRows={6} maxRows={18} autosize styles={{ input: { fontFamily: "monospace" } }} />}
    {truncated && <Text size="xs" c="dimmed">Showing a bounded excerpt; additional log bytes were omitted.</Text>}
  </Stack>;
}
