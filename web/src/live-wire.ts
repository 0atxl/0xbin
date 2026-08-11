import type { LiveRoomDocument } from "./live-api";

export type LiveCursor = {
  documentID: string;
  revision: number;
  anchor: number;
  head: number;
};

export type LiveConnectionCursor = LiveCursor & {
  connectionID: string;
};

export type LiveParticipant = {
  id: string;
  nickname: string;
  role: "writer" | "watch_only";
  accessClass: "creator" | "collaborator" | "viewer";
  canEdit: boolean;
  connectionCount: number;
  joinedAt: string;
  color: string;
  currentTab: string;
  cursor?: LiveCursor;
  cursors: LiveConnectionCursor[];
  status: "connected" | "connection_lost" | "offline";
  lastSeenAt: string;
};

export type LiveDocumentRevision = {
  documentID: string;
  revision: number;
};

export type LiveWireEvent =
  | {
      type: "joined";
      expiresAt: string;
      metadataRevision: number;
      documentRevisions: LiveDocumentRevision[];
      participants: LiveParticipant[];
      participant: LiveParticipant;
      reconnected: boolean;
      creator: boolean;
      watchOnly: boolean;
    }
  | {
      type: "changes";
      operationID: string;
      clientID: string;
      documentID: string;
      baseVersion: number;
      revision: number;
      changes: unknown;
    }
  | {
      type: "document_created";
      operationID: string;
      clientID: string;
      metadataRevision: number;
      document: LiveRoomDocument;
    }
  | {
      type: "document_updated";
      operationID: string;
      clientID: string;
      documentID: string;
      metadataRevision: number;
      name: string;
      language: string;
    }
  | {
      type: "document_deleted";
      operationID: string;
      clientID: string;
      documentID: string;
      metadataRevision: number;
    }
  | {
      type: "document_reordered";
      operationID: string;
      clientID: string;
      metadataRevision: number;
      order: string[];
    }
  | {
      type: "presence_joined" | "presence_updated" | "participant_renamed";
      participant: LiveParticipant;
    }
  | {
      type: "presence_left";
      participantID: string;
      participant?: LiveParticipant;
    }
  | {
      type: "room_mode_changed";
      watchOnly: boolean;
      participants: LiveParticipant[];
    }
  | { type: "participant_removed"; participantID: string }
  | {
      type: "status";
      status:
        "http_resync_required" | "rate_limited" | "synchronized" | "expired";
    }
  | {
      type: "error";
      code: string;
      message: string;
      operationID?: string;
      status?: string;
    };

export function liveJoinMessage(
  sessionID: string,
  connectionID: string,
  clientID: string,
  metadataRevision: number,
  documents: LiveRoomDocument[],
  preferredName?: string,
) {
  return {
    type: "join" as const,
    session_id: sessionID,
    connection_id: connectionID,
    client_id: clientID,
    ...(preferredName ? { preferred_name: preferredName } : {}),
    metadata_revision: metadataRevision,
    document_revisions: documents.map((document) => ({
      document_id: document.id,
      revision: document.revision,
    })),
  };
}

