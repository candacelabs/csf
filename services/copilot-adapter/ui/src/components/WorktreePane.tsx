import { useCallback, useEffect, useRef, useState } from "react";
import { api, describeError } from "../api/client";
import type { Worktree } from "../api/client";
import { absoluteTime } from "../format";
import { PaneLoading } from "./PaneLoading";

export function WorktreePane({ worktreeId, revision }: { worktreeId: string; revision: number }) {
  const [worktree, setWorktree] = useState<Worktree | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [loading, setLoading] = useState(true);
  const loadVersion = useRef(0);

  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true);
    try {
      const { data, error } = await api.GET("/v1/worktrees/{worktreeId}", {
        params: { path: { worktreeId } },
      });
      if (version !== loadVersion.current) return;
      if (error !== undefined || data === undefined) {
        setFailure(error === undefined ? "the worktree could not be read" : describeError(error));
        return;
      }
      setWorktree(data);
      setFailure(null);
    } catch (cause) {
      if (version === loadVersion.current) {
        setFailure(`Worktree details could not be refreshed: ${describeError(cause)}. Retry when the connection is available.`);
      }
    } finally {
      if (version === loadVersion.current) setLoading(false);
    }
  }, [worktreeId]);

  useEffect(() => {
    void load();
    return () => { loadVersion.current += 1; };
  }, [load, revision]);

  async function copyPath() {
    if (worktree === null) return;
    try {
      if (navigator.clipboard === undefined) throw new Error("Clipboard access is unavailable in this browser");
      await navigator.clipboard.writeText(worktree.path);
      setCopied(true);
    } catch (cause) {
      setFailure(describeError(cause));
    }
  }

  if (loading && worktree === null) return <PaneLoading label="Inspecting worktree" />;
  return (
    <div className="worktree-pane pane-content">
      <header className="pane-toolbar">
        <div><strong>Worktree details</strong><span className="pane-subtitle">Live git metadata</span></div>
        <button type="button" className="ghost" disabled={loading} onClick={() => void load()}>{loading ? "Refreshing…" : "↻ Refresh"}</button>
      </header>
      {failure !== null && <p className="error" role="alert">{failure}</p>}
      {worktree !== null && (
        <div className="metadata-grid">
          <div className="metadata-primary">
            <span className="branch-icon" aria-hidden="true">↳</span>
            <div><span>Branch</span><strong>{worktree.branch || "Detached HEAD"}</strong></div>
            <span className={worktree.clean ? "state-badge clean" : "state-badge changed"}>{worktree.clean ? "Clean" : "Changes"}</span>
          </div>
          <dl>
            <div><dt>Path</dt><dd><code>{worktree.path}</code><button type="button" className="copy-button" onClick={() => void copyPath()}>{copied ? "Copied" : "Copy"}</button></dd></div>
            <div><dt>HEAD</dt><dd><code>{worktree.headSha}</code></dd></div>
            <div><dt>Base ref</dt><dd>{worktree.baseRef}</dd></div>
            <div><dt>Ownership</dt><dd>{worktree.managed ? "Managed by the adapter" : "Configured repository checkout"}</dd></div>
            <div><dt>State</dt><dd>{worktree.state}</dd></div>
            <div><dt>Chats</dt><dd>{worktree.sessionCount}</dd></div>
            <div><dt>Created</dt><dd>{absoluteTime(worktree.createdAt)}</dd></div>
            <div><dt>Last inspected</dt><dd>{absoluteTime(worktree.updatedAt)}</dd></div>
          </dl>
        </div>
      )}
    </div>
  );
}
