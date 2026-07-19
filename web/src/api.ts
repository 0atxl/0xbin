export type APIErrorCode =
  | "invalid_request"
  | "payload_too_large"
  | "rate_limited"
  | "not_found"
  | "service_unavailable"
  | "internal_error";

export type APIError = {
  code: APIErrorCode;
  message: string;
  requestID?: string;
};

export class PasteAPIError extends Error {
  readonly status: number;
  readonly code: APIErrorCode | "network_error";
  readonly requestID?: string;

  constructor(status: number, error: APIError | "network_error") {
    const details = error === "network_error" ? undefined : error;
    super(details?.message ?? "The service is temporarily unavailable.");
    this.name = "PasteAPIError";
    this.status = status;
    this.code = details?.code ?? "network_error";
    this.requestID = details?.requestID;
  }
}

export type PasteAPI = {
  request<Response>(path: string, init?: RequestInit): Promise<Response>;
};

export type PlaintextPasteRequest = {
  title: string;
  language: string;
  content: string;
  expiry: "1h" | "24h" | "72h";
  burnAfterRead: boolean;
};

export type EncryptedPasteRequest = {
  envelope: CiphertextEnvelope;
  expiry: "1h" | "24h" | "72h";
  burnAfterRead: boolean;
};

export type CreatedPaste = {
  slug: string;
  url: string;
  expiresAt: string;
};

export type PlaintextPayload = {
  version: 1;
  title: string;
  language: string;
  content: string;
};

export type CiphertextEnvelope = {
  version: 1;
  algorithm: "A256GCM";
  iv: string;
  ciphertext: string;
};

export type RetrievedPaste = {
  slug: string;
  payload: PlaintextPayload;
  expiresAt: string;
  createdAt: string;
};

export type RetrievedEncryptedPaste = {
  slug: string;
  envelope: CiphertextEnvelope;
  expiresAt: string;
  createdAt: string;
};

export type BurnMetadata = {
  burnAfterRead: true;
  isEncrypted: boolean;
};

export function createPasteAPI(
  fetcher: typeof fetch = fetch,
  origin = window.location.origin,
): PasteAPI {
  return {
    async request<Response>(path: string, init: RequestInit = {}) {
      const response = await request(fetcher, origin, path, init);
      return response as Response;
    },
  };
}

export function apiURL(origin: string, path: string): string {
  if (!path.startsWith("/api/")) {
    throw new Error("API paths must start with /api/");
  }
  const destination = new URL(path, origin);
  // Deliberately construct API requests from a path only. Browser URL fragments
  // may hold encryption keys, but they are never included in these URLs.
  destination.hash = "";
  return destination.toString();
}

export async function createPlaintextPaste(
  api: PasteAPI,
  request: PlaintextPasteRequest,
): Promise<CreatedPaste> {
  const response = await api.request<unknown>("/api/v1/pastes", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      mode: "plaintext",
      payload: {
        version: 1,
        title: request.title,
        language: request.language,
        content: request.content,
      },
      expiry: request.expiry,
      burn_after_read: request.burnAfterRead,
    }),
  });
  if (!isCreatedPaste(response)) {
    throw new PasteAPIError(0, "network_error");
  }
  return {
    slug: response.slug,
    url: response.url,
    expiresAt: response.expires_at,
  };
}

export async function createEncryptedPaste(
  api: PasteAPI,
  request: EncryptedPasteRequest,
): Promise<CreatedPaste> {
  const response = await api.request<unknown>("/api/v1/pastes", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      mode: "encrypted",
      payload: request.envelope,
      expiry: request.expiry,
      burn_after_read: request.burnAfterRead,
    }),
  });
  if (!isCreatedPaste(response)) throw new PasteAPIError(0, "network_error");
  return {
    slug: response.slug,
    url: response.url,
    expiresAt: response.expires_at,
  };
}

export async function getPaste(
  api: PasteAPI,
  slug: string,
  signal?: AbortSignal,
): Promise<RetrievedPaste | RetrievedEncryptedPaste | BurnMetadata> {
  const response = await api.request<unknown>(
    `/api/v1/pastes/${encodeURIComponent(slug)}`,
    signal ? { signal } : undefined,
  );
  if (isBurnMetadata(response)) {
    return {
      burnAfterRead: true,
      isEncrypted: response.is_encrypted,
    };
  }
  if (isRetrievedEncryptedPaste(response)) {
    return {
      slug: response.slug,
      envelope: response.envelope,
      expiresAt: response.expires_at,
      createdAt: response.created_at,
    };
  }
  if (!isRetrievedPaste(response)) {
    throw new PasteAPIError(0, "network_error");
  }
  return {
    slug: response.slug,
    payload: response.payload,
    expiresAt: response.expires_at,
    createdAt: response.created_at,
  };
}

export async function consumePaste(
  api: PasteAPI,
  slug: string,
): Promise<RetrievedPaste | RetrievedEncryptedPaste> {
  const response = await api.request<unknown>(
    `/api/v1/pastes/${encodeURIComponent(slug)}/consume`,
    { method: "POST" },
  );
  if (isRetrievedEncryptedPaste(response, true)) {
    return {
      slug: response.slug,
      envelope: response.envelope,
      expiresAt: response.expires_at,
      createdAt: response.created_at,
    };
  }
  if (isRetrievedPaste(response, true)) {
    return {
      slug: response.slug,
      payload: response.payload,
      expiresAt: response.expires_at,
      createdAt: response.created_at,
    };
  }
  throw new PasteAPIError(0, "network_error");
}

