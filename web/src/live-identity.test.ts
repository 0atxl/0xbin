import { describe, expect, it } from "vitest";
import {
  liveIdentityStorageKey,
  resolveLiveBrowserIdentity,
  saveLiveBrowserNickname,
} from "./live-identity";

function memoryStorage(initial: Record<string, string> = {}) {
  const values = new Map(Object.entries(initial));
  return {
    values,
    storage: {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
    },
  };
}

function exclusiveLocks() {
  let tail = Promise.resolve();
  return {
    request<T>(_name: string, callback: () => T | PromiseLike<T>): Promise<T> {
      const result = tail.then(callback);
      tail = result.then(
        () => undefined,
        () => undefined,
      );
      return result;
    },
  };
}

describe("live browser identity", () => {
  it("serializes concurrent first-use tabs onto one room credential", async () => {
    const { storage, values } = memoryStorage();
    const locks = exclusiveLocks();
    let next = 0;
    const identities = await Promise.all([
      resolveLiveBrowserIdentity("calmbrightotter", {
        storage,
        locks,
        createCredential: () => `browser-${++next}`,
      }),
      resolveLiveBrowserIdentity("calmbrightotter", {
        storage,
        locks,
        createCredential: () => `browser-${++next}`,
      }),
    ]);

    expect(identities[0]).toEqual(identities[1]);
    expect(values.size).toBe(1);
  });

  it("scopes identity and authoritative nickname to one room", async () => {
    const { storage, values } = memoryStorage();
    const first = await resolveLiveBrowserIdentity("calmbrightotter", {
      storage,
      locks: null,
      createCredential: () => "browser-first",
    });
    const second = await resolveLiveBrowserIdentity("quietamberfox", {
      storage,
      locks: null,
      createCredential: () => "browser-second",
    });
    saveLiveBrowserNickname(
      "calmbrightotter",
      first.credential,
      "Quiet Otter",
      storage,
    );

    expect(first.credential).not.toBe(second.credential);
    expect(
      JSON.parse(values.get(liveIdentityStorageKey("calmbrightotter"))!),
    ).toEqual({
      version: 1,
      credential: "browser-first",
      nickname: "Quiet Otter",
    });
    expect(JSON.stringify([...values])).not.toMatch(
      /password|cookie|creator|token/i,
    );
  });

  it("repairs corrupt records and silently falls back when storage is denied", async () => {
    const key = liveIdentityStorageKey("calmbrightotter");
    const { storage } = memoryStorage({ [key]: "not-json" });
    await expect(
      resolveLiveBrowserIdentity("calmbrightotter", {
        storage,
        locks: null,
        createCredential: () => "browser-repaired",
      }),
    ).resolves.toMatchObject({ credential: "browser-repaired" });

    const denied = {
      getItem: () => {
        throw new DOMException("denied", "SecurityError");
      },
      setItem: () => {
        throw new DOMException("denied", "SecurityError");
      },
    };
    await expect(
      resolveLiveBrowserIdentity("calmbrightotter", {
        storage: denied,
        locks: null,
        createCredential: () => "browser-memory",
      }),
    ).resolves.toEqual({ version: 1, credential: "browser-memory" });
  });
});
