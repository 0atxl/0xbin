# LiveBin WebSocket Contract

This document defines the version-1 LiveBin WebSocket transport. It is used
with the HTTP room bootstrap in [`openapi.yaml`](openapi.yaml). The transport
is server-authoritative: clients do not negotiate peer-to-peer state, and all
field names on the wire use `snake_case`.

## Handshake

Connect to `GET /api/v1/live/{slug}/ws` with an exact same-origin `Origin`
header. Invalid origins receive HTTP `403`. Missing, expired, or otherwise
unavailable rooms use the generic HTTP `404` response. A protected room
requires its valid, short-lived, HttpOnly room-access cookie before upgrade;
an unauthorized request receives HTTP `401` and does not establish a WebSocket.
Browsers can surface that pre-upgrade rejection only as an abnormal close. The
client probes the authenticated HTTP bootstrap before retrying such a close,
opens the password gate only for `password_required`, and uses bounded backoff
for transient failures. The probe and reconnect budgets prevent request loops.

After a successful `101` upgrade, the client must promptly send one text
`join` message:

```json
{
  "type": "join",
  "session_id": "stable-browser-session-id",
  "client_id": "stable-client-id",
  "metadata_revision": 4,
  "document_revisions": [{ "document_id": "doc-1", "revision": 12 }]
}
```

`session_id` preserves a participant identity across reconnect grace;
`client_id` identifies submitted operations. Both are client-generated opaque
identifiers and remain stable while the same page renews an expired access
session. The revision set must describe the HTTP bootstrap from which the client
is joining. A malformed or missing join closes the socket with close code
`1002`; a missing session ID closes it with `1008`.

## Ordering and resynchronization

The server serializes accepted room changes. It sends `joined` first, including
the authoritative roster and current metadata/document revisions. It then
sends retained metadata and document deltas newer than the join revision set.
If bounded retained history cannot bridge that set, it sends
`{"type":"status","status":"http_resync_required"}` instead; the client
must fetch the HTTP bootstrap again before resuming structural operations.
Only the newest HTTP request generation may change authority. Durable events
received while that request is in flight are retained in a fixed-size buffer
and replayed once, in wire order, after a non-regressing snapshot. Events
published before a replacement request are covered by the replacement
snapshot. A false reconciliation, event gap, or transient request failure uses
bounded backoff; a full buffer starts a replacement snapshot. Exhausting the
attempt or time budget stops collaboration in recovery without discarding
local editor text.

Document changes use a document `revision`; tab create/update/delete/reorder
use the room `metadata_revision`. A client acknowledges received revisions with
`ack`, and bases a document change on `base_version`. A rejected stale or
conflicting operation is an `error` event with a resync status, not an
unannounced client-side merge.

Every mutating client message carries a stable, unique `operation_id`. The
server includes it on accepted document and metadata events, and may mark a
replayed duplicate with `duplicate: true`. Clients retain an operation ID until
its authoritative acknowledgement or an explicit terminal error, so retries do
not create a second edit.

## Client messages

All messages are JSON text frames. Unknown fields, unknown `type` values, and
malformed change sets are rejected.

| Type | Required fields | Meaning |
| --- | --- | --- |
| `join` | `session_id`, revision set | Complete the connection handshake. |
| `heartbeat` | — | Keep the participant active. |
| `ack` | current revisions | Acknowledge applied authority state. |
| `push_changes` | `operation_id`, `document_id`, `base_version`, `changes` | Submit one CodeMirror change set. |
| `document_create` | `operation_id`, `name`, `language`, `content` | Create a tab. |
| `document_update` | `operation_id`, `document_id`, `name` and/or `language` | Rename or retag a tab. |
| `document_delete` | `operation_id`, `document_id` | Delete a tab; the last tab cannot be deleted. |
| `document_reorder` | `operation_id`, `order` | Submit the complete tab order. |
| `presence` | `current_tab`, optional cursor selection | Send ephemeral tab/cursor state. |
| `participant_rename` | `name` | Rename the caller for this room session. |
| `room_watch_only` | `watch_only` | Creator-only room mode change. |
| `participant_remove` | `participant_id` | Creator-only active-session removal. |

The server bounds frame size, message rate, aggregate room bytes, per-document
bytes, tab count, and participant capacity. It never logs raw room text,
passwords, cookies, creator capabilities, or WebSocket frames.

## Server events

`joined` contains `expires_at`, `metadata_revision`, `document_revisions`, the
full process-local participant roster, the caller's participant record,
`creator`, `watch_only`, and `reconnected`. Authority updates are one of
`changes`, `document_created`, `document_updated`, `document_deleted`, or
`document_reordered`; each includes the accepted operation ID and revision.

Presence events are `presence_joined`, `presence_updated`,
`participant_renamed`, `presence_left`, and `participant_removed`. A socket
close produces `presence_left` and keeps the participant as
`connection_lost` during reconnect grace. When that grace expires, the server
deterministically emits `participant_removed` before deleting its transient
presence and cursor state. `room_mode_changed` supplies the authoritative
watch-only mode and roster after a creator change.

`status` values are `http_resync_required`, `rate_limited`, `synchronized`,
and `expired`. `error` contains a stable public `code`, concise message, and
when applicable an `operation_id` and status (`validation`, `resync_required`,
`auth_required`, `overloaded`, `retryable`, or `expired`).

## Close behavior

| Code | Meaning |
| --- | --- |
| `1000` | Normal client/server shutdown. |
| `1001` | Peer write or heartbeat failed, or service is shutting down. |
| `1002` | A required join or protocol envelope was malformed. |
| `1008` | Policy violation, including a removed participant or missing join session ID. |
| `1009` | Frame exceeds the configured live message limit. |
| `1013` | Room expired, participant capacity is full, or outbound connection is overloaded. |

## Capacity and creator boundary

Each room has configured writer, watch-only viewer, total participant, tab,
and content limits. A creator capability is a separate opaque, HttpOnly,
room-scoped cookie backed only by process memory. It authorizes only switching
the whole room to watch-only and removing active sessions; it is not an
account, is never stored in SQLite, and does not survive a process restart.
Ordinary password unlock grants room access but not creator authority.