async function request(
  fetcher: typeof fetch,
  origin: string,
  path: string,
  init: RequestInit,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetcher(apiURL(origin, path), {
      ...init,
      headers: {
        Accept: "application/json",
        ...init.headers,
      },
    });
  } catch {
    throw new PasteAPIError(0, "network_error");
  }

  if (!response.ok) {
    throw new PasteAPIError(response.status, await decodeError(response));
  }
  if (response.status === 204) {
    return undefined;
  }
  return response.json();
}

async function decodeError(response: Response): Promise<APIError> {
  try {
    const body: unknown = await response.json();
    if (
      typeof body === "object" &&
      body !== null &&
      "error" in body &&
      typeof body.error === "object" &&
      body.error !== null &&
      "code" in body.error &&
      "message" in body.error &&
      typeof body.error.code === "string" &&
      typeof body.error.message === "string"
    ) {
      return {
        code: normalizeErrorCode(body.error.code),
        message: body.error.message,
        requestID:
          "request_id" in body.error &&
          typeof body.error.request_id === "string"
            ? body.error.request_id
            : undefined,
      };
    }
  } catch {
    // Public API failures stay generic when a proxy returns non-JSON content.
  }
  return {
    code: "service_unavailable",
    message: "The service is temporarily unavailable.",
  };
}

function normalizeErrorCode(value: string): APIErrorCode {
  const codes: APIErrorCode[] = [
    "invalid_request",
    "payload_too_large",
    "rate_limited",
    "not_found",
    "service_unavailable",
    "internal_error",
  ];
  return codes.includes(value as APIErrorCode)
    ? (value as APIErrorCode)
    : "service_unavailable";
}

function isCreatedPaste(value: unknown): value is {
  slug: string;
  url: string;
  expires_at: string;
} {
  return (
    typeof value === "object" &&
    value !== null &&
    "slug" in value &&
    "url" in value &&
    "expires_at" in value &&
    typeof value.slug === "string" &&
    typeof value.url === "string" &&
    typeof value.expires_at === "string"
  );
}

function isBurnMetadata(value: unknown): value is {
  burn_after_read: true;
  is_encrypted: boolean;
} {
  return (
    typeof value === "object" &&
    value !== null &&
    "burn_after_read" in value &&
    value.burn_after_read === true &&
    "is_encrypted" in value &&
    typeof value.is_encrypted === "boolean"
  );
}

function isRetrievedPaste(
  value: unknown,
  allowBurnAfterRead = false,
): value is {
  slug: string;
  payload: PlaintextPayload;
  is_encrypted: false;
  burn_after_read: false;
  expires_at: string;
  created_at: string;
} {
  if (
    typeof value !== "object" ||
    value === null ||
    !("payload" in value) ||
    typeof value.payload !== "object" ||
    value.payload === null
  ) {
    return false;
  }
  const payload = value.payload;
  return (
    "slug" in value &&
    typeof value.slug === "string" &&
    "is_encrypted" in value &&
    value.is_encrypted === false &&
    "burn_after_read" in value &&
    typeof value.burn_after_read === "boolean" &&
    (allowBurnAfterRead || value.burn_after_read === false) &&
    "expires_at" in value &&
    typeof value.expires_at === "string" &&
    "created_at" in value &&
    typeof value.created_at === "string" &&
    "version" in payload &&
    payload.version === 1 &&
    "title" in payload &&
    typeof payload.title === "string" &&
    "language" in payload &&
    typeof payload.language === "string" &&
    "content" in payload &&
    typeof payload.content === "string"
  );
}

function isRetrievedEncryptedPaste(
  value: unknown,
  allowBurnAfterRead = false,
): value is {
  slug: string;
  envelope: CiphertextEnvelope;
  is_encrypted: true;
  burn_after_read: false;
  expires_at: string;
  created_at: string;
} {
  if (
    typeof value !== "object" ||
    value === null ||
    !("envelope" in value) ||
    typeof value.envelope !== "object" ||
    value.envelope === null
  ) {
    return false;
  }
  const envelope = value.envelope;
  return (
    "slug" in value &&
    typeof value.slug === "string" &&
    "is_encrypted" in value &&
    value.is_encrypted === true &&
    "burn_after_read" in value &&
    typeof value.burn_after_read === "boolean" &&
    (allowBurnAfterRead || value.burn_after_read === false) &&
    "expires_at" in value &&
    typeof value.expires_at === "string" &&
    "created_at" in value &&
    typeof value.created_at === "string" &&
    "version" in envelope &&
    envelope.version === 1 &&
    "algorithm" in envelope &&
    envelope.algorithm === "A256GCM" &&
    "iv" in envelope &&
    typeof envelope.iv === "string" &&
    "ciphertext" in envelope &&
    typeof envelope.ciphertext === "string"
  );
}
