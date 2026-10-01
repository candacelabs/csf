import { useCallback, useEffect, useRef, useState } from "react";
import { api, describeAmbiguousMutation, describeError, newClientUUID } from "../api/client";
import type { ChatSchedule, ChatScheduleStatus } from "../api/client";
import { absoluteTime } from "../format";
import { PaneLoading } from "./PaneLoading";

type ScheduleDraft = {
  displayName: string;
  prompt: string;
  cronExpression: string;
  timezone: string;
};

const freshDraft = (): ScheduleDraft => ({
  displayName: "",
  prompt: "",
  cronExpression: "0 9 * * 1-5",
  timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
});

function draftOf(schedule: ChatSchedule): ScheduleDraft {
  return {
    displayName: schedule.displayName,
    prompt: schedule.prompt,
    cronExpression: schedule.cronExpression,
    timezone: schedule.timezone,
  };
}

function ambiguousCreateFailure(cause: unknown): string {
  return `Saving the schedule did not receive a definitive result and may have succeeded. The draft is locked; retrying uses the same request identity. ${describeError(cause)}`;
}

export function SchedulesPane({ sessionId }: { sessionId: string }) {
  const [schedules, setSchedules] = useState<ChatSchedule[]>([]);
  const [draft, setDraft] = useState<ScheduleDraft>(freshDraft);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [createIdempotencyKey, setCreateIdempotencyKey] = useState<string | null>(null);
  const [createAmbiguous, setCreateAmbiguous] = useState(false);
  const [showForm, setShowForm] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [busyScheduleId, setBusyScheduleId] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const savingRef = useRef(false);
  const busyScheduleRef = useRef<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const { data, error } = await api.GET("/v1/schedules", {});
      if (error !== undefined || data === undefined) {
        setFailure(error === undefined ? "schedules could not be read" : describeError(error));
        return;
      }
      setSchedules(data.data.filter((schedule) => schedule.sessionId === sessionId));
      setFailure(null);
    } catch (cause) {
      setFailure(`Schedules could not be read. ${describeError(cause)}`);
    } finally {
      setLoading(false);
    }
  }, [sessionId]);

  useEffect(() => { void load(); }, [load]);

  function create() {
    setDraft(freshDraft());
    setEditingId(null);
    setCreateIdempotencyKey(newClientUUID());
    setCreateAmbiguous(false);
    setShowForm(true);
  }

  function edit(schedule: ChatSchedule) {
    setDraft(draftOf(schedule));
    setEditingId(schedule.id);
    setCreateIdempotencyKey(null);
    setCreateAmbiguous(false);
    setShowForm(true);
  }

  function closeForm() {
    setShowForm(false);
    setCreateIdempotencyKey(null);
    setCreateAmbiguous(false);
  }

  async function save(event: React.FormEvent) {
    event.preventDefault();
    if (savingRef.current) return;
    savingRef.current = true;
    setSaving(true);
    setFailure(null);
    try {
      if (editingId === null && createIdempotencyKey === null) {
        setFailure("The schedule draft has no request identity. Start a new schedule and try again.");
        return;
      }
      const response = editingId === null
        ? await api.POST("/v1/schedules", { body: { idempotencyKey: createIdempotencyKey!, sessionId, ...draft } })
        : await api.PATCH("/v1/schedules/{scheduleId}", {
            params: { path: { scheduleId: editingId } },
            body: draft,
          });
      if (response.error !== undefined) {
        if (editingId === null && response.response.status >= 500) {
          setCreateAmbiguous(true);
          setFailure(ambiguousCreateFailure(response.error));
        } else {
          setFailure(describeError(response.error));
        }
        return;
      }
      closeForm();
      await load();
    } catch (cause) {
      if (editingId === null) {
        setCreateAmbiguous(true);
        setFailure(ambiguousCreateFailure(cause));
      } else {
        setFailure(describeAmbiguousMutation("Saving the schedule", cause));
      }
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }

  async function setStatus(schedule: ChatSchedule, status: ChatScheduleStatus) {
    if (busyScheduleRef.current !== null) return;
    busyScheduleRef.current = schedule.id;
    setBusyScheduleId(schedule.id);
    setFailure(null);
    try {
      const { error } = await api.PATCH("/v1/schedules/{scheduleId}", {
        params: { path: { scheduleId: schedule.id } },
        body: { status },
      });
      if (error !== undefined) setFailure(describeError(error));
      else await load();
    } catch (cause) {
      setFailure(describeAmbiguousMutation("Updating the schedule", cause));
    } finally {
      busyScheduleRef.current = null;
      setBusyScheduleId(null);
    }
  }

  async function remove(scheduleId: string) {
    if (confirmDelete !== scheduleId) {
      setConfirmDelete(scheduleId);
      return;
    }
    if (busyScheduleRef.current !== null) return;
    busyScheduleRef.current = scheduleId;
    setBusyScheduleId(scheduleId);
    setFailure(null);
    try {
      const { error } = await api.DELETE("/v1/schedules/{scheduleId}", {
        params: { path: { scheduleId } },
      });
      if (error !== undefined) setFailure(describeError(error));
      else {
        setConfirmDelete(null);
        await load();
      }
    } catch (cause) {
      setFailure(describeAmbiguousMutation("Deleting the schedule", cause));
    } finally {
      busyScheduleRef.current = null;
      setBusyScheduleId(null);
    }
  }

  if (loading) return <PaneLoading label="Loading schedules" />;
  return (
    <div className="schedules-pane pane-content">
      <header className="pane-toolbar">
        <div><strong>Scheduled tasks</strong><span className="pane-subtitle">Recurring prompts use Candace cron</span></div>
        <button type="button" className="small-button" onClick={create}>＋ New schedule</button>
      </header>
      {failure !== null && <p className="error" role="alert">{failure} <button type="button" className="ghost" onClick={() => void load()}>Refresh schedules</button></p>}
      {showForm && (
        <form className="schedule-form" onSubmit={save}>
          <header><strong>{editingId === null ? "New schedule" : "Edit schedule"}</strong><button type="button" className="icon-button" aria-label="Close schedule form" onClick={closeForm}>×</button></header>
          <div className="form-grid two-columns">
            <label className="field">Name<input required disabled={createAmbiguous} value={draft.displayName} onChange={(event) => setDraft({ ...draft, displayName: event.target.value })} placeholder="Morning review" /></label>
            <label className="field">Time zone<input required disabled={createAmbiguous} value={draft.timezone} onChange={(event) => setDraft({ ...draft, timezone: event.target.value })} placeholder="America/Chicago" /></label>
          </div>
          <label className="field">Cron expression<input className="mono" required disabled={createAmbiguous} value={draft.cronExpression} onChange={(event) => setDraft({ ...draft, cronExpression: event.target.value })} aria-describedby="cron-hint" /><small id="cron-hint">Five fields: minute, hour, day of month, month, day of week.</small></label>
          <label className="field">Prompt<textarea required disabled={createAmbiguous} rows={4} value={draft.prompt} onChange={(event) => setDraft({ ...draft, prompt: event.target.value })} placeholder="Review open work and continue the highest-priority item." /></label>
          <footer><button type="button" className="secondary" onClick={closeForm}>Cancel</button><button type="submit" disabled={saving}>{saving ? "Saving…" : createAmbiguous ? "Retry exact create" : "Save schedule"}</button></footer>
        </form>
      )}
      {schedules.length === 0 && !showForm ? (
        <div className="pane-empty"><span aria-hidden="true">◷</span><strong>Nothing scheduled</strong><small>Run a prompt automatically without leaving this workbench.</small><button type="button" className="secondary" onClick={create}>Create a schedule</button></div>
      ) : (
        <ul className="schedule-list">
          {schedules.map((schedule) => (
            <li key={schedule.id}>
              <div className="schedule-icon" aria-hidden="true">◷</div>
              <div className="schedule-copy">
                <div><strong>{schedule.displayName}</strong><span className={`state-badge ${schedule.status}`}>{schedule.status}</span></div>
                <p>{schedule.prompt}</p>
                <small><code>{schedule.cronExpression}</code> · {schedule.timezone}</small>
                <small>{schedule.nextRunAt === undefined ? "No next run while paused" : `Next ${absoluteTime(schedule.nextRunAt)}`}</small>
                {schedule.lastRunStatus !== undefined && <small className={`run-status ${schedule.lastRunStatus}`}>Last run: {schedule.lastRunStatus}{schedule.lastError === undefined ? "" : ` · ${schedule.lastError}`}</small>}
              </div>
              <div className="schedule-actions">
                <button type="button" className="ghost" disabled={busyScheduleId !== null} onClick={() => edit(schedule)}>Edit</button>
                <button type="button" className="ghost" disabled={busyScheduleId !== null} onClick={() => void setStatus(schedule, schedule.status === "active" ? "paused" : "active")}>{schedule.status === "active" ? "Pause" : "Resume"}</button>
                <button type="button" disabled={busyScheduleId !== null} className={confirmDelete === schedule.id ? "danger confirm" : "ghost danger-text"} onClick={() => void remove(schedule.id)}>{confirmDelete === schedule.id ? "Confirm delete" : "Delete"}</button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
