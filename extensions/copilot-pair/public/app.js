import { ensureRenderer, onRendererReady, renderRichText } from "./renderer.js";

const transcript = document.querySelector("#transcript");
const streaming = document.querySelector("#streaming");
const streamBody = streaming.querySelector(".stream-body");
const pending = document.querySelector("#pending");
const connectionDot = document.querySelector("#connection-dot");
const connectionLabel = document.querySelector("#connection-label");
const sessionIdLabel = document.querySelector("#session-id");
const peerCount = document.querySelector("#peer-count");
const eventCount = document.querySelector("#event-count");
const actorInput = document.querySelector("#actor");
let modelControl = document.querySelector("#model");
const promptInput = document.querySelector("#prompt");
const statusLabel = document.querySelector("#action-status");

// Must match the marker the pair server prepends to the first shared prompt.
const GROUP_PREAMBLE_MARKER = "[Copilot Pair]";
const STATUS_HINT = "Enter to send · Shift+Enter for a new line · Ctrl+Enter to steer mid-turn";

const state = {
  events: new Map(),
  rendered: new Map(),
  toolCards: new Map(),
  permissionRequests: new Map(),
  userInputRequests: new Map(),
  planRequests: new Map(),
  streams: new Map(),
  toolNames: new Map(),
  activeStreamId: undefined,
  sessionId: undefined,
};
const dirty = { transcript: false, pending: false, stream: false };
let lastStreamText;
let streamPending = false;
let statusTimer;

actorInput.value = localStorage.getItem("candace-pair-actor") || "Guest";
actorInput.addEventListener("change", () => {
  localStorage.setItem("candace-pair-actor", actorInput.value.trim() || "Guest");
});

document.querySelector("#send").addEventListener("click", () => sendPrompt("enqueue"));
document.querySelector("#steer").addEventListener("click", () => sendPrompt("immediate"));
document.querySelector("#abort").addEventListener("click", () => runAction({ type: "abort" }, "Stopping…"));
document.querySelector("#change-model").addEventListener("click", () => {
  const model = modelControl.value.trim();
  if (model) {
    void runAction({ type: "model", model }, `Switching to ${model}…`);
  }
});
promptInput.addEventListener("keydown", (event) => {
  if (event.key !== "Enter" || event.shiftKey || event.isComposing) {
    return;
  }
  event.preventDefault();
  void sendPrompt(event.ctrlKey || event.metaKey ? "immediate" : "enqueue");
});

ensureRenderer();
onRendererReady(() => {
  // Re-render everything that was drawn as plain text before the libraries
  // arrived. Tool cards rebuild too, so completions re-merge in event order.
  state.rendered.clear();
  state.toolCards.clear();
  lastStreamText = undefined;
  dirty.transcript = true;
  dirty.pending = true;
  dirty.stream = true;
  render();
});
connect();
void loadModels();

async function loadModels() {
  try {
    const response = await fetch("/api/models");
    if (!response.ok) {
      throw new Error("model catalog unavailable");
    }
    const catalog = await response.json();
    if (!Array.isArray(catalog.models) || catalog.models.length === 0) {
      throw new Error("model catalog empty");
    }
    modelControl.replaceChildren();
    for (const model of catalog.models) {
      const option = document.createElement("option");
      option.value = model.id;
      option.textContent = model.name || model.id;
      modelControl.append(option);
    }
    if (catalog.current) {
      selectModel(catalog.current);
    }
  } catch {
    useModelTextFallback();
  }
}

function selectModel(id) {
  if (modelControl.tagName !== "SELECT") {
    modelControl.value = id;
    return;
  }
  if (![...modelControl.options].some((option) => option.value === id)) {
    const option = document.createElement("option");
    option.value = id;
    option.textContent = id;
    modelControl.append(option);
  }
  modelControl.value = id;
}

function useModelTextFallback() {
  if (modelControl.tagName === "INPUT") {
    return;
  }
  const input = document.createElement("input");
  input.id = "model";
  input.placeholder = "model id";
  input.disabled = modelControl.disabled;
  modelControl.replaceWith(input);
  modelControl = input;
}

