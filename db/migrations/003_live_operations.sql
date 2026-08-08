CREATE TABLE live_operations (
    sequence INTEGER PRIMARY KEY,
    room_slug TEXT NOT NULL REFERENCES live_rooms(slug) ON DELETE CASCADE,
    operation_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL CHECK(length(fingerprint) = 64),
    stream_kind TEXT NOT NULL CHECK(stream_kind IN ('metadata', 'document')),
    stream_id TEXT NOT NULL,
    base_revision INTEGER NOT NULL CHECK(base_revision >= 0),
    revision INTEGER NOT NULL CHECK(revision > 0),
    operation_kind TEXT NOT NULL CHECK(length(operation_kind) > 0),
    result_payload TEXT NOT NULL CHECK(length(result_payload) > 0),
    created_at INTEGER NOT NULL,
    UNIQUE(room_slug, operation_id)
) STRICT;

CREATE INDEX live_operations_room_sequence_idx
    ON live_operations(room_slug, sequence DESC);

CREATE INDEX live_operations_room_client_sequence_idx
    ON live_operations(room_slug, client_id, sequence DESC);
