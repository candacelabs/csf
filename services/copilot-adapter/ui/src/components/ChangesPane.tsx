import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, describeError } from "../api/client";
import type { GitChange, WorktreeChanges } from "../api/client";
import { PaneLoading } from "./PaneLoading";

type ChangesPaneProps = { worktreeId: string; revision: number };

function statusOf(change: GitChange): string {
  if (change.worktreeState !== "unmodified") return change.worktreeState;
  return change.indexState;
}

function markerOf(state: string): string {
  if (state === "added" || state === "untracked") return "A";
  if (state === "deleted") return "D";
  if (state === "renamed") return "R";
  if (state === "copied") return "C";
  if (state === "unmerged") return "U";
  return "M";
}

function decodeGitPathToken(header: string, start: number): { path: string; next: number } | null {
  let index = start;
  while (header[index] === " ") index += 1;
  if (index >= header.length) return null;
  if (header[index] !== '"') {
    const end = header.indexOf(" ", index);
    return { path: header.slice(index, end < 0 ? header.length : end), next: end < 0 ? header.length : end };
  }
  index += 1;
  const bytes: number[] = [];
  const encoder = new TextEncoder();
  const escapes: Record<string, number> = {
    a: 0x07, b: 0x08, t: 0x09, n: 0x0a, v: 0x0b, f: 0x0c, r: 0x0d,
    '"': 0x22, "\\": 0x5c,
  };
  while (index < header.length) {
    const current = header[index];
    if (current === '"') return { path: new TextDecoder().decode(new Uint8Array(bytes)), next: index + 1 };
    if (current === "\\") {
      index += 1;
      const escaped = header[index];
      if (escaped === undefined) return null;
      if (/[0-7]/.test(escaped)) {
        let octal = escaped;
        while (octal.length < 3 && /[0-7]/.test(header[index + 1] ?? "")) {
          index += 1;
          octal += header[index];
        }
        bytes.push(Number.parseInt(octal, 8));
      } else if (escapes[escaped] !== undefined) {
        bytes.push(escapes[escaped]);
      } else {
        bytes.push(...encoder.encode(escaped));
      }
      index += 1;
      continue;
    }
    const codePoint = header.codePointAt(index);
    if (codePoint === undefined) return null;
    const character = String.fromCodePoint(codePoint);
    bytes.push(...encoder.encode(character));
    index += character.length;
  }
  return null;
}

function stripDiffSide(path: string): string {
  return path.startsWith("a/") || path.startsWith("b/") ? path.slice(2) : path;
}

function pathsFromDiffHeader(section: string): Array<[string, string]> {
  const newline = section.indexOf("\n");
  const header = section.slice(0, newline < 0 ? section.length : newline).replace(/\r$/, "");
  const prefix = "diff --git ";
  if (!header.startsWith(prefix)) return [];
  const payload = header.slice(prefix.length);
  if (payload.startsWith('"')) {
    const left = decodeGitPathToken(header, prefix.length);
    if (left === null) return [];
    let rightStart = left.next;
    while (header[rightStart] === " ") rightStart += 1;
    const right = header[rightStart] === '"'
      ? decodeGitPathToken(header, rightStart)
      : { path: header.slice(rightStart), next: header.length };
    if (right === null || header.slice(right.next).trim() !== "") return [];
    return [[stripDiffSide(left.path), stripDiffSide(right.path)]];
  }
  const quotedRight = payload.lastIndexOf(' "');
  if (quotedRight >= 0) {
    const right = decodeGitPathToken(header, prefix.length + quotedRight + 1);
    if (right === null || header.slice(right.next).trim() !== "") return [];
    return [[stripDiffSide(payload.slice(0, quotedRight)), stripDiffSide(right.path)]];
  }
  const pairs: Array<[string, string]> = [];
  let marker = payload.indexOf(" b/");
  while (marker >= 0) {
    pairs.push([stripDiffSide(payload.slice(0, marker)), stripDiffSide(payload.slice(marker + 1))]);
    marker = payload.indexOf(" b/", marker + 1);
  }
  return pairs.filter(([left, right]) => left === right);
}