function connect() {
  setConnection("connecting", "Connecting");
  const source = new EventSource("/api/events");
  source.addEventListener("open", () => setConnection("live", "Live"));
  source.addEventListener("error", () => setConnection("error", "Reconnecting"));
  source.addEventListener("snapshot", (message) => {
    const snapshot = JSON.parse(message.data);
    if (state.sessionId && state.sessionId !== snapshot.sessionId) {
      // Reconnected to a different Copilot session at the same URL: the old
      // session's events must not leak into the new session's replica.
      resetSessionState();
    }
    state.sessionId = snapshot.sessionId;
    sessionIdLabel.textContent = shortId(snapshot.sessionId);
    const storedReplica = mergeStoredReplica();
    for (const event of snapshot.durableEvents ?? []) {
      mergeEvent(event, true);
    }
    persistReplica();
    dirty.transcript = true;
    dirty.pending = true;
    render();
    void publishReplica(storedReplica);
  });
  source.addEventListener("session-event", (message) => {
    const payload = JSON.parse(message.data);
    mergeEvent(payload.event, payload.durable);
    if (
      payload.event?.type === "session.model_change"
      && typeof payload.event.data?.newModel === "string"
      && payload.event.data.newModel
    ) {
      selectModel(payload.event.data.newModel);
    }
    if (payload.durable) {
      persistReplicaSoon();
    }
    render();
  });
  source.addEventListener("presence", (message) => {
    peerCount.textContent = JSON.parse(message.data).connectedClients ?? 0;
  });
  source.addEventListener("share-stopped", () => {
    source.close();
    setConnection("error", "Share stopped");
    disableControls(true);
  });
}

function resetSessionState() {
  state.events.clear();
  state.rendered.clear();
  state.toolCards.clear();
  state.permissionRequests.clear();
  state.userInputRequests.clear();
  state.planRequests.clear();
  state.streams.clear();
  state.toolNames.clear();
  state.activeStreamId = undefined;
  lastStreamText = undefined;
  dirty.transcript = true;
  dirty.pending = true;
  dirty.stream = true;
}

function mergeEvent(event, durable) {
  if (!event || typeof event.id !== "string") {
    return;
  }
  if (durable && !state.events.has(event.id)) {
    state.events.set(event.id, event);
    if (isVisibleEvent(event)) {
      dirty.transcript = true;
    }
  }

  const requestId = event.data?.requestId;
  switch (event.type) {
    case "assistant.message_delta": {
      const messageId = event.data?.messageId || "active";
      state.activeStreamId = messageId;
      state.streams.set(
        messageId,
        (state.streams.get(messageId) || "") + (event.data?.deltaContent ?? ""),
      );
      dirty.stream = true;
      break;
    }
    case "assistant.message": {
      const messageId = event.data?.messageId;
      if (messageId) state.streams.delete(messageId);
      if (!messageId || state.activeStreamId === messageId) state.activeStreamId = undefined;
      dirty.stream = true;
      break;
    }
    case "session.idle":
      state.streams.clear();
      state.activeStreamId = undefined;
      dirty.stream = true;
      break;
    case "tool.execution_start":
      if (event.data?.toolCallId) {
        state.toolNames.set(event.data.toolCallId, event.data.toolName || "unknown");
      }
      break;
    case "permission.requested":
      if (requestId) {
        state.permissionRequests.set(requestId, event);
        dirty.pending = true;
      }
      break;
    case "permission.completed":
      if (state.permissionRequests.delete(requestId)) dirty.pending = true;
      break;
    case "user_input.requested":
      if (requestId) {
        state.userInputRequests.set(requestId, event);
        dirty.pending = true;
      }
      break;
    case "user_input.completed":
      if (state.userInputRequests.delete(requestId)) dirty.pending = true;
      break;
    case "exit_plan_mode.requested":
      if (requestId) {
        state.planRequests.set(requestId, event);
        dirty.pending = true;
      }
      break;
    case "exit_plan_mode.completed":
      if (state.planRequests.delete(requestId)) dirty.pending = true;
      break;
  }
}

function render() {
  const wasNearBottom = isNearBottom();
  eventCount.textContent = state.events.size;
  if (dirty.transcript) {
    dirty.transcript = false;
    renderTranscript();
  }
  if (dirty.pending) {
    dirty.pending = false;
    renderPendingRequests();
  }
  if (dirty.stream) {
    dirty.stream = false;
    scheduleStreamRender();
  }
  if (wasNearBottom) {
    stickToBottom();
  }
}

function renderTranscript() {
  const events = [...state.events.values()].sort(compareEvents).filter(isVisibleEvent);
  if (events.length === 0) {
    transcript.replaceChildren(document.querySelector("#empty-template").content.cloneNode(true));
    return;
  }
  transcript.replaceChildren(...events.map((event) => {
    let node = state.rendered.get(event.id);
    if (!node) {
      node = renderEvent(event);
      state.rendered.set(event.id, node);
    }
    return node;
  }));
}

