-- Copyright 2026 Candace Labs
--
-- An artifact path with a doubled separator. position('//' IN artifact_ref) = 0
-- must reject it.
INSERT INTO csf_documents (content_hash, byte_size, artifact_ref)
VALUES (repeat('9', 64), 1, 'documents//double.txt')
