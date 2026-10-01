(function () {
  "use strict";

  var body = document.body;
  body.classList.add("is-enhanced");

  var snapshot = normalizeSnapshot(readInitialSnapshot());
  var routePatterns = readRoutePatterns();
  var enumValues = readEnumValues();
  var toastTimer = 0;
  var eventSource = null;

  bindNavigation();
  bindPrompt();
  bindChat();
  bindActions();
  render(snapshot);
  openEventStream();
  window.setInterval(refreshSnapshot, 30000);

  function select(selector, root) {
    return (root || document).querySelector(selector);
  }

  function selectAll(selector, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(selector));
  }

  // The product and agent names are snapshot data, not literals: a rebranded
  // core ships this exact file and renames itself through the snapshot it
  // publishes. The fallbacks are the stock identity, used only until the first
  // snapshot lands.
  var brandFallback = { product: "CandaceOS", agent: "Claw" };

  function productName() {
    return stringValue(asObject(snapshot.system).name, brandFallback.product);
  }

  function agentName() {
    return stringValue(asObject(snapshot.system).agent_name, brandFallback.agent);
  }

  function readRoutePatterns() {
    return {
      approval: body.getAttribute("data-route-approval") || "",
      clawChat: body.getAttribute("data-route-claw-chat") || "",
      clawMessages: body.getAttribute("data-route-claw-messages") || "",
      clawRunAbort: body.getAttribute("data-route-claw-run-abort") || "",
      currentRunAbort: body.getAttribute("data-route-current-run-abort") || "",
      events: body.getAttribute("data-route-events") || "",
      prompts: body.getAttribute("data-route-prompts") || "",
      snapshot: body.getAttribute("data-route-snapshot") || ""
    };
  }

  function readEnumValues() {
    return {
      harnessBackendCopilotCLI: body.getAttribute("data-enum-harness-backend-copilot-cli") || "",
      harnessBackendDemo: body.getAttribute("data-enum-harness-backend-demo") || "",
      harnessBackendEmbedded: body.getAttribute("data-enum-harness-backend-embedded") || "",
      harnessBackendOllama: body.getAttribute("data-enum-harness-backend-ollama") || "",
      harnessBackendOpenCode: body.getAttribute("data-enum-harness-backend-opencode") || "",
      harnessCapabilityActiveTurnSteering: body.getAttribute("data-enum-harness-capability-active-turn-steering") || "",
      harnessDeliveryEnqueue: body.getAttribute("data-enum-harness-delivery-enqueue") || "",
      harnessDeliveryImmediate: body.getAttribute("data-enum-harness-delivery-immediate") || ""
    };
  }

  function routePath(pattern, parameters) {
    var path = pattern;
    Object.keys(parameters || {}).forEach(function (name) {
      path = path.replace(":" + name, encodeURIComponent(parameters[name]));
    });
    return path;
  }

  function readInitialSnapshot() {
    var element = select("#initial-snapshot");
    if (!element) return {};
    try {
      return JSON.parse(element.textContent || "{}");
    } catch (_) {
      return {};
    }
  }

  function asObject(value) {
    return value && typeof value === "object" && !Array.isArray(value) ? value : {};
  }

  function asList(value) {
    return Array.isArray(value) ? value : [];
  }

  function normalizeSnapshot(value) {
    var source = asObject(value);
    var fleet = asObject(source.fleet);
    var run = source.run && typeof source.run === "object" ? asObject(source.run) : null;
    if (run) run.entries = asList(run.entries);
    fleet.quorum = asObject(fleet.quorum);
    fleet.nodes = asList(fleet.nodes);
    return {
      generated_at: stringValue(source.generated_at),
      system: asObject(source.system),
      attention: asList(source.attention),
      run: run,
      fleet: fleet,
      apps: asList(source.apps),
      activity: asList(source.activity)
    };
  }

  function stringValue(value, fallback) {
    if (typeof value === "string" && value.trim()) return value;
    return fallback || "";
  }

  function numberValue(value) {
    var parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : 0;
  }

  function boolValue(value) {
    return value === true;
  }

  function setText(selector, value) {
    selectAll(selector).forEach(function (element) {
      element.textContent = value;
    });
  }

  function create(tag, className, text) {
    var element = document.createElement(tag);
    if (className) element.className = className;
    if (text !== undefined && text !== null) element.textContent = String(text);
    return element;
  }

  function append(parent) {
    for (var index = 1; index < arguments.length; index += 1) {
      if (arguments[index]) parent.appendChild(arguments[index]);
    }
    return parent;
  }

  function replace(selector, children) {
    var target = select(selector);
    if (!target) return;
    target.replaceChildren.apply(target, children);
  }

  function titleCase(value) {
    var text = stringValue(value, "unknown").replace(/[_-]+/g, " ");
    return text.replace(/\b\w/g, function (character) { return character.toUpperCase(); });
  }

  function harnessBackendLabel(value) {
    var backend = stringValue(value).toUpperCase();
    switch (backend) {
      case enumValues.harnessBackendCopilotCLI:
        return "Copilot CLI";
      case enumValues.harnessBackendDemo:
        return "Demo";
      case enumValues.harnessBackendOllama:
        return "Ollama";
      case enumValues.harnessBackendEmbedded:
        return "Embedded";
      case enumValues.harnessBackendOpenCode:
        return "OpenCode";
      default:
        return "Agent";
    }
  }

  function tone(value) {
    switch (stringValue(value).toLowerCase()) {
      case "alive":
      case "complete":
      case "healthy":
      case "online":
      case "ready":
      case "running":
      case "succeeded":
      case "done":
      case "approved":
      case "leader":
        return "positive";
      case "awaiting_approval":
      case "busy":
      case "working":
      case "deploying":
      case "pending":
      case "queued":
      case "steering":
      case "compacted":
      case "truncated":
      case "requested":
      case "starting":
      case "suspect":
      case "warning":
      case "candidate":
        return "attention";
      case "canceled":
      case "dead":
      case "failed":
      case "offline":
      case "unavailable":
      case "stopped":
      case "rejected":
      case "blocked":
      case "error":
        return "negative";
      default:
        return "neutral";
    }
  }

  function statusPill(status) {
    var pill = create("span", "status-pill tone-" + tone(status));
    pill.appendChild(create("span", "status-dot"));
    pill.lastChild.setAttribute("aria-hidden", "true");
    pill.appendChild(document.createTextNode(titleCase(status)));
    return pill;
  }

  function relativeTime(value) {
    if (!value || String(value).indexOf("0001-01-01") === 0) return "now";
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) return "now";
    var seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
    if (seconds < 45) return "just now";
    if (seconds < 3600) return Math.floor(seconds / 60) + "m ago";
    if (seconds < 86400) return Math.floor(seconds / 3600) + "h ago";
    return Math.floor(seconds / 86400) + "d ago";
  }

  function displayName(primary, fallback) {
    return stringValue(primary, stringValue(fallback, "Unnamed"));
  }

  function percent(value) {
    return Math.max(0, Math.min(100, numberValue(value)));
  }

  function monogram(name) {
    return Array.from(stringValue(name, "?").trim())[0] || "?";
  }

  // The navigation is data the page renders, not a list this file knows. Every
  // view name here is read back out of the DOM, so a core that registers extra
  // sidebar entries, reorders them, or replaces the nav markup entirely still
  // gets working in-page switching for whatever views it did render — and an
  // entry that points somewhere else stays ordinary navigation.
  function viewSection(name) {
    if (!name) return null;
    var found = null;
    selectAll("[data-view]").forEach(function (view) {
      if (!found && view.getAttribute("data-view") === name) found = view;
    });
    return found;
  }

  function defaultView() {
    var opening = "";
    selectAll("[data-nav]").forEach(function (item) {
      var name = item.getAttribute("data-nav") || "";
      if (!opening && viewSection(name)) opening = name;
    });
    if (opening) return opening;
    var first = select("[data-view]");
    return first ? first.getAttribute("data-view") || "" : "";
  }

  function viewOf(element) {
    var section = element ? element.closest("[data-view]") : null;
    return (section && section.getAttribute("data-view")) || defaultView();
  }

  function bindNavigation() {
    document.addEventListener("click", function (event) {
      var target = event.target.closest("[data-nav], [data-view-link]");
      if (!target) return;
      var viewName = target.getAttribute("data-nav") || target.getAttribute("data-view-link");
      if (!viewSection(viewName)) return;
      event.preventDefault();
      navigate(viewName, true);
    });

    window.addEventListener("hashchange", function () {
      navigate(viewFromHash(), false);
    });
    navigate(viewFromHash(), false);
  }

  function viewFromHash() {
    var name = window.location.hash.replace(/^#/, "");
    return viewSection(name) ? name : defaultView();
  }

  function navigate(name, updateHash) {
    selectAll("[data-view]").forEach(function (view) {
      var active = view.getAttribute("data-view") === name;
      view.classList.toggle("is-active", active);
      view.setAttribute("aria-hidden", active ? "false" : "true");
    });
    selectAll("[data-nav]").forEach(function (item) {
      var active = item.getAttribute("data-nav") === name;
      item.classList.toggle("is-active", active);
      if (active) item.setAttribute("aria-current", "page");
      else item.removeAttribute("aria-current");
    });
    if (updateHash && name && window.location.hash !== "#" + name) {
      window.history.pushState(null, "", "#" + name);
    }
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  function bindPrompt() {
    var form = select("[data-prompt-form]");
    var input = select("#prompt");
    if (!form || !input) return;

    selectAll("[data-prompt-suggestion]").forEach(function (button) {
      button.addEventListener("click", function () {
        input.value = button.getAttribute("data-prompt-suggestion") || "";
        input.focus();
      });
    });

    document.addEventListener("click", function (event) {
      var button = event.target.closest("[data-fill-prompt]");
      if (!button) return;
      navigate(viewOf(form), true);
      input.value = button.getAttribute("data-fill-prompt") || "";
      window.setTimeout(function () { input.focus(); }, 0);
    });

    input.addEventListener("keydown", function (event) {
      if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        form.requestSubmit();
      }
    });

    form.addEventListener("submit", async function (event) {
      event.preventDefault();
      var prompt = input.value.trim();
      if (!prompt) {
        input.focus();
        return;
      }
      var button = select('button[type="submit"]', form);
      var message = select("[data-prompt-message]", form);
      button.disabled = true;
      message.classList.remove("is-error");
      message.textContent = "Giving this to " + agentName() + "…";
      try {
        await postJSON(routePatterns.prompts, { prompt: prompt });
        input.value = "";
        message.textContent = "Started. The run will appear below.";
        showToast(agentName() + " started working on it.");
        await refreshSnapshot();
      } catch (error) {
        message.classList.add("is-error");
        message.textContent = error.message;
        showToast(error.message, true);
      } finally {
        button.disabled = false;
      }
    });
  }

  function bindChat() {
    var form = select("[data-chat-form]");
    var input = select("#chat-prompt", form);
    if (!form || !input) return;

    input.addEventListener("keydown", function (event) {
      if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        var preferred = select("[data-chat-enqueue]", form);
        form.requestSubmit(preferred || undefined);
      }
    });

    form.addEventListener("submit", async function (event) {
      event.preventDefault();
      if (form.getAttribute("data-chat-submitting") === "true") return;
      var prompt = input.value.trim();
      if (!prompt) {
        input.focus();
        return;
      }

      var submitter = event.submitter || select("[data-chat-enqueue]", form);
      var delivery = submitter ? submitter.getAttribute("data-delivery") : enumValues.harnessDeliveryEnqueue;
      var message = select("[data-chat-message]", form);
      form.setAttribute("data-chat-submitting", "true");
      form.setAttribute("aria-busy", "true");
      renderChat(snapshot.run, snapshot.system);
      message.classList.remove("is-error");
      message.textContent = delivery === enumValues.harnessDeliveryImmediate
        ? "Steering " + agentName() + " now…"
        : "Sending this to " + agentName() + "…";

      try {
        await postJSON(form.getAttribute("action"), {
          prompt: prompt,
          delivery: delivery,
          expected_run_id: form.getAttribute("data-expected-run-id") || ""
        });
        input.value = "";
        message.textContent = delivery === enumValues.harnessDeliveryImmediate
          ? agentName() + " is changing direction now."
          : "Queued in this session.";
        showToast(delivery === enumValues.harnessDeliveryImmediate
          ? "Steered " + agentName() + "."
          : "Message sent to " + agentName() + ".");
        await refreshSnapshot();
      } catch (error) {
        message.classList.add("is-error");
        message.textContent = error.message;
        showToast(error.message, true);
      } finally {
        form.removeAttribute("data-chat-submitting");
        form.setAttribute("aria-busy", "false");
        renderChat(snapshot.run, snapshot.system);
      }
    });
  }

  function bindActions() {
    document.addEventListener("click", async function (event) {
      var approval = event.target.closest("[data-approval]");
      if (approval) {
        var id = approval.getAttribute("data-approval");
        var decision = approval.getAttribute("data-decision");
        if (!id || !decision) return;
        approval.disabled = true;
        try {
          await postJSON(routePath(routePatterns.approval, { id: id }), { decision: decision });
          showToast(decision === "approve"
            ? "Approved. " + agentName() + " can continue."
            : "Not approved. The run will adjust.");
          await refreshSnapshot();
        } catch (error) {
          approval.disabled = false;
          showToast(error.message, true);
        }
        return;
      }

      var abort = event.target.closest("[data-abort-run]");
      if (!abort) return;
      abort.disabled = true;
      try {
        await postJSON(abort.getAttribute("data-chat-abort-url") || routePatterns.currentRunAbort, {});
        showToast("Asked " + agentName() + " to stop safely.");
        await refreshSnapshot();
      } catch (error) {
        abort.disabled = false;
        showToast(error.message, true);
      }
    });
  }

  async function postJSON(url, payload) {
    var response = await window.fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify(payload)
    });
    if (!response.ok) {
      var detail = "The request could not be completed.";
      try {
        var result = await response.json();
        detail = stringValue(result.error, detail);
      } catch (_) {
        // Keep the calm, generic error when a proxy returned non-JSON.
      }
      throw new Error(detail);
    }
  }

  async function refreshSnapshot() {
    try {
      var response = await window.fetch(routePatterns.snapshot, {
        headers: { "Accept": "application/json" },
        cache: "no-store"
      });
      if (!response.ok) throw new Error("snapshot unavailable");
      applySnapshot(await response.json());
      setLive(true);
    } catch (_) {
      setLive(false);
    }
  }

  function openEventStream() {
    if (!("EventSource" in window)) {
      setLive(false);
      return;
    }
    eventSource = new window.EventSource(routePatterns.events);
    eventSource.addEventListener("open", function () { setLive(true); });
    eventSource.addEventListener("error", function () { setLive(false); });
    eventSource.addEventListener("snapshot", acceptSnapshotEvent);
    eventSource.onmessage = acceptSnapshotEvent;
  }

  function acceptSnapshotEvent(event) {
    try {
      applySnapshot(JSON.parse(event.data));
      setLive(true);
    } catch (_) {
      // Ignore malformed or non-snapshot events; EventSource remains open.
    }
  }

  function applySnapshot(value) {
    snapshot = normalizeSnapshot(value);
    render(snapshot);
  }

  function setLive(connected) {
    var state = select(".live-state");
    if (state) state.classList.toggle("is-offline", !connected);
    setText("[data-live-label]", connected ? "Connected" : "Reconnecting");
  }

  function showToast(message, isError) {
    var toast = select("[data-toast]");
    if (!toast) return;
    window.clearTimeout(toastTimer);
    toast.textContent = message;
    toast.classList.toggle("is-error", Boolean(isError));
    toast.hidden = false;
    toastTimer = window.setTimeout(function () { toast.hidden = true; }, 4200);
  }

  function render(value) {
    renderSystem(value);
    renderAttention(value.attention);
    renderRun(value.run);
    renderChat(value.run, value.system);
    renderHomeFleet(value.fleet);
    renderHomeApps(value.apps);
    renderHomeActivity(value.activity);
    renderApps(value.apps);
    renderFleet(value.fleet);
    renderActivity(value.activity);
  }

  function renderSystem(value) {
    var system = value.system;
    setText("[data-system-name]", stringValue(system.name, brandFallback.product));
    setText("[data-system-status]", titleCase(system.status));
    setText("[data-system-summary]", stringValue(system.summary, "Local control plane"));
    setText("[data-system-version]", system.version ? "core " + system.version : "");
    var harnessLabel = harnessBackendLabel(system.harness_backend);
    if (String(system.harness_backend).toUpperCase() === enumValues.harnessBackendEmbedded && system.harness_implementation) {
      harnessLabel = system.harness_implementation;
    }
    setText("[data-harness-runtime]", harnessLabel + (system.harness_model ? " · " + system.harness_model : ""));
    setText("[data-app-count]", value.apps.length);
    setText("[data-node-count]", value.fleet.nodes.length);
    selectAll(".connection-light").forEach(function (light) {
      light.className = "connection-light tone-" + tone(system.status);
    });
  }

  function renderAttention(items) {
    setText("[data-attention-count]", items.length);
    var children = items.map(function (item) {
      var card = create("article", "approval-card");
      card.setAttribute("data-approval-id", stringValue(item.id));
      var topline = create("div", "approval-topline");
      var icon = create("span", "approval-icon", "!");
      icon.setAttribute("aria-hidden", "true");
      var risk = create("span", "risk-label", stringValue(item.risk, "Approval required"));
      var when = create("time", "", relativeTime(item.requested_at));
      if (item.requested_at) when.dateTime = item.requested_at;
      append(topline, icon, risk, when);
      card.appendChild(topline);
      card.appendChild(create("h3", "", stringValue(item.title, "Approval requested")));
      if (item.detail) card.appendChild(create("p", "", item.detail));
      var actions = create("div", "approval-actions");
      actions.appendChild(actionButton("Not now", item.id, "reject", "button secondary"));
      actions.appendChild(actionButton("Approve", item.id, "approve", "button primary"));
      card.appendChild(actions);
      return card;
    });
    if (!children.length) {
      children.push(emptyState("Nothing waiting on you", productName() + " will pause here before consequential actions.", "compact", "✓"));
    }
    replace("[data-attention-list]", children);
  }

  function actionButton(label, id, decision, className) {
    var button = create("button", className, label);
    button.type = "button";
    button.setAttribute("data-approval", stringValue(id));
    button.setAttribute("data-decision", decision);
    return button;
  }

  function emptyState(title, detail, extraClass, mark) {
    var empty = create("div", "empty-state " + (extraClass || ""));
    var icon = create("span", mark === "✓" ? "empty-check" : "empty-orbit", mark === "✓" ? mark : "");
    icon.setAttribute("aria-hidden", "true");
    var copy = create("div");
    append(copy, create("strong", "", title), create("p", "", detail));
    append(empty, icon, copy);
    return empty;
  }

  function renderRun(run) {
    var target = select("[data-current-run]");
    var statusTarget = select("[data-run-status]");
    if (!target || !statusTarget) return;
    statusTarget.replaceChildren(statusPill(run ? run.status : "idle"));
    var active = run && ["busy", "running", "starting", "working"].indexOf(stringValue(run.status).toLowerCase()) >= 0;
    setText("[data-run-heading]", run ? (active ? agentName() + " is working" : "Latest " + agentName() + " run") : agentName() + " is ready");
    if (!run) {
      target.replaceChildren(emptyState("No active run", "Tell " + productName() + " what you want above. The plan and every tool call will stream here.", "run-empty"));
      return;
    }

    var summary = create("div", "run-summary");
    var copy = create("div");
    append(copy,
      create("strong", "", stringValue(run.title, "Untitled run")),
      create("span", "", run.entries.length + " updates · started " + relativeTime(run.started_at))
    );
    summary.appendChild(copy);
    var actions = create("div", "run-actions");
    if (run.session_id) {
      var chatLink = create("a", "run-chat-link", "Open live chat ");
      chatLink.href = clawChatPath(run.session_id);
      var arrow = create("span", "", "→");
      arrow.setAttribute("aria-hidden", "true");
      chatLink.appendChild(arrow);
      actions.appendChild(chatLink);
    }
    if (boolValue(run.can_abort)) {
      var stop = create("button", "text-button danger", "Stop run");
      stop.type = "button";
      stop.setAttribute("data-abort-run", "");
      actions.appendChild(stop);
    }
    if (actions.childNodes.length) summary.appendChild(actions);
    var transcript = create("ol", "transcript");
    transcript.setAttribute("data-transcript", "");
    transcript.setAttribute("aria-live", "polite");
    run.entries.forEach(function (entry) { transcript.appendChild(runEntry(entry)); });
    if (!run.entries.length) transcript.appendChild(create("li", "transcript-waiting", agentName() + " is preparing the first step…"));
    target.replaceChildren(summary, transcript);
    transcript.scrollTop = transcript.scrollHeight;
  }

  function clawChatPath(sessionID) {
    return routePath(routePatterns.clawChat, { sessionID: sessionID });
  }

  function runIsBusy(run) {
    return Boolean(run) && ["running", "busy", "starting", "working", "waiting", "queued", "aborting"].indexOf(stringValue(run.status).toLowerCase()) >= 0;
  }

  function canSteerActiveTurn(system) {
    return asList(asObject(system).harness_capabilities).indexOf(enumValues.harnessCapabilityActiveTurnSteering) >= 0;
  }

  function renderChat(run, system) {
    var root = select("[data-chat-session]");
    if (!root) return;

    var expectedSessionID = root.getAttribute("data-chat-session") || "";
    var currentSessionID = run ? stringValue(run.session_id) : "";
    var matches = Boolean(run && currentSessionID === expectedSessionID);
    var form = select("[data-chat-form]", root);
    var unavailable = select("[data-chat-unavailable]", root);
    var transcript = select("[data-chat-transcript]", root);
    var stop = select("[data-chat-stop]", root);
    var busy = runIsBusy(run);
    var canSteer = canSteerActiveTurn(system);
    if (unavailable) unavailable.hidden = matches;
    if (transcript) transcript.hidden = !matches;
    if (form) {
      var submitting = form.getAttribute("data-chat-submitting") === "true";
      selectAll('button[type="submit"]', form).forEach(function (button) { button.disabled = !matches || submitting || (busy && !canSteer); });
      var input = select("textarea", form);
      if (input) input.disabled = !matches || submitting || (busy && !canSteer);
    }
    if (!matches) {
      if (stop) stop.hidden = true;
      return;
    }

    setText("[data-chat-title]", stringValue(run.title, "Untitled run"));
    setText("[data-chat-backend]", harnessBackendLabel(system.harness_backend));
    setText("[data-chat-model]", stringValue(system.harness_model, "default"));
    setText("[data-chat-run-id]", stringValue(run.id));
    setText("[data-chat-update-count]", run.entries.length + " updates");
    var status = select("[data-chat-status]", root);
    if (status) status.replaceChildren(statusPill(run.status));

    if (transcript) {
      var nearBottom = transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight < 80;
      var entries = run.entries.map(runEntry);
      if (!entries.length) entries.push(create("li", "transcript-waiting", agentName() + " is preparing the first step…"));
      transcript.replaceChildren.apply(transcript, entries);
      if (nearBottom) transcript.scrollTop = transcript.scrollHeight;
    }

    var busyActions = select("[data-chat-busy-actions]", root);
    var idleActions = select("[data-chat-idle-actions]", root);
    var steerNote = select("[data-chat-steer-note]", root);
    if (busyActions) busyActions.hidden = !busy || !canSteer;
    if (idleActions) idleActions.hidden = busy;
    if (steerNote) steerNote.hidden = !busy || !canSteer;
    if (form) {
      form.setAttribute("action", routePath(routePatterns.clawMessages, { sessionID: expectedSessionID }));
      form.setAttribute("data-expected-run-id", stringValue(run.id));
    }
    if (stop) {
      stop.hidden = !boolValue(run.can_abort);
      stop.setAttribute("data-chat-abort-url", routePath(routePatterns.clawRunAbort, {
        sessionID: expectedSessionID,
        runID: run.id
      }));
    }
  }

  function runEntry(entry) {
    var rawKind = stringValue(entry.kind, "message");
    var kind = rawKind === "tool" || rawKind === "error" || rawKind === "notice" ? rawKind : "message";
    var item = create("li", "transcript-entry entry-" + kind);
    if (kind === "tool") {
      var card = create("div", "tool-card");
      var head = create("div", "tool-card-head");
      var symbol = create("span", "tool-symbol", "›_");
      symbol.setAttribute("aria-hidden", "true");
      append(head, symbol, create("strong", "", stringValue(entry.name, "Tool")), statusPill(entry.status));
      card.appendChild(head);
      if (entry.text) card.appendChild(create("p", "", entry.text));
      if (entry.detail) card.appendChild(create("pre", "", entry.detail));
      item.appendChild(card);
      return item;
    }
    if (kind === "notice") {
      var noticeCard = create("div", "tool-card notice-card");
      var noticeHead = create("div", "tool-card-head");
      var noticeSymbol = create("span", "tool-symbol", "i");
      noticeSymbol.setAttribute("aria-hidden", "true");
      append(noticeHead, noticeSymbol, create("strong", "", stringValue(entry.name, agentName())), statusPill(entry.status));
      noticeCard.appendChild(noticeHead);
      if (entry.text) noticeCard.appendChild(create("p", "", entry.text));
      item.appendChild(noticeCard);
      return item;
    }
    if (kind === "error") {
      var errorCard = create("div", "tool-card error-card");
      var errorHead = create("div", "tool-card-head");
      var errorSymbol = create("span", "tool-symbol", "!");
      errorSymbol.setAttribute("aria-hidden", "true");
      append(errorHead, errorSymbol, create("strong", "", stringValue(entry.name, agentName())), statusPill(stringValue(entry.status, "failed")));
      errorCard.appendChild(errorHead);
      if (entry.text) errorCard.appendChild(create("p", "", entry.text));
      item.appendChild(errorCard);
      return item;
    }
    var role = create("div", "message-role", stringValue(entry.name, entry.role === "user" ? "You" : agentName()));
    if (entry.status) role.appendChild(statusPill(entry.status));
    item.appendChild(role);
    item.appendChild(create("p", "", stringValue(entry.text)));
    return item;
  }

  function renderHomeFleet(fleet) {
    var children = [];
    var leader = create("article", "fleet-summary-card");
    append(leader, summaryIcon("L"), summaryCopy("Leader", stringValue(fleet.leader_id, "Electing…")));
    children.push(leader);
    var quorum = create("article", "fleet-summary-card");
    var quorumIcon = summaryIcon("Q", "quorum" + (fleet.quorum.healthy ? " healthy" : ""));
    append(quorum, quorumIcon, summaryCopy("Quorum", numberValue(fleet.quorum.online) + " of " + numberValue(fleet.quorum.required) + " required"));
    children.push(quorum);
    fleet.nodes.forEach(function (node) {
      var card = create("article", "node-mini-card");
      var light = create("span", "node-light tone-" + tone(node.status));
      light.setAttribute("aria-hidden", "true");
      var copy = create("div");
      append(copy,
        create("strong", "", displayName(node.name, node.id)),
        create("span", "", titleCase(node.role) + " · " + numberValue(node.apps) + " apps")
      );
      append(card, light, copy);
      if (hasMetric(node, "cpu_percent")) card.appendChild(create("span", "node-load", Math.round(percent(node.cpu_percent)) + "%"));
      children.push(card);
    });
    replace("[data-home-fleet]", children);
    setText("[data-leader-id]", stringValue(fleet.leader_id, "Electing…"));
    setText("[data-quorum]", numberValue(fleet.quorum.online) + " of " + numberValue(fleet.quorum.required) + " required");
  }

  function summaryIcon(label, extraClass) {
    var icon = create("span", "summary-icon " + (extraClass || ""), label);
    icon.setAttribute("aria-hidden", "true");
    return icon;
  }

  function summaryCopy(label, value) {
    var copy = create("div");
    append(copy, create("span", "", label), create("strong", "", value));
    return copy;
  }

  function renderHomeApps(apps) {
    var children = apps.slice(0, 4).map(function (app) {
      var card = create("article", "app-mini-card");
      var copy = create("div", "app-mini-body");
      append(copy,
        create("strong", "", displayName(app.name, app.id)),
        create("span", "", stringValue(app.node_id, "Not placed"))
      );
      append(card, create("div", "app-monogram", monogram(app.name)), copy, statusPill(app.status));
      return card;
    });
    if (!children.length) children.push(emptyState("No apps yet", "Ask " + agentName() + " about the first one.", "compact", "✓"));
    replace("[data-home-apps]", children);
  }

  function renderHomeActivity(activity) {
    var children = activity.slice(0, 5).map(function (item) {
      var row = create("li");
      var mark = create("span", "receipt-mark tone-" + tone(item.status));
      mark.setAttribute("aria-hidden", "true");
      var copy = create("div");
      var detail = relativeTime(item.at);
      if (item.receipt_id) detail += " · " + item.receipt_id;
      append(copy, create("strong", "", stringValue(item.title, "Activity")), create("span", "", detail));
      append(row, mark, copy);
      return row;
    });
    if (!children.length) children.push(create("li", "inline-empty", "Receipts will appear as work completes."));
    replace("[data-home-activity]", children);
  }

  function renderApps(apps) {
    var children = apps.map(appCard);
    if (!children.length) children.push(emptyState("Your fleet is ready for its first app", "Use plain language—" + agentName() + " will explain what the selected backend can do and pause before deployment.", "page-empty"));
    replace("[data-app-grid]", children);
  }

  function appCard(app) {
    var card = create("article", "app-card");
    var head = create("div", "app-card-head");
    append(head, create("div", "app-monogram large", monogram(app.name)), statusPill(app.status));
    append(card,
      head,
      create("h2", "", displayName(app.name, app.id)),
      create("p", "", stringValue(app.summary, "A " + productName() + " application."))
    );
    var details = create("dl", "app-details");
    details.appendChild(detailRow("Node", stringValue(app.node_id, "Unassigned")));
    details.appendChild(detailRow("Revision", stringValue(app.revision, "—"), "mono"));
    details.appendChild(detailRow("Updated", relativeTime(app.updated_at)));
    card.appendChild(details);
    var actions = create("div", "app-card-actions");
    var url = safeURL(app.url);
    if (url) {
      var link = create("a", "button secondary", "Open app ↗");
      link.href = url;
      link.target = "_blank";
      link.rel = "noreferrer";
      actions.appendChild(link);
    }
    var ask = create("button", "text-button", "Ask " + agentName());
    ask.type = "button";
    ask.setAttribute("data-fill-prompt", "Update " + displayName(app.name, app.id) + " so that ");
    actions.appendChild(ask);
    card.appendChild(actions);
    return card;
  }

  function detailRow(label, value, className) {
    var row = create("div");
    append(row, create("dt", "", label), create("dd", className || "", value));
    return row;
  }

  function safeURL(value) {
    if (!value) return "";
    try {
      var url = new URL(value, window.location.origin);
      return url.protocol === "http:" || url.protocol === "https:" ? url.href : "";
    } catch (_) {
      return "";
    }
  }

  function renderFleet(fleet) {
    setText("[data-fleet-leader]", stringValue(fleet.leader_id, "No leader"));
    setText("[data-fleet-quorum]", numberValue(fleet.quorum.online) + " online");
    setText("[data-fleet-node-total]", fleet.nodes.length);
    var health = select("[data-fleet-health]");
    if (health) health.replaceChildren(statusPill(fleet.quorum.healthy ? "healthy" : "warning"));
    var children = fleet.nodes.map(nodeCard);
    if (!children.length) children.push(emptyState("Waiting for fleet membership", "Nodes will appear after they report to " + productName() + " Core.", "page-empty"));
    replace("[data-node-grid]", children);
  }

  function nodeCard(node) {
    var card = create("article", "node-card");
    var head = create("div", "node-card-head");
    var identity = create("div");
    var light = create("span", "node-light tone-" + tone(node.status));
    light.setAttribute("aria-hidden", "true");
    append(identity, light, create("strong", "", displayName(node.name, node.id)));
    append(head, identity, statusPill(node.role));
    card.appendChild(head);
    card.appendChild(create("p", "mono node-address", stringValue(node.address, "address unavailable")));
    if (hasMetric(node, "cpu_percent") || hasMetric(node, "memory_percent")) {
      card.appendChild(resourceRow("CPU", node.cpu_percent));
      card.appendChild(resourceRow("Memory", node.memory_percent));
    } else {
      card.appendChild(create("p", "metrics-unavailable", "Load telemetry unavailable"));
    }
    var foot = create("div", "node-card-foot");
    append(foot,
      create("span", "", numberValue(node.apps) + " apps"),
      create("span", "", "seen " + relativeTime(node.last_seen))
    );
    card.appendChild(foot);
    return card;
  }

  function resourceRow(label, value) {
    var amount = percent(value);
    var row = create("div", "resource-row");
    var labelElement = create("label");
    append(labelElement, document.createTextNode(label), create("span", "", Math.round(amount) + "%"));
    var meter = create("meter");
    meter.min = 0;
    meter.max = 100;
    meter.value = amount;
    meter.textContent = Math.round(amount) + "%";
    append(row, labelElement, meter);
    return row;
  }

  function hasMetric(node, field) {
    return Object.prototype.hasOwnProperty.call(node, field) && typeof node[field] === "number" && Number.isFinite(node[field]);
  }

  function renderActivity(activity) {
    var children = activity.map(activityRow);
    if (!children.length) children.push(emptyState("No activity yet", "Completed work and decisions will leave a receipt here.", "page-empty"));
    replace("[data-activity-list]", children);
  }

  function activityRow(item) {
    var row = create("li", "activity-row");
    var glyphs = { deploy: "↑", approval: "✓", tool: "›_" };
    var glyph = create("span", "activity-glyph tone-" + tone(item.status), glyphs[item.kind] || "·");
    glyph.setAttribute("aria-hidden", "true");
    var copy = create("div", "activity-copy");
    var title = create("div");
    append(title, create("strong", "", stringValue(item.title, "Activity")), statusPill(item.status));
    copy.appendChild(title);
    if (item.detail) copy.appendChild(create("p", "", item.detail));
    var meta = create("span", "", relativeTime(item.at));
    if (item.receipt_id) {
      meta.appendChild(document.createTextNode(" · receipt "));
      meta.appendChild(create("code", "", item.receipt_id));
    }
    copy.appendChild(meta);
    append(row, glyph, copy);
    return row;
  }
})();