// Throttled to one repaint per frame; the timeout fallback keeps streaming
// text updating in windows where requestAnimationFrame is throttled away.
function scheduleStreamRender() {
  if (streamPending) {
    return;
  }
  streamPending = true;
  const run = () => {
    if (!streamPending) {
      return;
    }
    streamPending = false;
    renderStream();
  };
  requestAnimationFrame(run);
  setTimeout(run, 120);
}

function renderStream() {
  const text = currentStreamText();
  if (text === lastStreamText) {
    return;
  }
  lastStreamText = text;
  const wasNearBottom = isNearBottom();
  if (!text) {
    streaming.hidden = true;
    streamBody.replaceChildren();
    return;
  }
  streaming.hidden = false;
  renderRichText(streamBody, text);
  const cursor = document.createElement("span");
  cursor.className = "cursor";
  cursor.textContent = "▍";
  const target = streamBody.lastElementChild?.tagName === "P"
    ? streamBody.lastElementChild
    : streamBody;
  target.append(cursor);
  if (wasNearBottom) {
    stickToBottom();
  }
}

function renderEvent(event) {
  switch (event.type) {
    case "user.message":
      return userMessage(event);
    case "assistant.message":
      return assistantMessage(event);
    case "tool.execution_start":
      return toolStartCard(event);
    case "tool.execution_complete":
      return toolCompletion(event);
    default:
      return errorMessage(event);
  }
}

function userMessage(event) {
  let content = textValue(event.data?.content);
  let seededIntro = false;
  if (content.startsWith(GROUP_PREAMBLE_MARKER)) {
    const separator = content.indexOf("\n\n");
    if (separator > 0) {
      content = content.slice(separator + 2);
      seededIntro = true;
    }
  }
  const attribution = content.match(/^([\w][\w .-]{0,31}):\s([\s\S]*)$/);
  const actor = attribution ? attribution[1].trim() : "Owner";
  const body = attribution ? attribution[2] : content;
  const article = messageShell("user", actor, event.timestamp, attribution ? guestChip(actor) : ownerChip());
  article.append(markdownBody(body));
  if (seededIntro) {
    const note = document.createElement("p");
    note.className = "note";
    note.textContent = "Included the one-time group-chat intro for Copilot";
    article.append(note);
  }
  return article;
}

function assistantMessage(event) {
  const article = messageShell("assistant", "Copilot", event.timestamp, copilotChip());
  article.append(markdownBody(textValue(event.data?.content)));
  return article;
}

function toolStartCard(event) {
  const name = event.data?.toolName ?? "unknown";
  const details = document.createElement("details");
  details.className = "tool-card";

  const summary = document.createElement("summary");
  const title = document.createElement("code");
  title.textContent = name;
  summary.append(title);
  const command = event.data?.shellToolInfo?.displayCommand;
  if (command) {
    const hint = document.createElement("span");
    hint.className = "tool-hint";
    hint.textContent = command;
    summary.append(hint);
  }
  const status = document.createElement("span");
  status.className = "tool-status running";
  status.textContent = "running";
  summary.append(status, timeNode(event.timestamp));

  const body = document.createElement("div");
  body.className = "tool-body";
  const args = compactValue(event.data?.arguments);
  if (args) {
    body.append(toolField("arguments", args));
  }
  details.append(summary, body);

  if (event.data?.toolCallId) {
    state.toolCards.set(event.data.toolCallId, { details, status, body });
  }
  return details;
}

function toolCompletion(event) {
  const failed = event.data?.success === false;
  const output = compactValue(
    event.data?.error?.message
      ?? event.data?.result?.detailedContent
      ?? event.data?.result?.content
      ?? event.data?.error
      ?? event.data?.result,
  );

  const card = state.toolCards.get(event.data?.toolCallId);
  if (card) {
    card.status.textContent = failed ? "failed" : "done";
    card.status.className = `tool-status ${failed ? "failed" : "ok"}`;
    if (output) {
      card.body.append(toolField(failed ? "error" : "result", output));
    }
    if (failed) {
      card.details.open = true;
      card.details.classList.add("failed");
    }
    // The completion merges into the start card, so it adds no node of its own.
    return document.createComment("merged into tool card");
  }

  const details = document.createElement("details");
  details.className = `tool-card${failed ? " failed" : ""}`;
  details.open = failed;
  const summary = document.createElement("summary");
  const title = document.createElement("code");
  title.textContent = state.toolNames.get(event.data?.toolCallId) ?? "unknown";
  const status = document.createElement("span");
  status.className = `tool-status ${failed ? "failed" : "ok"}`;
  status.textContent = failed ? "failed" : "done";
  summary.append(title, status, timeNode(event.timestamp));
  const body = document.createElement("div");
  body.className = "tool-body";
  if (output) {
    body.append(toolField(failed ? "error" : "result", output));
  }
  details.append(summary, body);
  return details;
}

