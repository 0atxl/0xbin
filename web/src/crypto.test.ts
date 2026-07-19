import { describe, expect, it } from "vitest";
import {
  DecryptionError,
  decodeBase64url,
  decryptPayload,
  encodeBase64url,
  encryptPayload,
  encryptionAlgorithm,
  encryptionVersion,
  keyFromFragmentOrURL,
  plaintextVersion,
  withKeyFragment,
  type CiphertextEnvelope,
  type PlaintextPayload,
} from "./crypto";

const fixedKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8";
const fixedEnvelope: CiphertextEnvelope = {
  version: encryptionVersion,
  algorithm: encryptionAlgorithm,
  iv: "AAECAwQFBgcICQoL",
  ciphertext:
    "PCCgfreWq3TjY626ncsMBPe64hbKWbvEroBwCT9FIt5gfsmJzqZ3uk6GD4Hp7kZMiyEUr3b0wLVR4093bMHPzJhZqha881v2JMr0jgcULWciCYGVgohq",
};

const unicodePayload: PlaintextPayload = {
  version: plaintextVersion,
  title: "世界",
  language: "plaintext",
  content: "hello",
};

describe("browser crypto", () => {
  it("round-trips base64url without padding", () => {
    const bytes = Uint8Array.from([0, 255, 254, 1]);
    expect(encodeBase64url(bytes)).toBe("AP_-AQ");
    expect(decodeBase64url("AP_-AQ")).toEqual(bytes);
    expect(() => decodeBase64url("not/base64")).toThrow(DecryptionError);
  });

  it("decrypts the fixed AES-GCM unicode compatibility vector", async () => {
    await expect(decryptPayload(fixedEnvelope, fixedKey)).resolves.toEqual(
      unicodePayload,
    );
  });

  it("encrypts with a 256-bit key and unique 96-bit IV", async () => {
    const encrypted = await encryptPayload(unicodePayload);
    const second = await encryptPayload(unicodePayload);
    expect(decodeBase64url(encrypted.key)).toHaveLength(32);
    expect(decodeBase64url(encrypted.envelope.iv)).toHaveLength(12);
    expect(second.key).not.toBe(encrypted.key);
    expect(second.envelope.iv).not.toBe(encrypted.envelope.iv);
    await expect(
      decryptPayload(encrypted.envelope, encrypted.key),
    ).resolves.toEqual(unicodePayload);
  });

  it("rejects wrong keys, modified ciphertext, and unsupported versions", async () => {
    const wrongKey = fixedKey.slice(0, -1) + "A";
    await expect(
      decryptPayload(fixedEnvelope, wrongKey),
    ).rejects.toBeInstanceOf(DecryptionError);
    await expect(
      decryptPayload(
        {
          ...fixedEnvelope,
          ciphertext: fixedEnvelope.ciphertext.slice(0, -1) + "A",
        },
        fixedKey,
      ),
    ).rejects.toBeInstanceOf(DecryptionError);
    await expect(
      decryptPayload(
        { ...fixedEnvelope, version: 2 } as unknown as CiphertextEnvelope,
        fixedKey,
      ),
    ).rejects.toBeInstanceOf(DecryptionError);
  });

  it("requires exactly 32 key bytes and accepts raw keys or complete URLs", () => {
    expect(keyFromFragmentOrURL(fixedKey)).toBe(fixedKey);
    expect(
      keyFromFragmentOrURL(`https://0xbin.app/quietbrightotter#${fixedKey}`),
    ).toBe(fixedKey);
    expect(() => keyFromFragmentOrURL("AAECAw")).toThrow(DecryptionError);
  });

  it.each(["", "#", "not/base64", "https://%", "https://0xbin.app/paste"])(
    "reports malformed key input generically: %s",
    (input) => {
      expect(() => keyFromFragmentOrURL(input)).toThrow(DecryptionError);
    },
  );

  it.each([
    {
      name: "unsupported algorithm",
      envelope: { ...fixedEnvelope, algorithm: "other" },
    },
    { name: "malformed IV", envelope: { ...fixedEnvelope, iv: "not/base64" } },
    { name: "short IV", envelope: { ...fixedEnvelope, iv: "AAECAw" } },
    {
      name: "malformed ciphertext",
      envelope: { ...fixedEnvelope, ciphertext: "not/base64" },
    },
    {
      name: "truncated tag",
      envelope: { ...fixedEnvelope, ciphertext: "AAECAw" },
    },
  ])("reports $name generically", async ({ envelope }) => {
    await expect(
      decryptPayload(envelope as CiphertextEnvelope, fixedKey),
    ).rejects.toBeInstanceOf(DecryptionError);
  });

  it.each([
    { name: "non-JSON plaintext", plaintext: "not JSON" },
    {
      name: "unsupported plaintext version",
      plaintext: JSON.stringify({ ...unicodePayload, version: 2 }),
    },
    {
      name: "invalid plaintext shape",
      plaintext: JSON.stringify({ ...unicodePayload, content: "" }),
    },
  ])("reports authenticated $name generically", async ({ plaintext }) => {
    const envelope = await encryptTestPlaintext(plaintext);
    await expect(decryptPayload(envelope, fixedKey)).rejects.toBeInstanceOf(
      DecryptionError,
    );
  });

  it("keeps the key out of the constructed HTTP request", () => {
    const shareURL = withKeyFragment(
      "https://0xbin.app/quietbrightotter",
      fixedKey,
    );
    const destination = new URL(shareURL);
    const requestTarget = destination.pathname + destination.search;
    expect(shareURL).toContain(`#${fixedKey}`);
    expect(requestTarget).not.toContain(fixedKey);
    expect(destination.hash).toBe(`#${fixedKey}`);
  });
});

async function encryptTestPlaintext(
  plaintext: string,
): Promise<CiphertextEnvelope> {
  const rawKey = decodeBase64url(fixedKey);
  const iv = decodeBase64url(fixedEnvelope.iv);
  const key = await crypto.subtle.importKey(
    "raw",
    rawKey as BufferSource,
    { name: "AES-GCM" },
    false,
    ["encrypt"],
  );
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt(
      { name: "AES-GCM", iv: iv as BufferSource },
      key,
      new TextEncoder().encode(plaintext),
    ),
  );
  return {
    version: encryptionVersion,
    algorithm: encryptionAlgorithm,
    iv: fixedEnvelope.iv,
    ciphertext: encodeBase64url(ciphertext),
  };
}
