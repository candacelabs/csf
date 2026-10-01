import { useRef, useState } from "react";
import { Alert, Badge, Button, Card, Code, Group, Stack, Text, Title } from "@mantine/core";
import { api, describeAmbiguousMutation, describeError } from "../api/client";
import type { SessionRequest } from "../api/client";
import { summarizeRequestPrompt } from "../format";

export type RequestsPanelProps = {
  sessionId: string;
  requests: SessionRequest[];
  onResolved: () => void;
};

export function requestElementId(requestId: string): string {
  return `permission-request-${requestId}`;
}

// The exact-identity permission queue. Both decisions are closed OpenAPI enum
// values and carry no free-form fields.
export function RequestsPanel({ sessionId, requests, onResolved }: RequestsPanelProps) {
  const [failure, setFailure] = useState<string | null>(null);
  const [resolving, setResolving] = useState<Set<string>>(() => new Set());
  const resolvingRef = useRef(new Set<string>());

  async function resolve(request: SessionRequest, decision: "approve" | "deny") {
    if (resolvingRef.current.has(request.id)) return;
    resolvingRef.current.add(request.id);
    setResolving(new Set(resolvingRef.current));
    setFailure(null);
    try {
      const { error } = await api.POST("/v1/sessions/{sessionId}/requests/{requestId}/resolve", {
        params: { path: { sessionId, requestId: request.id } },
        body: { decision },
      });
      if (error !== undefined) {
        setFailure(describeError(error));
        return;
      }
      onResolved();
    } catch (cause) {
      setFailure(describeAmbiguousMutation("Resolving the request", cause));
    } finally {
      resolvingRef.current.delete(request.id);
      setResolving(new Set(resolvingRef.current));
    }
  }

  const pending = requests.filter((request) => request.status === "pending");
  if (pending.length === 0) return null;

  return (
    <Card component="section" className="attention" role="region" aria-label="Pending requests" withBorder radius="md" p="sm" w="calc(100% - 1rem)" maw={760} mx="auto" mb="sm" style={{ flex: "0 0 auto", maxHeight: "30vh", overflowY: "auto" }}>
      <Stack gap="sm">
        <Title order={6} component="h2">Waiting on you</Title>
        {failure !== null && <Alert color="red" variant="light" role="alert">{failure}</Alert>}
        {pending.map((request) => {
          const summary = summarizeRequestPrompt(request.prompt);
          return (
            <Card
              key={request.id}
              id={requestElementId(request.id)}
              component="article"
              tabIndex={-1}
              className="request"
              aria-label={request.toolName === undefined ? "Pending request" : `Pending request for ${request.toolName}`}
              withBorder
              radius="sm"
              p="sm"
            >
              <Stack gap="xs">
                <Group gap="xs">
                  <Badge color={request.kind === "permission" ? "orange" : "blue"} variant="light">{request.kind}</Badge>
                  {request.toolName !== undefined && <Code>{request.toolName}</Code>}
                </Group>
                <Text component="p" className="request-prompt" ff="monospace" size="sm">{summary.headline}</Text>
                {summary.detail !== null && (
                  <details className="request-detail">
                    <summary>Full request</summary>
                    <Code block component="pre">{summary.detail}</Code>
                  </details>
                )}
                <Group justify="flex-end" gap="xs">
                  <Button loading={resolving.has(request.id)} onClick={() => void resolve(request, "approve")}>Approve</Button>
                  <Button type="button" variant="default" disabled={resolving.has(request.id)} onClick={() => void resolve(request, "deny")}>
                    Deny
                  </Button>
                </Group>
              </Stack>
            </Card>
          );
        })}
      </Stack>
    </Card>
  );
}
