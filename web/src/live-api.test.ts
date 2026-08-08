import { describe, expect, it, vi } from "vitest";
import {
  createLiveAPI,
  createLiveRoom,
  getLiveServiceConfig,
  getLiveRoom,
  LiveAPIError,
  liveAPIURL,
  liveWebSocketURL,
  probeLiveRoomReconnect,
} from "./live-api";

describe("live API client", () => {
  it("keeps live API URLs fragment-free and in the live namespace", () => {
    expect(liveAPIURL("https://0xbin.app/#secret", "/api/v1/live")).toBe(
      "https://0xbin.app/api/v1/live",
    );
    expect(() => liveAPIURL("https://0xbin.app", "/api/v1/pastes")).toThrow(
      "live API paths",
    );
  });

  it("builds same-origin WebSocket URLs without query or fragment data", () => {
    expect(
      liveWebSocketURL("https://0xbin.app/#secret", "quiet bright otter"),
    ).toBe("wss://0xbin.app/api/v1/live/quiet%20bright%20otter/ws");
  });

  it("sends the password only in the room-create request body", async () => {
    const request = vi.fn().mockResolvedValue({
      slug: "quietbrightotter",
      url: "https://0xbin.app/live/quietbrightotter",
      expires_at: "2026-08-06T12:00:00Z",
      password_required: true,
    });
    const created = await createLiveRoom(
      { request },
      {
        password: "correct horse battery staple",
        documents: [{ name: "main", language: "plaintext", content: "" }],
      },
    );
    expect(created).toEqual({
      slug: "quietbrightotter",
      url: "https://0xbin.app/live/quietbrightotter",
      expiresAt: "2026-08-06T12:00:00Z",
      passwordRequired: true,
    });
    expect(request.mock.calls[0][1].body).toContain("correct horse");
    expect(request.mock.calls[0][1].body).not.toContain("https://");
    expect(JSON.stringify(created)).not.toContain("correct horse");
  });

  it("decodes a room snapshot and forwards cancellation", async () => {
    const request = vi.fn().mockResolvedValue({
      slug: "quietbrightotter",
      expires_at: "2026-08-06T12:00:00Z",
      password_required: false,
      metadata_revision: 0,
      max_bytes: 1048576,
      max_tabs: 8,
      max_writers: 10,
      max_viewers: 100,
      max_participants: 110,
      room_lifetime_seconds: 86400,
      accepted_operation_ids: ["committed-without-ack"],
      documents: [
        {
          id: "main",
          name: "main",
          language: "plaintext",
          content: "hello",
          revision: 0,
        },
      ],
    });
    const controller = new AbortController();
    await expect(
      getLiveRoom(
        { request },
        "quietbrightotter",
        controller.signal,
        "reconcile-client",
      ),
    ).resolves.toMatchObject({
      slug: "quietbrightotter",
      maxBytes: 1048576,
      maxTabs: 8,
      documents: [{ content: "hello" }],
      acceptedOperationIDs: ["committed-without-ack"],
    });
    expect(request).toHaveBeenCalledWith(
      "/api/v1/live/quietbrightotter",
      expect.objectContaining({
        signal: controller.signal,
        headers: { "X-0xbin-Live-Client-ID": "reconcile-client" },
      }),
    );
  });

  it("loads the operator's public create limits", async () => {
    const request = vi.fn().mockResolvedValue({
      max_bytes: 2 << 20,
      max_document_bytes: 2 << 20,
      max_tabs: 4,
      max_writers: 3,
      max_viewers: 7,
      max_participants: 10,
      room_lifetime_seconds: 7200,
    });
    await expect(getLiveServiceConfig({ request })).resolves.toEqual({
      maxBytes: 2 << 20,
      maxDocumentBytes: 2 << 20,
      maxTabs: 4,
      maxWriters: 3,
      maxViewers: 7,
      maxParticipants: 10,
      roomLifetimeSeconds: 7200,
    });
    expect(request).toHaveBeenCalledWith(
      "/api/v1/live/config",
      expect.objectContaining({ cache: "no-store" }),
    );
  });

  it("maps password-required responses to a focused live error", async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          error: {
            code: "password_required",
            message: "Room password required",
          },
        }),
        { status: 401, headers: { "Content-Type": "application/json" } },
      ),
    );
    const api = createLiveAPI(fetcher, "https://0xbin.app");
    await expect(api.request("/api/v1/live/quietbrightotter")).rejects.toEqual(
      expect.objectContaining<Partial<LiveAPIError>>({
        name: "LiveAPIError",
        status: 401,
        code: "password_required",
      }),
    );
  });

  it("classifies protected reconnect bootstrap outcomes", async () => {
    const request = vi.fn();
    const api = { request };

    request.mockRejectedValueOnce(
      new LiveAPIError(401, {
        code: "password_required",
        message: "Password required",
      }),
    );
    await expect(
      probeLiveRoomReconnect(api, "quietbrightotter", "client-one"),
    ).resolves.toBe("authentication_required");

    request.mockRejectedValueOnce(
      new LiveAPIError(404, { code: "not_found", message: "Not found" }),
    );
    await expect(probeLiveRoomReconnect(api, "quietbrightotter")).resolves.toBe(
      "room_unavailable",
    );

    request.mockRejectedValueOnce(new LiveAPIError(0, "network_error"));
    await expect(probeLiveRoomReconnect(api, "quietbrightotter")).resolves.toBe(
      "retry",
    );
  });
});