function errorMessage(event) {
  const article = document.createElement("article");
  article.className = "message error";
  const label = document.createElement("p");
  label.className = "message-label";
  const name = document.createElement("span");
  name.className = "actor-name";
  name.textContent = "Session error";
  label.append(name, timeNode(event.timestamp));
  const body = document.createElement("pre");
  body.textContent = event.data?.message ?? compactValue(event.data);
  article.append(label, body);
  return article;
}

function messageShell(kind, name, timestamp, chip) {
  const article = document.createElement("article");
  article.className = `message ${kind}`;
  const label = document.createElement("p");
  label.className = "message-label";
  const nameSpan = document.createElement("span");
  nameSpan.className = "actor-name";
  nameSpan.textContent = name;
  label.append(chip, nameSpan, timeNode(timestamp));
  article.append(label);
  return article;
}

function markdownBody(text) {
  const body = document.createElement("div");
  body.className = "md";
  renderRichText(body, text);
  return body;
}

function copilotChip() {
  const chip = document.createElement("span");
  chip.className = "chip copilot";
  chip.textContent = "✦";
  return chip;
}

function ownerChip() {
  const chip = document.createElement("span");
  chip.className = "chip owner";
  chip.textContent = "❯";
  chip.title = "Typed in the owner's terminal";
  return chip;
}

function guestChip(name) {
  const chip = document.createElement("span");
  chip.className = "chip";
  chip.textContent = name.split(/\s+/).slice(0, 2).map((word) => word[0]).join("").toUpperCase();
  chip.style.background = `hsl(${actorHue(name)} 45% 62%)`;
  return chip;
}

function actorHue(name) {
  let hash = 0;
  for (const ch of name.toLowerCase()) {
    hash = (hash * 31 + ch.codePointAt(0)) % 360;
  }
  return hash;
}

function timeNode(timestamp) {
  const time = document.createElement("time");
  const date = new Date(timestamp ?? "");
  if (!Number.isNaN(date.valueOf())) {
    time.dateTime = date.toISOString();
    time.textContent = date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  return time;
}

function toolField(labelText, text) {
  const field = document.createElement("div");
  const label = document.createElement("p");
  label.className = "field-label";
  label.textContent = labelText;
  const body = document.createElement("pre");
  body.textContent = text;
  field.append(label, body);
  return field;
}

function renderPendingRequests() {
  pending.replaceChildren();
  for (const event of state.permissionRequests.values()) {
    pending.append(permissionCard(event));
  }
  for (const event of state.userInputRequests.values()) {
    pending.append(userInputCard(event));
  }
  for (const event of state.planRequests.values()) {
    pending.append(planCard(event));
  }
}

function permissionCard(event) {
  const card = requestCard("Copilot needs permission");
  const body = document.createElement("pre");
  body.textContent = describePermission(event.data);
  card.append(body);
  const actions = document.createElement("div");
  actions.className = "request-actions";
  actions.append(
    actionButton("Deny", "danger", () => runAction({
      type: "permission",
      requestId: event.data.requestId,
      decision: "deny",
    }, "Denying…")),
    actionButton("Approve once", "primary", () => runAction({
      type: "permission",
      requestId: event.data.requestId,
      decision: "approve",
    }, "Approving…")),
  );
  card.append(actions);
  return card;
}

function userInputCard(event) {
  const card = requestCard("Copilot has a question");
  card.append(markdownBody(textValue(event.data.question ?? "Answer required")));
  const choices = (event.data.choices ?? []).join(" · ");
  if (choices) {
    const list = document.createElement("p");
    list.className = "choices";
    list.textContent = choices;
    card.append(list);
  }
  card.append(requestNote("Answer this in the owner CLI."));
  return card;
}

function planCard(event) {
  const card = requestCard(event.data.summary || "Copilot has a plan");
  card.append(markdownBody(textValue(event.data.planContent || "Plan review required.")));
  card.append(requestNote("Approve or revise this plan in the owner CLI."));
  return card;
}

function requestCard(title) {
  const card = document.createElement("article");
  card.className = "request";
  const heading = document.createElement("h2");
  heading.textContent = title;
  card.append(heading);
  return card;
}

function requestNote(text) {
  const note = document.createElement("p");
  note.className = "note";
  note.textContent = text;
  return note;
}

function actionButton(label, className, action) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = className;
  button.textContent = label;
  button.addEventListener("click", () => {
    void Promise.resolve(action()).catch((error) => {
      setStatus(error.message || "Action failed");
    });
  });
  return button;
}

