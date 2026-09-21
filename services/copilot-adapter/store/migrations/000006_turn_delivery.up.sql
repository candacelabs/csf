-- Prompt rows commit before crossing the Copilot SDK boundary. Pending means
-- no SDK call began, unknown means a call may have crossed the boundary, and
-- accepted means the bridge proved the SDK accepted that TurnID.
ALTER TABLE turns
    ADD COLUMN delivery_status TEXT NOT NULL DEFAULT 'accepted';
