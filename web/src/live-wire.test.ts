import { describe, expect, it } from "vitest";
import { decodeLiveWireEvent, liveJoinMessage } from "./live-wire";

describe("live WebSocket wire decoder", () => {
  it("builds a canonical snake_case join from the HTTP snapshot revisions", () => {
    expect(
      liveJoinMessage("session-1", "client-1", 3, [
        {
          id: "main",
          name: "main",
          language: "plaintext",
          content: "hello",
          revision: 4,
          position: 0,
        },
      ]),
    ).toEqual({
      type: "join",
      session_id: "session-1",
      client_id: "client-1",
      metadata_revision: 3,
      document_revisions: [{ document_id: "main", revision: 4 }],
    });
  });

  it("decodes and normalizes participant state without accepting document bodies in joined", () => {
    expect(
      decodeLiveWireEvent({
        type: "joined",
        expires_at: "2026-08-08T12:00:00+00:00",
        metadata_revision: 1,
        reconnected: false,
        creator: true,
        watch_only: false,
        document_revisions: [{ document_id: "main", revision: 2 }],
        participants: [
          {
            id: "p-1",
            nickname: "calm otter",
            role: "writer",
            color: "#123456",
            current_tab: "main",
            joined_at: "2026-08-08T10:00:00+00:00",
            last_seen_at: "2026-08-08T10:01:00+00:00",
            status: "connected",
            cursor: {
              document_id: "main",
              revision: 2,
              anchor: 1,
              head: 3,
            },
          },
        ],
        participant: {
          id: "p-1",
          nickname: "calm otter",
          role: "writer",
          color: "#123456",
          current_tab: "main",
          joined_at: "2026-08-08T10:00:00+00:00",
          last_seen_at: "2026-08-08T10:01:00+00:00",
          status: "connected",
        },
      }),
    ).toMatchObject({
      type: "joined",
      creator: true,
      watchOnly: false,
      expiresAt: "2026-08-08T12:00:00.000Z",
      documentRevisions: [{ documentID: "main", revision: 2 }],
      participants: [
        {
          currentTab: "main",
          joinedAt: "2026-08-08T10:00:00.000Z",
          cursor: { documentID: "main", revision: 2, anchor: 1, head: 3 },
        },
      ],
    });
  });

  it("decodes creator control events and rejects invalid role updates", () => {
    const participant = {
      id: "p-1",
      nickname: "calm otter",
      role: "watch_only",
      color: "#123456",
      current_tab: "main",
      joined_at: "2026-08-08T10:00:00Z",
      last_seen_at: "2026-08-08T10:01:00Z",
      status: "connected",
    };
    expect(
      decodeLiveWireEvent({
        type: "room_mode_changed",
        watch_only: true,
        participants: [participant],
      }),
    ).toMatchObject({ type: "room_mode_changed", watchOnly: true });
    expect(
      decodeLiveWireEvent({
        type: "participant_removed",
        participant_id: "p-1",
      }),
    ).toEqual({ type: "participant_removed", participantID: "p-1" });
    expect(
      decodeLiveWireEvent({
        type: "room_mode_changed",
        watch_only: true,
        participants: [{ ...participant, role: "owner" }],
      }),
    ).toBeUndefined();
  });

  it("rejects malformed participant, revision, and structural wire fields", () => {
    for (const event of [
      { type: "joined", metadata_revision: 0, document_revisions: [] },
      {
        type: "changes",
        operation_id: "op",
        client_id: "client",
        document_id: "main",
        base_version: -1,
        revision: 1,
        changes: [],
      },
      {
        type: "presence_updated",
        participant: {
          id: "p",
          nickname: "name",
          role: "writer",
          color: "#123456",
          current_tab: 7,
          joined_at: "not-a-time",
          last_seen_at: "2026-08-08T10:00:00Z",
          status: "connected",
        },
      },
      {
        type: "document_reordered",
        operation_id: "op",
        client_id: "client",
        metadata_revision: 1,
        order: ["main", 2],
      },
    ]) {
      expect(decodeLiveWireEvent(event)).toBeUndefined();
    }
  });

  it("retains operation error status so the room can choose retry, resync, or recovery", () => {
    expect(
      decodeLiveWireEvent({
        type: "error",
        code: "service_unavailable",
        status: "retryable",
        message: "Live operation could not be applied",
        operation_id: "op-stable",
      }),
    ).toMatchObject({
      type: "error",
      code: "service_unavailable",
      status: "retryable",
      operationID: "op-stable",
    });
  });
});
