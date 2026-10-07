-- Copyright 2026 Candace Labs
--
-- An artifact path ending in a separator. right(artifact_ref, 1) <> '/' must
-- reject it.
INSERT INTO csf_documents (content_hash, byte_size, artifact_ref)
VALUES (repeat('f', 64), 1, 'documents/')
