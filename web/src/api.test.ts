import { describe, expect, it, vi } from "vitest";
import {
  apiURL,
  createEncryptedPaste,
  createPasteAPI,
  createPlaintextPaste,
  consumePaste,
  getPaste,
  PasteAPIError,
} from "./api";

describe("paste API client", () => {
  it("constructs API URLs without a sharing fragment", () => {
    expect(apiURL("https://0xbin.app/#secret", "/api/v1/pastes")).toBe(
      "https://0xbin.app/api/v1/pastes",
    );
  });

  it("forwards viewer cancellation to the GET request", async () => {
    const request = vi.fn().mockResolvedValue({
      burn_after_read: true,
      is_encrypted: false,
    });
    const controller = new AbortController();

    await getPaste({ request }, "quietbrightotter", controller.signal);

    expect(request).toHaveBeenCalledWith("/api/v1/pastes/quietbrightotter", {
      signal: controller.signal,
    });
  });

  it("decodes stable API errors", async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          error: {
            code: "rate_limited",
            message: "Try again later",
            request_id: "req-1",
          },
        }),
        { status: 429, headers: { "Content-Type": "application/json" } },
      ),
    );
    const api = createPasteAPI(fetcher, "https://0xbin.app");

    await expect(api.request("/api/v1/pastes")).rejects.toMatchObject({
      name: PasteAPIError.name,
      status: 429,
      code: "rate_limited",
      requestID: "req-1",
    });
  });

  it("serializes a plaintext create request with server-controlled expiry", async () => {
    const request = vi.fn().mockResolvedValue({
      slug: "quietbrightotter",
      url: "https://0xbin.app/quietbrightotter",
      expires_at: "2026-07-22T12:00:00Z",
    });
    await expect(
      createPlaintextPaste(
        { request },
        {
          title: "Example",
          language: "plaintext",
          content: "hello",
          expiry: "24h",
          burnAfterRead: false,
        },
      ),
    ).resolves.toEqual({
      slug: "quietbrightotter",
      url: "https://0xbin.app/quietbrightotter",
      expiresAt: "2026-07-22T12:00:00Z",
    });
    expect(request).toHaveBeenCalledWith(
      "/api/v1/pastes",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("serializes an encrypted envelope without a key", async () => {
    const request = vi.fn().mockResolvedValue({
      slug: "quietbrightotter",
      url: "https://0xbin.app/quietbrightotter",
      expires_at: "2026-07-22T12:00:00Z",
    });
    await createEncryptedPaste(
      { request },
      {
        envelope: {
          version: 1,
          algorithm: "A256GCM",
          iv: "AAECAwQFBgcICQoL",
          ciphertext: "AAECAwQFBgcICQoLDA0ODw",
        },
        expiry: "24h",
        burnAfterRead: false,
      },
    );
    expect(request).toHaveBeenCalledWith(
      "/api/v1/pastes",
      expect.objectContaining({
        body: JSON.stringify({
          mode: "encrypted",
          payload: {
            version: 1,
            algorithm: "A256GCM",
            iv: "AAECAwQFBgcICQoL",
            ciphertext: "AAECAwQFBgcICQoLDA0ODw",
          },
          expiry: "24h",
          burn_after_read: false,
        }),
      }),
    );
    expect(request.mock.calls[0][1].body).not.toContain("key");
  });

  it("decodes an active plaintext paste", async () => {
    const request = vi.fn().mockResolvedValue({
      slug: "quietbrightotter",
      payload: {
        version: 1,
        title: "Example",
        language: "go",
        content: "package main",
      },
      is_encrypted: false,
      burn_after_read: false,
      expires_at: "2026-07-22T12:00:00Z",
      created_at: "2026-07-21T12:00:00Z",
    });
    await expect(
      getPaste({ request }, "quietbrightotter"),
    ).resolves.toMatchObject({
      slug: "quietbrightotter",
      payload: { content: "package main" },
    });
  });

  it("decodes an active encrypted paste envelope", async () => {
    const request = vi.fn().mockResolvedValue({
      slug: "quietbrightotter",
      envelope: {
        version: 1,
        algorithm: "A256GCM",
        iv: "AAECAwQFBgcICQoL",
        ciphertext: "AAECAwQFBgcICQoLDA0ODw",
      },
      is_encrypted: true,
      burn_after_read: false,
      expires_at: "2026-07-22T12:00:00Z",
      created_at: "2026-07-21T12:00:00Z",
    });
    await expect(
      getPaste({ request }, "quietbrightotter"),
    ).resolves.toMatchObject({
      slug: "quietbrightotter",
      envelope: { algorithm: "A256GCM" },
    });
  });

  it("decodes a consumed plaintext paste that retains burn metadata", async () => {
    const request = vi.fn().mockResolvedValue({
      slug: "quietbrightotter",
      payload: {
        version: 1,
        title: "Example",
        language: "go",
        content: "package main",
      },
      is_encrypted: false,
      burn_after_read: true,
      expires_at: "2026-07-22T12:00:00Z",
      created_at: "2026-07-21T12:00:00Z",
    });
    await expect(
      consumePaste({ request }, "quietbrightotter"),
    ).resolves.toMatchObject({
      slug: "quietbrightotter",
      payload: { content: "package main" },
    });
  });

  it("decodes a consumed encrypted paste that retains burn metadata", async () => {
    const request = vi.fn().mockResolvedValue({
      slug: "quietbrightotter",
      envelope: {
        version: 1,
        algorithm: "A256GCM",
        iv: "AAECAwQFBgcICQoL",
        ciphertext: "AAECAwQFBgcICQoLDA0ODw",
      },
      is_encrypted: true,
      burn_after_read: true,
      expires_at: "2026-07-22T12:00:00Z",
      created_at: "2026-07-21T12:00:00Z",
    });
    await expect(
      consumePaste({ request }, "quietbrightotter"),
    ).resolves.toMatchObject({
      slug: "quietbrightotter",
      envelope: { algorithm: "A256GCM" },
    });
  });

  it("rejects consumed paste responses with non-boolean burn metadata", async () => {
    const request = vi.fn().mockResolvedValue({
      slug: "quietbrightotter",
      payload: {
        version: 1,
        title: "Example",
        language: "go",
        content: "package main",
      },
      is_encrypted: false,
      burn_after_read: "true",
      expires_at: "2026-07-22T12:00:00Z",
      created_at: "2026-07-21T12:00:00Z",
    });

    await expect(
      consumePaste({ request }, "quietbrightotter"),
    ).rejects.toMatchObject({
      name: PasteAPIError.name,
      code: "network_error",
    });
  });
});
