import { randomLiveID } from "./live-collab";

export const liveIdentityVersion = 1 as const;
export const liveIdentityStoragePrefix = "0xbin.live.identity.v1:";

export type LiveBrowserIdentity = {
  version: typeof liveIdentityVersion;
  credential: string;
  nickname?: string;
};

type IdentityStorage = Pick<Storage, "getItem" | "setItem">;

type IdentityLockManager = {
  request<T>(name: string, callback: () => T | PromiseLike<T>): Promise<T>;
};

type ResolveIdentityOptions = {
  storage?: IdentityStorage | null;
  locks?: IdentityLockManager | null;
  createCredential?: () => string;
};

export function liveIdentityStorageKey(slug: string): string {
  return `${liveIdentityStoragePrefix}${slug}`;
}

export async function resolveLiveBrowserIdentity(
  slug: string,
  options: ResolveIdentityOptions = {},
): Promise<LiveBrowserIdentity> {
  const createCredential =
    options.createCredential ?? (() => randomLiveID("browser-"));
  const fallback = (): LiveBrowserIdentity => ({
    version: liveIdentityVersion,
    credential: createCredential(),
  });
  const storage =
    "storage" in options ? options.storage : browserIdentityStorage();
  if (!storage) return fallback();

  const key = liveIdentityStorageKey(slug);
  const resolveStored = () => {
    const existing = readIdentity(storage, key);
    if (existing) return existing;
    const created = fallback();
    storage.setItem(key, JSON.stringify(created));
    return readIdentity(storage, key) ?? created;
  };

  try {
    const locks = "locks" in options ? options.locks : browserIdentityLocks();
    return locks
      ? await locks.request(`${key}:lock`, resolveStored)
      : resolveStored();
  } catch {
    return fallback();
  }
}

export function saveLiveBrowserNickname(
  slug: string,
  credential: string,
  nickname: string,
  storage: IdentityStorage | null = browserIdentityStorage(),
): void {
  if (!storage || !validNickname(nickname)) return;
  const key = liveIdentityStorageKey(slug);
  try {
    const identity = readIdentity(storage, key);
    if (!identity || identity.credential !== credential) return;
    storage.setItem(key, JSON.stringify({ ...identity, nickname }));
  } catch {
    // Storage denial is an expected privacy-mode fallback.
  }
}

function readIdentity(
  storage: IdentityStorage,
  key: string,
): LiveBrowserIdentity | undefined {
  const raw = storage.getItem(key);
  if (!raw) return undefined;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (
      value.version !== liveIdentityVersion ||
      !validOpaqueIdentifier(value.credential)
    ) {
      return undefined;
    }
    const nickname =
      value.nickname === undefined
        ? undefined
        : validNickname(value.nickname)
          ? value.nickname
          : undefined;
    return {
      version: liveIdentityVersion,
      credential: value.credential,
      ...(nickname ? { nickname } : {}),
    };
  } catch {
    return undefined;
  }
}

function validOpaqueIdentifier(value: unknown): value is string {
  return (
    typeof value === "string" &&
    value.length > 0 &&
    value.length <= 128 &&
    /^[A-Za-z0-9_-]+$/.test(value)
  );
}

function validNickname(value: unknown): value is string {
  return (
    typeof value === "string" &&
    value.length > 0 &&
    value === value.trim() &&
    new TextEncoder().encode(value).length <= 32 &&
    !/[\u0000-\u001f\u007f]/.test(value)
  );
}

function browserIdentityStorage(): IdentityStorage | null {
  try {
    return globalThis.localStorage;
  } catch {
    return null;
  }
}

function browserIdentityLocks(): IdentityLockManager | null {
  const manager = globalThis.navigator?.locks;
  if (!manager) return null;
  return {
    request: (name, callback) => manager.request(name, callback),
  };
}
