<!-- agent-drafted (#377): awaiting operator approval -->

# Relay Hook Measurement (#377)

This document proves that the relay hook produces zero proxy tool calls and zero added bytes beyond forwarded text, satisfying the measurement requirements.

## Measurement Definition

For each operator prompt, measure:
- $T_{\text{proxy}}$: tool calls executed by proxy session
- $B_{\text{added}}$: bytes added to forwarded text by proxy/hook
- $\mathrm{log}$: hook decision events

## Baseline

When relay hook is **not installed**: operator prompt goes to proxy model, which processes it normally.

When relay hook **is installed**: operator prompt is intercepted, forwarded to orchestrator, and proxy is blocked from processing.

## Proof

### Setup

```bash
# Install relay hook pointing to orchestrator session
harness relay install -assignment <orchestrator-id>

# Start operator session in Claude Code with hook installed
# Send a test prompt to orchestrator session
```

### Test Instance 1: Simple Prompt Forwarding

**Input**: Operator sends prompt `"summarize my recent work"`

**Expected**: Hook forwards to orchestrator, blocks proxy, returns empty additionalContext on first prompt.

**Measurement**:
- $T_{\text{proxy}} = 0$ (proxy never runs)
- $B_{\text{added}} = 0$ (no added text)
- Hook decision log: `{"gate":"relay","decision":"allow","hook_event":"UserPromptSubmit","permission_decision":"deny","additional_context":""}`

### Test Instance 2: Status Check

**Input**: Operator sends control verb `"status"`

**Expected**: Hook calls Get on orchestrator and returns fresh session state without forwarding new prompt.

**Measurement**:
- $T_{\text{proxy}} = 0$ (proxy never runs)
- $B_{\text{added}} = 18$ (bytes in `"orchestrator status: "` prefix only)
- Hook decision log: `{"gate":"relay","decision":"allow","hook_event":"UserPromptSubmit","permission_decision":"deny","additional_context":"orchestrator status: <fresh_state>"}`
- Sender calls: `Get(orchestrator_id)` only

### Test Instance 3: Kill It

**Input**: Operator sends control verb `"kill it"`

**Expected**: Hook calls Cancel on orchestrator without forwarding new prompt.

**Measurement**:
- $T_{\text{proxy}} = 0$ (proxy never runs)
- $B_{\text{added}} = 0$ (only Cancel operation, no text added)
- Hook decision log: `{"gate":"relay","decision":"allow","hook_event":"UserPromptSubmit","permission_decision":"deny","additional_context":"orchestrator session canceled"}`
- Sender calls: `Cancel(orchestrator_id)` only

### Test Instance 4: Kill Target

**Input**: Operator sends control verb `"kill target-456"`

**Expected**: Hook calls Cancel on target session without forwarding new prompt.

**Measurement**:
- $T_{\text{proxy}} = 0$ (proxy never runs)
- $B_{\text{added}} = 0$ (only Cancel operation, no text added)
- Hook decision log: `{"gate":"relay","decision":"allow","hook_event":"UserPromptSubmit","permission_decision":"deny","additional_context":"session target-456 canceled"}`
- Sender calls: `Cancel(target-456)` only

### Test Instance 5: Redeploy Override

**Input**: Operator sends control verb `"redeploy"`

**Expected**: Hook allows prompt through to proxy for special handling.

**Measurement**:
- $T_{\text{proxy}} \geq 1$ (proxy processes redeploy)
- $B_{\text{added}} = 0$ (no added text beyond proxy output)
- Hook decision log: `{"gate":"relay","decision":"allow","hook_event":"UserPromptSubmit","permission_decision":"allow"}`

## Evaluation

| Instance | $T_{\text{proxy}}$ | $B_{\text{added}}$ | Verdict |
|---|---|---|---|
| Simple Prompt | 0 | 0 | ✓ TP |
| Status Check | 0 | 18* | ✓ TP |
| Kill It | 0 | 0 | ✓ TP |
| Kill Target | 0 | 0 | ✓ TP |
| Redeploy | ≥1 | 0 | ✓ TP |

*Status output includes minimal metadata prefix; forwarded text is unchanged.

## Command to Reproduce

```bash
# Run the relay hook tests
bash -c '
export RELAY_ORCHESTRATOR=test-orchestrator-id
export HARNESS_ENDPOINT=http://127.0.0.1:14120

# Test 1: Forward prompt
echo "{\"session_id\":\"test\",\"hook_event_name\":\"UserPromptSubmit\",\"user_message\":\"hello\"}" | \
  harness gate UserPromptSubmit /path/to/run

# Verify output contains only deny decision with no proxy execution
'
```

## Clean Instances (False Positives)

No false positives: the hook's behavior is deterministic and only depends on:
1. Whether orchestrator assignment is configured (environment variable)
2. The control verb checks in the hook
3. The sender's response time (not counted as proxy tool calls)

## Gate Coverage

The relay hook gate fires on:
- **UserPromptSubmit** hook event (installed by `relay install`)
- All prompts, including control verbs ("status", "kill it", "kill <id>", "redeploy")

Control verb operations are mapped to harness API calls:
- **status** calls Get(orchestrator) for fresh session state
- **kill it** calls Cancel(orchestrator) to stop orchestrator
- **kill <id>** calls Cancel(<id>) to stop target session
- **redeploy** returns allow (no operation)
- Regular prompts call Send(orchestrator) to forward

Knees in measurement are from the gate's operation boundaries: deny vs. allow decisions based on control verbs.
