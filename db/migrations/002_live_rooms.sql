CREATE TABLE live_rooms (
    slug TEXT PRIMARY KEY,
    password_hash TEXT CHECK(password_hash IS NULL OR length(password_hash) > 0),
    content_size INTEGER NOT NULL CHECK(content_size >= 0),
    metadata_revision INTEGER NOT NULL CHECK(metadata_revision >= 0),
    metadata_snapshot_revision INTEGER NOT NULL CHECK(metadata_snapshot_revision >= 0),
    expires_at INTEGER NOT NULL CHECK(expires_at > created_at),
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX live_rooms_expires_idx ON live_rooms(expires_at);

CREATE TABLE live_documents (
    room_slug TEXT NOT NULL REFERENCES live_rooms(slug) ON DELETE CASCADE,
    document_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK(length(name) > 0),
    language TEXT NOT NULL CHECK(length(language) > 0),
    content TEXT NOT NULL,
    position INTEGER NOT NULL CHECK(position >= 0),
    current_revision INTEGER NOT NULL CHECK(current_revision >= 0),
    snapshot_revision INTEGER NOT NULL CHECK(snapshot_revision >= 0),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(room_slug, document_id)
) STRICT;

CREATE INDEX live_documents_order_idx ON live_documents(room_slug, position);

CREATE TABLE live_changes (
    room_slug TEXT NOT NULL REFERENCES live_rooms(slug) ON DELETE CASCADE,
    stream_kind TEXT NOT NULL CHECK(stream_kind IN ('metadata', 'document')),
    stream_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    change_kind TEXT NOT NULL CHECK(length(change_kind) > 0),
    payload TEXT NOT NULL CHECK(length(payload) > 0),
    created_at INTEGER NOT NULL,
    PRIMARY KEY(room_slug, stream_kind, stream_id, revision)
) STRICT;

CREATE INDEX live_changes_stream_idx
    ON live_changes(room_slug, stream_kind, stream_id, revision);
