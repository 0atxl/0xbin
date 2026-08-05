import { describe, expect, it, vi } from "vitest";
import {
  createLiveAPI,
  createLiveRoom,
  getLiveRoom,
  LiveAPIError,
  liveAPIURL,
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
      getLiveRoom({ request }, "quietbrightotter", controller.signal),
    ).resolves.toMatchObject({
      slug: "quietbrightotter",
      documents: [{ content: "hello" }],
    });
    expect(request).toHaveBeenCalledWith(
      "/api/v1/live/quietbrightotter",
      expect.objectContaining({ signal: controller.signal }),
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
});
