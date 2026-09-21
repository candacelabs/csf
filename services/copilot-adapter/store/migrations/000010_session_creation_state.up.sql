-- The receipt owns the at-most-once SDK create boundary. A non-null attempt
-- identifier is durable evidence that CreateSession may have crossed into the
-- Copilot CLI; every later process must ResumeSession instead of creating.
ALTER TABLE session_creations
ADD COLUMN sdk_create_attempt_id UUID;

-- Completion is a relational phase marker, not a cached HTTP response. The
-- sessions row remains the canonical response projection for exact retries.
ALTER TABLE session_creations
ADD COLUMN completed_at TIMESTAMPTZ;
