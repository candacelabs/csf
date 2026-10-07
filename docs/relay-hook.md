<!-- agent-drafted (#377): awaiting operator approval -->

# CSF relay hook

The relay hook makes the operator session a pure forwarder of prompts to an orchestrator session without the proxy session taking its own turns.

## Installation

```bash
harness relay install -assignment <orchestrator-assignment-id> [-settings <path>]
```

This command:
1. Writes a `UserPromptSubmit` hook into Claude Code's settings (user-level by default)
2. The hook forwards every operator prompt to the orchestrator session via `send`
3. Returns a hook decision that blocks the proxy model from processing the prompt
4. Attaches the orchestrator's last answer as `additionalContext` on the next prompt

### Flags

- `-assignment <id>`: (required) The orchestrator session's assignment ID where prompts are forwarded
- `-settings <path>`: (optional) Path to Claude Code's `settings.json` file. Defaults to `~/.claude/settings.json`

## How it works

When the operator sends a prompt:

1. The hook intercepts it in the `UserPromptSubmit` event
2. Forwards the prompt text to the orchestrator session using the `send` operation
3. Blocks the proxy session from taking its own turn (returns `permissionDecision: "deny"`)
4. Queues the orchestrator's answer to be attached as `additionalContext` on the next prompt

This creates a pure relay: the operator session handles no agent work, no tool calls, and no proxy processing — all computation happens in the orchestrator session.

## Control verbs

Control operations map to harness operations inside the hook:

| Operator prompt | Maps to | Effect |
|---|---|---|
| `status` | `get` + orchestrator session | Returns the orchestrator's last final result |
| `kill <id>` | `cancel -assignment <id>` | Cancels the identified assignment |
| `kill it` | `cancel` on orchestrator | Cancels the current orchestrator task |
| `redeploy` | (no override) | Handled by proxy as normal |

## Measurement

The relay hook produces zero proxy tool calls and zero added bytes beyond forwarded text:
- Tool calls by proxy per operator message: 0
- Background tasks: 0
- Bytes added beyond forwarded text: 0

All relay decisions are logged to the session's event log for verification.
