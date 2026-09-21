-- HTTP prompt retries carry a client UUID. The mapping is separate from turns
-- because scheduled turns have their own occurrence identity and must not
-- manufacture an HTTP wire concern. The turn row owns the request contents;
-- this relation owns only the scoped retry receipt.
CREATE TABLE prompt_submissions (
    session_id UUID NOT NULL,
    idempotency_key UUID NOT NULL,
    turn_id UUID NOT NULL,
    PRIMARY KEY (session_id, idempotency_key)
);
