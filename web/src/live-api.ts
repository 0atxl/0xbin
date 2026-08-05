import { apiURL } from "./api";

export type LiveAPIErrorCode =
  | "invalid_request"
  | "payload_too_large"
  | "rate_limited"
  | "not_found"
  | "service_unavailable"
  | "internal_error"
  | "password_required"
  | "invalid_password"
  | "room_limit_reached"
  | "room_expired"
  | "message_too_large"
  | "connection_limit_reached";

export type LiveAPIErrorBody = {
  code: LiveAPIErrorCode;
  message: string;
  requestID?: string;
};

export class LiveAPIError extends Error {
  readonly status: number;
  readonly code: LiveAPIErrorCode | "network_error";
  readonly requestID?: string;

  constructor(status: number, error: LiveAPIErrorBody | "network_error") {
    const details = error === "network_error" ? undefined : error;
    super(details?.message ?? "The service is temporarily unavailable.");
    this.name = "LiveAPIError";
    this.status = status;
    this.code = details?.code ?? "network_error";
    this.requestID = details?.requestID;
  }
}

export type LiveAPI = {
  request<Response>(path: string, init?: RequestInit): Promise<Response>;
};

export type LiveRoomDocument = {
  id: string;
  name: string;
  language: string;
  content: string;
  revision: number;
};

export type LiveRoomSnapshot = {
  slug: string;
  expiresAt: string;
  passwordRequired: boolean;
  metadataRevision: number;
  documents: LiveRoomDocument[];
};

export type LiveRoomCreateRequest = {
  password?: string;
  documents: Array<{
    name: string;
    language: string;
    content: string;
  }>;
};

export type CreatedLiveRoom = {
  slug: string;
  url: string;
  expiresAt: string;
  passwordRequired: boolean;
};

export function createLiveAPI(
  fetcher: typeof fetch = fetch,
  origin = globalThis.location?.origin ?? "http://localhost",
): LiveAPI {
  return {
    async request<Response>(path: string, init: RequestInit = {}) {
      return (await request(fetcher, origin, path, init)) as Response;
    },
  };
}

export function liveAPIURL(origin: string, path: string): string {
  if (path !== "/api/v1/live" && !path.startsWith("/api/v1/live/")) {
    throw new Error("live API paths must start with /api/v1/live");
  }
  return apiURL(origin, path);
}

export async function createLiveRoom(
  api: LiveAPI,
  request: LiveRoomCreateRequest,
): Promise<CreatedLiveRoom> {
  const response = await api.request<unknown>("/api/v1/live", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      ...(request.password ? { password: request.password } : {}),
      documents: request.documents,
    }),
  });
  if (!isCreatedLiveRoom(response)) {
    throw new LiveAPIError(0, "network_error");
  }
  return {
    slug: response.slug,
    url: response.url,
    expiresAt: response.expires_at,
    passwordRequired: response.password_required,
  };
}

export async function getLiveRoom(
  api: LiveAPI,
  slug: string,
  signal?: AbortSignal,
): Promise<LiveRoomSnapshot> {
  const response = await api.request<unknown>(
    `/api/v1/live/${encodeURIComponent(slug)}`,
    {
      credentials: "same-origin",
      cache: "no-store",
      ...(signal ? { signal } : {}),
    },
  );
  if (!isLiveRoomSnapshot(response)) {
    throw new LiveAPIError(0, "network_error");
  }
  return normalizeLiveRoomSnapshot(response);
}

export async function unlockLiveRoom(
  api: LiveAPI,
  slug: string,
  password: string,
): Promise<LiveRoomSnapshot> {
  const response = await api.request<unknown>(
    `/api/v1/live/${encodeURIComponent(slug)}/unlock`,
    {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password }),
    },
  );
  if (!isLiveRoomSnapshot(response)) {
    throw new LiveAPIError(0, "network_error");
  }
  return normalizeLiveRoomSnapshot(response);
}

async function request(
  fetcher: typeof fetch,
  origin: string,
  path: string,
  init: RequestInit,
): Promise<unknown> {
  let response: Response;
  try {
    response = await fetcher(liveAPIURL(origin, path), {
      ...init,
      headers: {
        Accept: "application/json",
        ...init.headers,
      },
    });
  } catch {
    throw new LiveAPIError(0, "network_error");
  }

  if (!response.ok) {
    throw new LiveAPIError(response.status, await decodeError(response));
  }
  if (response.status === 204) return undefined;
  return response.json();
}

async function decodeError(response: Response): Promise<LiveAPIErrorBody> {
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
        code: normalizeLiveErrorCode(body.error.code),
        message: body.error.message,
        requestID:
          "request_id" in body.error &&
          typeof body.error.request_id === "string"
            ? body.error.request_id
            : undefined,
      };
    }
  } catch {
    // Keep proxy failures generic and free of response-body details.
  }
  return {
    code: "service_unavailable",
    message: "The service is temporarily unavailable.",
  };
}

function normalizeLiveErrorCode(value: string): LiveAPIErrorCode {
  const codes: LiveAPIErrorCode[] = [
    "invalid_request",
    "payload_too_large",
    "rate_limited",
    "not_found",
    "service_unavailable",
    "internal_error",
    "password_required",
    "invalid_password",
    "room_limit_reached",
    "room_expired",
    "message_too_large",
    "connection_limit_reached",
  ];
  return codes.includes(value as LiveAPIErrorCode)
    ? (value as LiveAPIErrorCode)
    : "service_unavailable";
}

function isCreatedLiveRoom(value: unknown): value is {
  slug: string;
  url: string;
  expires_at: string;
  password_required: boolean;
} {
  return (
    typeof value === "object" &&
    value !== null &&
    "slug" in value &&
    typeof value.slug === "string" &&
    "url" in value &&
    typeof value.url === "string" &&
    "expires_at" in value &&
    typeof value.expires_at === "string" &&
    "password_required" in value &&
    typeof value.password_required === "boolean"
  );
}

function isLiveRoomSnapshot(value: unknown): value is {
  slug: string;
  expires_at: string;
  password_required: boolean;
  metadata_revision: number;
  documents: Array<{
    id: string;
    name: string;
    language: string;
    content: string;
    revision: number;
  }>;
} {
  if (
    typeof value !== "object" ||
    value === null ||
    !("slug" in value) ||
    typeof value.slug !== "string" ||
    !("expires_at" in value) ||
    typeof value.expires_at !== "string" ||
    !("password_required" in value) ||
    typeof value.password_required !== "boolean" ||
    !("metadata_revision" in value) ||
    typeof value.metadata_revision !== "number" ||
    !("documents" in value) ||
    !Array.isArray(value.documents)
  ) {
    return false;
  }
  return value.documents.every(
    (document) =>
      typeof document === "object" &&
      document !== null &&
      "id" in document &&
      typeof document.id === "string" &&
      "name" in document &&
      typeof document.name === "string" &&
      "language" in document &&
      typeof document.language === "string" &&
      "content" in document &&
      typeof document.content === "string" &&
      "revision" in document &&
      typeof document.revision === "number",
  );
}

function normalizeLiveRoomSnapshot(value: {
  slug: string;
  expires_at: string;
  password_required: boolean;
  metadata_revision: number;
  documents: Array<{
    id: string;
    name: string;
    language: string;
    content: string;
    revision: number;
  }>;
}): LiveRoomSnapshot {
  return {
    slug: value.slug,
    expiresAt: value.expires_at,
    passwordRequired: value.password_required,
    metadataRevision: value.metadata_revision,
    documents: value.documents.map((document) => ({ ...document })),
  };
}
