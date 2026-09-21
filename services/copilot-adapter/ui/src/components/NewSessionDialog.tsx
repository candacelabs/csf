import { useEffect, useMemo, useRef, useState } from "react";
import { useReducedMotion } from "@mantine/hooks";
import { Accordion, Alert, Button, Group, Modal, NativeSelect, Radio, SimpleGrid, Stack, TextInput, Textarea } from "@mantine/core";
import { api, describeError, newClientUUID } from "../api/client";
import type { CreateSessionRequest, Model, Repository, Session, Worktree, WorktreeMode } from "../api/client";
import { ModelPicker } from "./ModelPicker";

type NewSessionDialogProps = {
  repositories: Repository[];
  worktrees: Worktree[];
  models: Model[];
  modelsLoading?: boolean;
  modelsError?: string | null;
  onRefreshModels?: () => void;
  onClose: () => void;
  onCreated: (session: Session) => void;
};

export function NewSessionDialog({
  repositories,
  worktrees,
  models,
  modelsLoading = false,
  modelsError = null,
  onRefreshModels,
  onClose,
  onCreated,
}: NewSessionDialogProps) {
  const [repositoryId, setRepositoryId] = useState(repositories[0]?.id ?? "");
  const [mode, setMode] = useState<WorktreeMode>("newWorktree");
  const [worktreeId, setWorktreeId] = useState("");
  const [model, setModel] = useState(models.length === 1 ? models[0].id : "");
  const [displayName, setDisplayName] = useState("");
  const [baseRef, setBaseRef] = useState("");
  const [systemInstructions, setSystemInstructions] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const submittingRef = useRef(false);
  const attemptRef = useRef<{ signature: string; idempotencyKey: string } | null>(null);
  const returnFocusTo = useRef<HTMLElement | null>(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  const reducedMotion = useReducedMotion();
  const existing = useMemo(
    () => worktrees.filter((worktree) => worktree.repositoryId === repositoryId && worktree.managed && worktree.state === "active"),
    [repositoryId, worktrees],
  );

  useEffect(() => {
    if (!repositories.some((repository) => repository.id === repositoryId)) {
      setRepositoryId(repositories[0]?.id ?? "");
    }
  }, [repositories, repositoryId]);

  useEffect(() => {
    if (!models.some((candidate) => candidate.id === model)) setModel(models.length === 1 ? models[0].id : "");
  }, [model, models]);

  useEffect(() => {
    if (mode !== "reuseExistingWorktree") return;
    setWorktreeId((current) => existing.some((worktree) => worktree.id === current) ? current : (existing[0]?.id ?? ""));
  }, [existing, mode]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (submittingRef.current || modelsLoading || modelsError !== null || !models.some((candidate) => candidate.id === model)) return;
    submittingRef.current = true;
    setFailure(null);
    setSubmitting(true);
    let created: Session | undefined;
    const createBody = (idempotencyKey: string): CreateSessionRequest => {
      const optional = {
        ...(displayName.trim() === "" ? {} : { displayName: displayName.trim() }),
        ...(systemInstructions.trim() === "" ? {} : { systemInstructions: systemInstructions.trim() }),
      };
      switch (mode) {
        case "newWorktree":
          return {
            idempotencyKey, repositoryId, model, worktreeMode: "newWorktree",
            ...(baseRef.trim() === "" ? {} : { baseRef: baseRef.trim() }), ...optional,
          };
        case "reuseExistingWorktree":
          return { idempotencyKey, repositoryId, model, worktreeMode: "reuseExistingWorktree", worktreeId, ...optional };
        case "reuseCurrentWorktree":
          return { idempotencyKey, repositoryId, model, worktreeMode: "reuseCurrentWorktree", ...optional };
      }
    };
    let body: CreateSessionRequest;
    try {
      const signature = JSON.stringify(createBody("00000000-0000-4000-8000-000000000000"));
      if (attemptRef.current === null || attemptRef.current.signature !== signature) {
        attemptRef.current = { signature, idempotencyKey: newClientUUID() };
      }
      body = createBody(attemptRef.current.idempotencyKey);
    } catch (cause) {
      submittingRef.current = false;
      setSubmitting(false);
      setFailure(`The task request could not be prepared. ${describeError(cause)}`);
      return;
    }
    try {
      const { data, error } = await api.POST("/v1/sessions", {
        body,
      });
      if (error !== undefined || data === undefined) {
        setFailure(error === undefined ? "the task could not be created" : describeError(error));
        return;
      }
      created = data;
      attemptRef.current = null;
    } catch (cause) {
      setFailure(`Task creation did not receive a response and may have succeeded. Retry will reuse the same idempotency key. ${describeError(cause)}`);
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
    if (created !== undefined) onCreated(created);
  }

  function close() {
    if (submittingRef.current) return;
    onClose();
    const target = returnFocusTo.current;
    window.setTimeout(() => {
      if (target?.isConnected) target.focus();
    }, 0);
  }

  return (
    <Modal
      opened
      onClose={close}
      title={<Stack gap={0}><span>New task</span><span>Start a focused work session</span></Stack>}
      centered
      size="42rem"
      closeOnEscape={!submitting}
      closeOnClickOutside={!submitting}
      closeButtonProps={{ "aria-label": "Close", disabled: submitting }}
      yOffset="8vh"
      xOffset="sm"
      transitionProps={{ transition: "pop", duration: reducedMotion ? 0 : 180 }}
    >
      <form onSubmit={submit}>
        <Stack gap="md">
          {failure !== null && <Alert color="red" variant="light" role="alert">{failure}</Alert>}
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <TextInput data-autofocus label="Task name" value={displayName} onChange={(event) => setDisplayName(event.currentTarget.value)} placeholder="Optional" />
            <ModelPicker models={models} value={model} onChange={setModel} loading={modelsLoading} error={modelsError} disabled={submitting} onRefresh={onRefreshModels} />
          </SimpleGrid>
          <NativeSelect
            label="Repository"
            required
            value={repositoryId}
            onChange={(event) => setRepositoryId(event.currentTarget.value)}
            data={repositories.map((repository) => ({ value: repository.id, label: repository.displayName }))}
          />
          <Radio.Group label="Worktree" name="worktree-mode" value={mode} onChange={(value) => setMode(value as WorktreeMode)}>
            <Stack mt="xs" gap="xs">
              <Radio value="newWorktree" label="New isolated worktree" description="Default · keeps this task separate from other work" />
              <Radio value="reuseExistingWorktree" label="Existing managed worktree" description={existing.length === 0 ? "No active managed worktrees for this repository" : "Continue work already in progress"} disabled={existing.length === 0} />
              <Radio value="reuseCurrentWorktree" label="Configured checkout" description="Run directly in the repository root" />
            </Stack>
          </Radio.Group>
          {mode === "newWorktree" && <TextInput label="Base ref" value={baseRef} onChange={(event) => setBaseRef(event.currentTarget.value)} placeholder="Repository default" />}
          {mode === "reuseExistingWorktree" && (
            <NativeSelect
              label="Managed worktree"
              required
              value={worktreeId}
              onChange={(event) => setWorktreeId(event.currentTarget.value)}
              data={existing.map((worktree) => ({ value: worktree.id, label: worktree.branch || worktree.path }))}
            />
          )}
          <Accordion variant="separated" radius="md">
            <Accordion.Item value="task-instructions">
              <Accordion.Control>Task instructions</Accordion.Control>
              <Accordion.Panel>
                <Textarea label="System instructions" rows={4} value={systemInstructions} onChange={(event) => setSystemInstructions(event.currentTarget.value)} placeholder="Optional task-specific guidance" />
              </Accordion.Panel>
            </Accordion.Item>
          </Accordion>
          <Group justify="flex-end">
            <Button type="button" variant="default" disabled={submitting} onClick={close}>Cancel</Button>
            <Button type="submit" loading={submitting} disabled={submitting || modelsLoading || modelsError !== null || repositoryId === "" || !models.some((candidate) => candidate.id === model) || (mode === "reuseExistingWorktree" && worktreeId === "")}>
              {submitting ? "Starting…" : "Start task"}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
}
