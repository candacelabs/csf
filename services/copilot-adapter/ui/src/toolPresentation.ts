import { summarizeToolArgs } from "./format";
import type { TranscriptEntry } from "./transcript";

type ToolEntry = Extract<TranscriptEntry, { kind: "tool" }>;

// Tool payloads are owned by the CLI and individual MCP tools, not the adapter
// contract. Decode only for presentation; the transcript retains the originals.
function decodePayload(value: unknown): unknown {
  for (let layer = 0; layer < 3 && typeof value === "string"; layer += 1) {
    try {
      value = JSON.parse(value);
    } catch {
      break;
    }
  }
  return value;
}

function objectPayload(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function displayPayload(value: unknown): string {
  if (typeof value === "string") return value;
  return JSON.stringify(value, null, 2) ?? "";
}

function stringField(fields: Record<string, unknown> | null, ...keys: string[]): string | undefined {
  for (const key of keys) {
    const value = fields?.[key];
    if (typeof value === "string" && value !== "") return value;
  }
  return undefined;
}

export function presentTool(entry: ToolEntry) {
  const args = decodePayload(entry.args);
  const fields = objectPayload(args);
  const command = stringField(fields, "command", "cmd", "fullCommandText");
  const description = stringField(fields, "description", "intention");
  const result = decodePayload(entry.result);
  const resultFields = objectPayload(result);
  const output: string[] = [];
  let exitCode: number | undefined;
  let cwd: string | undefined;
  let shellId: string | undefined;
  let failed = resultFields?.isError === true || resultFields?.success === false;

  function appendOutput(text: string) {
    if (text !== "" && !output.includes(text)) output.push(text);
  }

  function readContent(value: unknown): string {
    if (Array.isArray(value)) {
      // Repeated blocks within one representation are real output, not aliases.
      return value.map(readContent).filter((text) => text !== "").join("\n");
    }
    const block = objectPayload(value);
    if (block?.type === "shell_exit") {
      if (typeof block.exitCode === "number") {
        exitCode = block.exitCode;
        failed ||= block.exitCode !== 0;
      }
      cwd = stringField(block, "cwd") ?? cwd;
      shellId = stringField(block, "shellId") ?? shellId;
      return "";
    }
    if (block?.type === "text" && typeof block.text === "string") {
      return displayPayload(decodePayload(block.text));
    }
    return displayPayload(decodePayload(value));
  }

  if (resultFields !== null) {
    let hasContent = false;
    if (typeof resultFields.exitCode === "number") {
      exitCode = resultFields.exitCode;
      failed ||= exitCode !== 0;
    }
    cwd = stringField(resultFields, "cwd");
    for (const key of ["stdout", "stderr", "content", "contents", "detailedContent"]) {
      if (resultFields[key] === undefined) continue;
      hasContent = true;
      const text = readContent(resultFields[key]);
      appendOutput(key === "stderr" && text !== "" ? `stderr:\n${text}` : text);
    }
    if (resultFields.error !== undefined && resultFields.error !== null && resultFields.error !== false) {
      failed = true;
      appendOutput(displayPayload(resultFields.error));
    }
    // Unknown tool shapes remain inspectable in the readable view as well.
    if (!hasContent && output.length === 0 && exitCode === undefined) appendOutput(displayPayload(result));
  } else if (entry.result !== undefined) {
    appendOutput(displayPayload(result));
  }

  // The CLI duplicates its shell-exit marker in content and detailedContent.
  // Hide only the exact marker backed by a structured shell_exit receipt.
  const marker = shellId !== undefined && exitCode !== undefined
    ? `<shellId: ${shellId} completed with exit code ${exitCode}>`
    : undefined;
  const resultText = output.map((text) => {
    if (marker === undefined || !text.trimEnd().endsWith(marker)) return text;
    return text.trimEnd().slice(0, -marker.length).trimEnd();
  }).filter((text) => text !== "").join("\n\n");
  const status = failed ? "failed"
    : entry.result !== undefined ? "done"
    : entry.terminalStatus === "completed" ? "no result"
    : entry.terminalStatus ?? "running";

  return {
    command,
    description,
    summary: description ?? command ?? summarizeToolArgs(entry.args),
    argumentsText: displayPayload(args),
    resultText,
    exitCode,
    cwd,
    status,
  };
}
