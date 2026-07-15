CREATE TABLE pastes (
    slug TEXT PRIMARY KEY,
    payload TEXT NOT NULL,
    is_encrypted INTEGER NOT NULL CHECK (is_encrypted IN (0, 1)),
    crypto_version INTEGER,
    burn_after_read INTEGER NOT NULL CHECK (burn_after_read IN (0, 1)),
    content_size INTEGER NOT NULL CHECK (content_size >= 0),
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    CHECK ((is_encrypted = 0 AND crypto_version IS NULL) OR (is_encrypted = 1 AND crypto_version IS NOT NULL))
) STRICT;

CREATE INDEX idx_pastes_expires_at ON pastes(expires_at);
