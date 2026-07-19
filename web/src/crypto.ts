export const plaintextVersion = 1;
export const encryptionVersion = 1;
export const encryptionAlgorithm = "A256GCM";

const keyBytes = 32;
const ivBytes = 12;
const ciphertextTagBytes = 16;

export type PlaintextPayload = {
  version: typeof plaintextVersion;
  title: string;
  language: string;
  content: string;
};

export type CiphertextEnvelope = {
  version: typeof encryptionVersion;
  algorithm: typeof encryptionAlgorithm;
  iv: string;
  ciphertext: string;
};

export type EncryptedPaste = {
  envelope: CiphertextEnvelope;
  key: string;
};

export class DecryptionError extends Error {
  constructor() {
    super("Unable to decrypt this paste");
    this.name = "DecryptionError";
  }
}

export function encodeBase64url(bytes: Uint8Array): string {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
  }
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
}

export function decodeBase64url(value: string): Uint8Array {
  if (!/^[A-Za-z0-9_-]*$/.test(value) || value.length % 4 === 1) {
    throw new DecryptionError();
  }
  try {
    const base64 = value
      .replaceAll("-", "+")
      .replaceAll("_", "/")
      .padEnd(Math.ceil(value.length / 4) * 4, "=");
    const binary = atob(base64);
    return Uint8Array.from(binary, (character) => character.charCodeAt(0));
  } catch {
    throw new DecryptionError();
  }
}

export async function encryptPayload(
  payload: PlaintextPayload,
): Promise<EncryptedPaste> {
  assertPlaintextPayload(payload);
  const key = await crypto.subtle.generateKey(
    { name: "AES-GCM", length: 256 },
    true,
    ["encrypt", "decrypt"],
  );
  const iv = crypto.getRandomValues(new Uint8Array(ivBytes));
  const plaintext = new TextEncoder().encode(JSON.stringify(payload));
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, plaintext),
  );
  const rawKey = new Uint8Array(await crypto.subtle.exportKey("raw", key));
  return {
    envelope: {
      version: encryptionVersion,
      algorithm: encryptionAlgorithm,
      iv: encodeBase64url(iv),
      ciphertext: encodeBase64url(ciphertext),
    },
    key: encodeBase64url(rawKey),
  };
}

export async function decryptPayload(
  envelope: CiphertextEnvelope,
  encodedKey: string,
): Promise<PlaintextPayload> {
  try {
    assertEnvelope(envelope);
    const rawKey = decodeBase64url(encodedKey);
    if (rawKey.length !== keyBytes) {
      throw new DecryptionError();
    }
    const key = await crypto.subtle.importKey(
      "raw",
      rawKey as BufferSource,
      { name: "AES-GCM" },
      false,
      ["decrypt"],
    );
    const plaintext = await crypto.subtle.decrypt(
      { name: "AES-GCM", iv: decodeBase64url(envelope.iv) as BufferSource },
      key,
      decodeBase64url(envelope.ciphertext) as BufferSource,
    );
    const payload: unknown = JSON.parse(new TextDecoder().decode(plaintext));
    assertPlaintextPayload(payload);
    return payload;
  } catch {
    throw new DecryptionError();
  }
}

// keyFromFragmentOrURL accepts a raw fragment key or a complete sharing URL;
// callers keep its return value in page memory and never send it to an API.
export function keyFromFragmentOrURL(input: string): string {
  try {
    const value = input.trim();
    const key = value.includes("://")
      ? new URL(value).hash.slice(1)
      : value.startsWith("#")
        ? value.slice(1)
        : value;
    const decoded = decodeBase64url(key);
    if (decoded.length !== keyBytes) {
      throw new DecryptionError();
    }
    return key;
  } catch {
    throw new DecryptionError();
  }
}

// withKeyFragment creates a share URL locally. URL fragments are not part of
// HTTP request targets, bodies, or headers.
export function withKeyFragment(url: string, encodedKey: string): string {
  const key = keyFromFragmentOrURL(encodedKey);
  const shareURL = new URL(url);
  shareURL.hash = key;
  return shareURL.toString();
}

function assertEnvelope(value: unknown): asserts value is CiphertextEnvelope {
  if (
    typeof value !== "object" ||
    value === null ||
    (value as CiphertextEnvelope).version !== encryptionVersion ||
    (value as CiphertextEnvelope).algorithm !== encryptionAlgorithm ||
    typeof (value as CiphertextEnvelope).iv !== "string" ||
    typeof (value as CiphertextEnvelope).ciphertext !== "string"
  ) {
    throw new DecryptionError();
  }
  const envelope = value as CiphertextEnvelope;
  if (
    decodeBase64url(envelope.iv).length !== ivBytes ||
    decodeBase64url(envelope.ciphertext).length < ciphertextTagBytes
  ) {
    throw new DecryptionError();
  }
}

function assertPlaintextPayload(
  value: unknown,
): asserts value is PlaintextPayload {
  if (
    typeof value !== "object" ||
    value === null ||
    (value as PlaintextPayload).version !== plaintextVersion ||
    typeof (value as PlaintextPayload).title !== "string" ||
    typeof (value as PlaintextPayload).language !== "string" ||
    typeof (value as PlaintextPayload).content !== "string" ||
    (value as PlaintextPayload).content === ""
  ) {
    throw new DecryptionError();
  }
}