async function sendPrompt(delivery) {
  const prompt = promptInput.value.trim();
  if (!prompt) return;
  promptInput.value = "";
  try {
    await runAction({
      type: "prompt",
      prompt,
      actor: actorInput.value.trim() || "Guest",
      delivery,
    }, delivery === "immediate" ? "Steering…" : "Sending…");
    promptInput.focus();
  } catch {
    promptInput.value = prompt;
  }
}

function actionId() {
  // crypto.randomUUID only exists in secure contexts; the pair link is plain
  // http on a LAN address, so build the id from getRandomValues instead.
  if (typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function runAction(action, pendingMessage) {
  setStatus(pendingMessage, { sticky: true });
  const response = await fetch("/api/actions", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: actionId(), ...action }),
  });
  const payload = await response.json();
  if (!response.ok) {
    setStatus(payload.error?.message || "Action failed");
    throw new Error(payload.error?.message || "Action failed");
  }
  setStatus("Done.");
  return payload;
}

function setStatus(text, { sticky = false } = {}) {
  clearTimeout(statusTimer);
  statusLabel.textContent = text;
  if (!sticky) {
    statusTimer = setTimeout(() => {
      statusLabel.textContent = STATUS_HINT;
    }, 4000);
  }
}

function mergeStoredReplica() {
  if (!state.sessionId) return [];
  try {
    const replica = JSON.parse(localStorage.getItem(storageKey()) || "[]");
    for (const event of replica) mergeEvent(event, true);
    return replica;
  } catch {
    localStorage.removeItem(storageKey());
    return [];
  }
}

async function publishReplica(events) {
  if (events.length === 0) return;
  try {
    const response = await fetch("/api/events/merge", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ events }),
    });
    if (response.ok) return;
  } catch {
    // The live EventSource will reconnect independently.
  }
  if (connectionLabel.textContent === "Live") {
    setStatus("The live session works, but this browser replica did not merge.");
  }
}

let persistTimer;
function persistReplicaSoon() {
  clearTimeout(persistTimer);
  persistTimer = setTimeout(persistReplica, 100);
}

function persistReplica() {
  if (!state.sessionId) return;
  try {
    localStorage.setItem(storageKey(), JSON.stringify([...state.events.values()]));
  } catch {
    setStatus("Live sync works, but this browser could not retain a local replica.");
  }
}

function storageKey() {
  return `candace-pair-events:${state.sessionId}`;
}

function setConnection(kind, label) {
  connectionDot.className = `dot ${kind}`;
  connectionLabel.textContent = label;
}

function disableControls(disabled) {
  for (const control of document.querySelectorAll("button, input, select, textarea")) {
    control.disabled = disabled;
  }
}

function isNearBottom() {
  return window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 200;
}

function stickToBottom() {
  window.scrollTo({ top: document.documentElement.scrollHeight });
  requestAnimationFrame(() => {
    window.scrollTo({ top: document.documentElement.scrollHeight });
  });
}

function compareEvents(left, right) {
  const byTime = String(left.timestamp ?? "").localeCompare(String(right.timestamp ?? ""));
  return byTime || left.id.localeCompare(right.id);
}

function isVisibleEvent(event) {
  return [
    "user.message",
    "assistant.message",
    "tool.execution_start",
    "tool.execution_complete",
    "session.error",
  ].includes(event.type);
}

function describePermission(data) {
  const request = data?.permissionRequest ?? {};
  return request.fullCommandText
    || request.command
    || request.fileName
    || request.path
    || request.url
    || request.toolName
    || request.subject
    || request.kind
    || compactValue(request);
}

function currentStreamText() {
  if (state.activeStreamId && state.streams.has(state.activeStreamId)) {
    return state.streams.get(state.activeStreamId);
  }
  return [...state.streams.values()].at(-1) || "";
}

function compactValue(value) {
  if (value === undefined || value === null || value === "") return "";
  const text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  return text.length > 8_000 ? `${text.slice(0, 8_000)}\n…` : text;
}

function textValue(value) {
  if (typeof value === "string") return value;
  return compactValue(value);
}

function shortId(value) {
  return typeof value === "string" && value.length > 12 ? `${value.slice(0, 12)}…` : value || "—";
}
