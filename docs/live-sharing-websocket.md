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
an unauthorized request receives HTTP `401` and does not establish a
WebSocket. Process-wide connection ceilings and connection rate limits are
also checked before upgrade.

Browser-participant, connection-per-participant, writer, viewer, and total
participant limits are evaluated after the client supplies its identity in
`join`. A new browser participant counts once toward room capacity even when
it owns several connections. If the participant limit or the eight-connection
per-participant limit is full, the server closes the established socket with
code `1013` without sending `joined`.

Browsers can surface a pre-upgrade rejection only as an abnormal close. The
client probes the authenticated HTTP bootstrap before retrying such a close,
opens the password gate only for `password_required`, and uses bounded backoff
for transient failures. The probe and reconnect budgets prevent request loops.

After a successful `101` upgrade, the client must promptly send one text
`join` message:

```json
{
  "type": "join",
  "session_id": "room-scoped-browser-resume-credential",
  "connection_id": "mounted-page-connection-id",
  "client_id": "operation-stream-id",
  "preferred_name": "Quiet Otter",
  "metadata_revision": 4,
  "document_revisions": [{ "document_id": "doc-1", "revision": 12 }]
}
```

`session_id` is a random 256-bit room-scoped browser resume credential kept in
same-origin local storage. Normal tabs in the same browser profile share it;
different profiles, incognito sessions, devices, origins, and rooms do not.
The raw credential is never persisted by the server, logged, or broadcast. A
public participant ID is derived from the room slug and credential with a
domain-separated hash.

`connection_id` identifies one mounted page and is unique for each tab
connection. During the compatibility window it is optional; an older client
that omits it uses its `session_id` and therefore retains one-connection
behavior. `client_id` identifies that page's submitted operation stream.
`preferred_name` is considered only when the browser participant has no
authoritative room nickname; later tabs receive the existing nickname. The
revision set must describe the HTTP bootstrap from which the client is joining.
A malformed or missing join closes the socket with close code `1002`; a missing
session identity closes it with `1008`.

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
its authoritative acknowledgement or an explicit terminal error, so retries
do not create a second edit.

## Client messages

All messages are JSON text frames. Unknown fields, unknown `type` values, and
malformed change sets are rejected.

| Type | Required fields | Meaning |
| --- | --- | --- |
| `join` | `session_id`, revision set; optional `connection_id`, `client_id`, `preferred_name` | Complete the connection handshake. |
| `heartbeat` | — | Keep this connection active. |
| `ack` | current revisions | Acknowledge applied authority state. |
| `push_changes` | `operation_id`, `document_id`, `base_version`, `changes` | Submit one CodeMirror change set. |
| `document_create` | `operation_id`, `name`, `language`, `content` | Create a tab. |
| `document_update` | `operation_id`, `document_id`, `name` and/or `language` | Rename or retag a tab. |
| `document_delete` | `operation_id`, `document_id` | Delete a tab; the last tab cannot be deleted. |
| `document_reorder` | `operation_id`, `order` | Submit the complete tab order. |
| `presence` | `current_tab`, optional cursor selection | Send connection-specific tab/cursor state. |
| `participant_rename` | `name` | Rename the browser participant for this room. |
| `room_watch_only` | `watch_only` | Creator-only durable lock (`true`) or unlock (`false`). |

During the bounded compatibility window, the removed `participant_remove`
message receives a stable unsupported-operation error and cannot disconnect a
participant or mutate the roster.

The server bounds frame size, message rate, the operator-configured aggregate
room-content budget, tab count, browser-participant capacity, and connections
per participant. The same aggregate budget necessarily bounds each individual
document; there is no independent per-document setting. It never logs raw room
text, browser resume credentials, passwords, cookies, creator capabilities, or
WebSocket frames.

## Server events

`joined` contains `expires_at`, `metadata_revision`, `document_revisions`, the
full transient participant roster, the caller's participant record, `creator`,
`locked`, and `reconnected`. Each participant record includes its
`access_class` (`creator`, `collaborator`, or `viewer`), effective `can_edit`,
connection count, and any connection-specific presence. Authority updates are
one of `changes`, `document_created`, `document_updated`, `document_deleted`,
or `document_reordered`; each includes the accepted operation ID and revision.

Presence events are `presence_joined`, `presence_updated`,
`participant_renamed`, `presence_left`, and `participant_removed`. Closing one
socket removes only that connection's ephemeral presence. The participant
remains connected while another connection is active and otherwise remains
`connection_lost` during reconnect grace. When grace expires, the server emits
`participant_removed` solely as a natural presence-expiry event before deleting
the participant's transient state; no creator action can produce it.
`room_mode_changed` supplies the durable `locked` state and authoritative
roster. Locking never removes participants: the creator remains editable,
collaborators become temporarily read-only, and viewers remain read-only.

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
| `1008` | Policy violation, including a missing join session identity. |
| `1009` | Frame exceeds the configured live message limit. |
| `1013` | Room expired, participant or per-participant connection capacity is full, or the connection is overloaded. |

## Capacity and creator boundary

Each room has configured collaborator, viewer, total browser-participant, tab,
and aggregate content limits, plus a fixed maximum of eight concurrent
connections for one browser participant. Pre-upgrade checks cover process-wide
connection capacity; per-room participant and per-participant connection
capacity are enforced by `join` after upgrade.

The creator capability is a separate random 256-bit value in an HttpOnly,
SameSite=Strict, room-scoped cookie. Only its domain-separated SHA-256 hash is
stored with the room in SQLite, so creator authority survives reconnects and
process restarts until room expiry without exposing the capability in JSON or
URLs. It authorizes durable room lock/unlock and gives its holder the creator
access class; it is not an account. Ordinary password unlock grants room access
but not creator authority. LiveBin intentionally provides no participant kick,
ban, promotion, demotion, or per-user role-management messages.