export function decodeLiveWireEvent(value: unknown): LiveWireEvent | undefined {
  const event = record(value);
  if (!event || typeof event.type !== "string") return undefined;
  switch (event.type) {
    case "joined": {
      const expiresAt = timestamp(event.expires_at);
      const metadataRevision = nonnegativeInteger(event.metadata_revision);
      const documentRevisions = decodeDocumentRevisions(
        event.document_revisions,
      );
      const participants = array(event.participants)?.map(decodeParticipant);
      const participant = decodeParticipant(event.participant);
      if (
        !expiresAt ||
        metadataRevision === undefined ||
        !documentRevisions ||
        !participants ||
        participants.some((value) => !value) ||
        !participant ||
        typeof event.reconnected !== "boolean" ||
        typeof event.creator !== "boolean" ||
        typeof event.watch_only !== "boolean"
      ) {
        return undefined;
      }
      return {
        type: "joined",
        expiresAt,
        metadataRevision,
        documentRevisions,
        participants: participants as LiveParticipant[],
        participant,
        reconnected: event.reconnected,
        creator: event.creator,
        watchOnly: event.watch_only,
      };
    }
    case "changes":
      return strings(event, "operation_id", "client_id", "document_id") &&
        nonnegativeInteger(event.base_version) !== undefined &&
        nonnegativeInteger(event.revision) !== undefined &&
        "changes" in event
        ? {
            type: "changes",
            operationID: event.operation_id as string,
            clientID: event.client_id as string,
            documentID: event.document_id as string,
            baseVersion: nonnegativeInteger(event.base_version)!,
            revision: nonnegativeInteger(event.revision)!,
            changes: event.changes,
          }
        : undefined;
    case "document_created": {
      const document = decodeDocument(event.document);
      return strings(event, "operation_id", "client_id") &&
        nonnegativeInteger(event.metadata_revision) !== undefined &&
        document
        ? {
            type: "document_created",
            operationID: event.operation_id as string,
            clientID: event.client_id as string,
            metadataRevision: nonnegativeInteger(event.metadata_revision)!,
            document,
          }
        : undefined;
    }
    case "document_updated":
      return strings(
        event,
        "operation_id",
        "client_id",
        "document_id",
        "name",
        "language",
      ) && nonnegativeInteger(event.metadata_revision) !== undefined
        ? {
            type: "document_updated",
            operationID: event.operation_id as string,
            clientID: event.client_id as string,
            documentID: event.document_id as string,
            metadataRevision: nonnegativeInteger(event.metadata_revision)!,
            name: event.name as string,
            language: event.language as string,
          }
        : undefined;
    case "document_deleted":
      return strings(event, "operation_id", "client_id", "document_id") &&
        nonnegativeInteger(event.metadata_revision) !== undefined
        ? {
            type: "document_deleted",
            operationID: event.operation_id as string,
            clientID: event.client_id as string,
            documentID: event.document_id as string,
            metadataRevision: nonnegativeInteger(event.metadata_revision)!,
          }
        : undefined;
    case "document_reordered": {
      const order = array(event.order);
      return strings(event, "operation_id", "client_id") &&
        nonnegativeInteger(event.metadata_revision) !== undefined &&
        order?.every((value) => typeof value === "string")
        ? {
            type: "document_reordered",
            operationID: event.operation_id as string,
            clientID: event.client_id as string,
            metadataRevision: nonnegativeInteger(event.metadata_revision)!,
            order: order as string[],
          }
        : undefined;
    }
    case "presence_joined":
    case "presence_updated":
    case "participant_renamed": {
      const participant = decodeParticipant(event.participant);
      return participant ? { type: event.type, participant } : undefined;
    }
    case "presence_left": {
      if (typeof event.participant_id !== "string") return undefined;
      const participant =
        event.participant === undefined
          ? undefined
          : decodeParticipant(event.participant);
      return event.participant !== undefined && !participant
        ? undefined
        : {
            type: "presence_left",
            participantID: event.participant_id,
            ...(participant ? { participant } : {}),
          };
    }
    case "room_mode_changed": {
      const participants = array(event.participants)?.map(decodeParticipant);
      return typeof event.watch_only === "boolean" &&
        participants &&
        !participants.some((value) => !value)
        ? {
            type: "room_mode_changed",
            watchOnly: event.watch_only,
            participants: participants as LiveParticipant[],
          }
        : undefined;
    }
    case "participant_removed":
      return typeof event.participant_id === "string"
        ? { type: "participant_removed", participantID: event.participant_id }
        : undefined;
    case "status":
      return event.status === "http_resync_required" ||
        event.status === "rate_limited" ||
        event.status === "synchronized" ||
        event.status === "expired"
        ? { type: "status", status: event.status }
        : undefined;
    case "error":
      return typeof event.code === "string" && typeof event.message === "string"
        ? {
            type: "error",
            code: event.code,
            message: event.message,
            ...(typeof event.operation_id === "string"
              ? { operationID: event.operation_id }
              : {}),
            ...(typeof event.status === "string"
              ? { status: event.status }
              : {}),
          }
        : undefined;
    default:
      return undefined;
  }
}

function decodeDocumentRevisions(
  value: unknown,
): LiveDocumentRevision[] | undefined {
  const documents = array(value);
  if (!documents) return undefined;
  const result: LiveDocumentRevision[] = [];
  const seen = new Set<string>();
  for (const document of documents) {
    const item = record(document);
    if (
      !item ||
      typeof item.document_id !== "string" ||
      nonnegativeInteger(item.revision) === undefined ||
      seen.has(item.document_id)
    )
      return undefined;
    seen.add(item.document_id);
    result.push({
      documentID: item.document_id,
      revision: nonnegativeInteger(item.revision)!,
    });
  }
  return result;
}

