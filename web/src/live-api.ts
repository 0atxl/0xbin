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

export function liveWebSocketURL(origin: string, slug: string): string {
  const url = new URL(`/api/v1/live/${encodeURIComponent(slug)}/ws`, origin);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.search = "";
  url.hash = "";
  return url.toString();
}

export type LiveRoomDocument = {
  id: string;
  name: string;
  language: string;
  content: string;
  revision: number;
  position?: number;
};

export type LiveRoomSnapshot = {
  slug: string;
  expiresAt: string;
  passwordRequired: boolean;
  metadataRevision: number;
  maxBytes: number;
  maxTabs: number;
  maxWriters: number;
  maxViewers: number;
  maxParticipants: number;
  roomLifetimeSeconds: number;
  documents: LiveRoomDocument[];
  acceptedOperationIDs: string[];
};

export type LiveServiceConfig = {
  maxBytes: number;
  maxDocumentBytes: number;
  maxTabs: number;
  maxWriters: number;
  maxViewers: number;
  maxParticipants: number;
  roomLifetimeSeconds: number;
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
  clientID?: string,
): Promise<LiveRoomSnapshot> {
  const response = await api.request<unknown>(
    `/api/v1/live/${encodeURIComponent(slug)}`,
    {
      credentials: "same-origin",
      cache: "no-store",
      ...(clientID ? { headers: { "X-0xbin-Live-Client-ID": clientID } } : {}),
      ...(signal ? { signal } : {}),
    },
  );
  if (!isLiveRoomSnapshot(response)) {
    throw new LiveAPIError(0, "network_error");
  }
  return normalizeLiveRoomSnapshot(response);
}

export async function getLiveServiceConfig(
  api: LiveAPI,
  signal?: AbortSignal,
): Promise<LiveServiceConfig> {
  const response = await api.request<unknown>("/api/v1/live/config", {
    credentials: "same-origin",
    cache: "no-store",
    ...(signal ? { signal } : {}),
  });
  if (!isLiveServiceConfig(response)) {
    throw new LiveAPIError(0, "network_error");
  }
  return normalizeLiveServiceConfig(response);
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
  max_bytes: number;
  max_tabs: number;
  max_writers: number;
  max_viewers: number;
  max_participants: number;
  room_lifetime_seconds: number;
  documents: Array<{
    id: string;
    name: string;
    language: string;
    content: string;
    revision: number;
    position?: number;
  }>;
  accepted_operation_ids?: string[];
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
    !("max_bytes" in value) ||
    !positiveInteger(value.max_bytes) ||
    !("max_tabs" in value) ||
    !positiveInteger(value.max_tabs) ||
    !("max_writers" in value) ||
    !positiveInteger(value.max_writers) ||
    !("max_viewers" in value) ||
    !nonnegativeInteger(value.max_viewers) ||
    !("max_participants" in value) ||
    !positiveInteger(value.max_participants) ||
    !("room_lifetime_seconds" in value) ||
    !positiveInteger(value.room_lifetime_seconds) ||
    !("documents" in value) ||
    !Array.isArray(value.documents)
  ) {
    return false;
  }
  return (
    value.documents.every(
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
        typeof document.revision === "number" &&
        (!("position" in document) || typeof document.position === "number"),
    ) &&
    (!("accepted_operation_ids" in value) ||
      (Array.isArray(value.accepted_operation_ids) &&
        value.accepted_operation_ids.every(
          (operationID) => typeof operationID === "string",
        )))
  );
}

function isLiveServiceConfig(value: unknown): value is {
  max_bytes: number;
  max_document_bytes: number;
  max_tabs: number;
  max_writers: number;
  max_viewers: number;
  max_participants: number;
  room_lifetime_seconds: number;
} {
  if (typeof value !== "object" || value === null) return false;
  return (
    "max_bytes" in value &&
    positiveInteger(value.max_bytes) &&
    "max_document_bytes" in value &&
    positiveInteger(value.max_document_bytes) &&
    "max_tabs" in value &&
    positiveInteger(value.max_tabs) &&
    "max_writers" in value &&
    positiveInteger(value.max_writers) &&
    "max_viewers" in value &&
    nonnegativeInteger(value.max_viewers) &&
    "max_participants" in value &&
    positiveInteger(value.max_participants) &&
    "room_lifetime_seconds" in value &&
    positiveInteger(value.room_lifetime_seconds)
  );
}

function normalizeLiveServiceConfig(value: {
  max_bytes: number;
  max_document_bytes: number;
  max_tabs: number;
  max_writers: number;
  max_viewers: number;
  max_participants: number;
  room_lifetime_seconds: number;
}): LiveServiceConfig {
  return {
    maxBytes: value.max_bytes,
    maxDocumentBytes: value.max_document_bytes,
    maxTabs: value.max_tabs,
    maxWriters: value.max_writers,
    maxViewers: value.max_viewers,
    maxParticipants: value.max_participants,
    roomLifetimeSeconds: value.room_lifetime_seconds,
  };
}

function normalizeLiveRoomSnapshot(value: {
  slug: string;
  expires_at: string;
  password_required: boolean;
  metadata_revision: number;
  max_bytes: number;
  max_tabs: number;
  max_writers: number;
  max_viewers: number;
  max_participants: number;
  room_lifetime_seconds: number;
  documents: Array<{
    id: string;
    name: string;
    language: string;
    content: string;
    revision: number;
    position?: number;
  }>;
  accepted_operation_ids?: string[];
}): LiveRoomSnapshot {
  return {
    slug: value.slug,
    expiresAt: value.expires_at,
    passwordRequired: value.password_required,
    metadataRevision: value.metadata_revision,
    maxBytes: value.max_bytes,
    maxTabs: value.max_tabs,
    maxWriters: value.max_writers,
    maxViewers: value.max_viewers,
    maxParticipants: value.max_participants,
    roomLifetimeSeconds: value.room_lifetime_seconds,
    documents: value.documents
      .map((document) => ({ ...document }))
      .sort((left, right) => (left.position ?? 0) - (right.position ?? 0)),
    acceptedOperationIDs: [...(value.accepted_operation_ids ?? [])],
  };
}

function nonnegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function positiveInteger(value: unknown): value is number {
  return nonnegativeInteger(value) && value > 0;
}