function pathsFromRenameOrCopy(section: string): string[] {
  const prefixes = ["rename to ", "copy to "];
  const paths: string[] = [];
  for (const line of section.split("\n")) {
    const prefix = prefixes.find((candidate) => line.startsWith(candidate));
    if (prefix === undefined) continue;
    const value = line.slice(prefix.length).replace(/\r$/, "");
    if (!value.startsWith('"')) {
      paths.push(value);
      continue;
    }
    const decoded = decodeGitPathToken(value, 0);
    if (decoded !== null && value.slice(decoded.next).trim() === "") paths.push(decoded.path);
  }
  return paths;
}

function patchForPath(patch: string, selectedPath: string): string {
  const sections = patch.split(/(?=^diff --git )/m);
  return sections.find((section) => {
    const renameOrCopyPaths = pathsFromRenameOrCopy(section);
    if (renameOrCopyPaths.length > 0) return renameOrCopyPaths.includes(selectedPath);
    return pathsFromDiffHeader(section).some((paths) => paths[1] === selectedPath);
  }) ?? "";
}

export function ChangesPane({ worktreeId, revision }: ChangesPaneProps) {
  const [changes, setChanges] = useState<WorktreeChanges | null>(null);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const loadVersion = useRef(0);

  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true);
    try {
      const { data, error } = await api.GET("/v1/worktrees/{worktreeId}/changes", {
        params: { path: { worktreeId } },
      });
      if (version !== loadVersion.current) return;
      if (error !== undefined || data === undefined) {
        setFailure(error === undefined ? "changes could not be read" : describeError(error));
        return;
      }
      setChanges(data);
      setFailure(null);
    } catch (cause) {
      if (version === loadVersion.current) {
        setFailure(`Changes could not be refreshed: ${describeError(cause)}. Retry when the connection is available.`);
      }
    } finally {
      if (version === loadVersion.current) setLoading(false);
    }
  }, [worktreeId]);

  useEffect(() => {
    void load();
    return () => { loadVersion.current += 1; };
  }, [load, revision]);

  const patch = useMemo(() => {
    if (changes === null || selectedPath === null) return changes?.patch ?? "";
    return patchForPath(changes.patch, selectedPath);
  }, [changes, selectedPath]);

  if (loading && changes === null) return <PaneLoading label="Reading git changes" />;
  return (
    <div className="changes-pane pane-content">
      <header className="pane-toolbar">
        <div>
          <strong>{changes?.files.length ?? 0} changed files</strong>
          <span className="pane-subtitle">{changes?.clean ? "Working tree clean" : changes?.headSha.slice(0, 8)}</span>
        </div>
        <button type="button" className="ghost" disabled={loading} onClick={() => void load()}>{loading ? "Refreshing…" : "↻ Refresh"}</button>
      </header>
      {failure !== null && <p className="error" role="alert">{failure}</p>}
      {changes?.files.length === 0 ? (
        <div className="pane-empty"><span aria-hidden="true">✓</span><strong>No changes</strong><small>This worktree matches HEAD.</small></div>
      ) : (
        <div className="changes-layout">
          <ul className="file-list" aria-label="Changed files">
            <li>
              <button type="button" className={selectedPath === null ? "selected" : ""} onClick={() => setSelectedPath(null)}>
                <span className="file-state summary">Σ</span><span>All changes</span>
              </button>
            </li>
            {changes?.files.map((file) => {
              const status = statusOf(file);
              return (
                <li key={file.path}>
                  <button type="button" className={selectedPath === file.path ? "selected" : ""} onClick={() => setSelectedPath(file.path)} title={file.path}>
                    <span className={`file-state ${status}`}>{markerOf(status)}</span>
                    <span>{file.path}</span>
                  </button>
                </li>
              );
            })}
          </ul>
          <div className="diff-view" aria-label="Git patch">
            {changes?.truncated && <div className="truncated-note">Patch reached the server display limit.</div>}
            <pre>{patch === "" ? "No textual diff for this selection." : patch}</pre>
          </div>
        </div>
      )}
    </div>
  );
}
