-- The CLI's own identifier for one tool invocation. It pairs a toolResult with
-- the toolCall that produced it, which the tool name alone cannot do when two
-- calls to the same tool overlap. Empty for items that are not tool events.
ALTER TABLE transcript_items ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT '';
