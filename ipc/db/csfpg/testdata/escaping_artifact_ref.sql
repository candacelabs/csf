-- Copyright 2026 Candace Labs
--
-- An artifact path that climbs out of the artifact root. The regular
-- expression CHECK on csf_documents.artifact_ref must reject it.
INSERT INTO csf_documents (content_hash, byte_size, artifact_ref)
VALUES (repeat('e', 64), 1, 'documents/../escape.txt')