function decodeDocument(value: unknown): LiveRoomDocument | undefined {
  const document = record(value);
  if (
    !document ||
    !strings(document, "id", "name", "language", "content") ||
    nonnegativeInteger(document.revision) === undefined ||
    nonnegativeInteger(document.position) === undefined
  )
    return undefined;
  return {
    id: document.id as string,
    name: document.name as string,
    language: document.language as string,
    content: document.content as string,
    revision: nonnegativeInteger(document.revision)!,
    position: nonnegativeInteger(document.position)!,
  };
}

function decodeParticipant(value: unknown): LiveParticipant | undefined {
  const participant = record(value);
  if (
    !participant ||
    !strings(participant, "id", "nickname", "color", "current_tab") ||
    !isParticipantStatus(participant.status) ||
    !isParticipantRole(participant.role)
  )
    return undefined;
  const joinedAt = timestamp(participant.joined_at);
  const lastSeenAt = timestamp(participant.last_seen_at);
  const cursor =
    participant.cursor === undefined
      ? undefined
      : decodeCursor(participant.cursor);
  const decodedCursors =
    participant.cursors === undefined
      ? []
      : array(participant.cursors)?.map(decodeConnectionCursor);
  const accessClass = isParticipantAccessClass(participant.access_class)
    ? participant.access_class
    : participant.role === "writer"
      ? "collaborator"
      : "viewer";
  const canEdit =
    typeof participant.can_edit === "boolean"
      ? participant.can_edit
      : participant.role === "writer";
  const connectionCount = nonnegativeInteger(participant.connection_count) ?? 1;
  if (
    !joinedAt ||
    !lastSeenAt ||
    (participant.cursor !== undefined && !cursor) ||
    !decodedCursors ||
    decodedCursors.some((value) => !value) ||
    connectionCount < 1
  )
    return undefined;
  return {
    id: participant.id as string,
    nickname: participant.nickname as string,
    role: participant.role,
    accessClass,
    canEdit,
    connectionCount,
    color: participant.color as string,
    currentTab: participant.current_tab as string,
    joinedAt,
    lastSeenAt,
    ...(cursor ? { cursor } : {}),
    cursors: decodedCursors as LiveConnectionCursor[],
    status: participant.status,
  };
}

function decodeConnectionCursor(
  value: unknown,
): LiveConnectionCursor | undefined {
  const cursor = record(value);
  const decoded = decodeCursor(value);
  return cursor && typeof cursor.connection_id === "string" && decoded
    ? { connectionID: cursor.connection_id, ...decoded }
    : undefined;
}

function decodeCursor(value: unknown): LiveCursor | undefined {
  const cursor = record(value);
  return cursor &&
    typeof cursor.document_id === "string" &&
    nonnegativeInteger(cursor.revision) !== undefined &&
    nonnegativeInteger(cursor.anchor) !== undefined &&
    nonnegativeInteger(cursor.head) !== undefined
    ? {
        documentID: cursor.document_id,
        revision: nonnegativeInteger(cursor.revision)!,
        anchor: nonnegativeInteger(cursor.anchor)!,
        head: nonnegativeInteger(cursor.head)!,
      }
    : undefined;
}

function record(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)
    : undefined;
}

function array(value: unknown): unknown[] | undefined {
  return Array.isArray(value) ? value : undefined;
}

function strings(value: Record<string, unknown>, ...keys: string[]): boolean {
  return keys.every((key) => typeof value[key] === "string");
}

function nonnegativeInteger(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0
    ? value
    : undefined;
}

function timestamp(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? undefined : date.toISOString();
}

function isParticipantStatus(
  value: unknown,
): value is LiveParticipant["status"] {
  return (
    value === "connected" || value === "connection_lost" || value === "offline"
  );
}

function isParticipantRole(value: unknown): value is LiveParticipant["role"] {
  return value === "writer" || value === "watch_only";
}

function isParticipantAccessClass(
  value: unknown,
): value is LiveParticipant["accessClass"] {
  return value === "creator" || value === "collaborator" || value === "viewer";
}
