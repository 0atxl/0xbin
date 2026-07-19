import { describe, expect, it } from "vitest";
import {
  lifetimeRequest,
  maxPasteBytes,
  utf8Bytes,
  validateDraft,
  type CreateDraft,
} from "./create";

const validDraft: CreateDraft = {
  title: "",
  language: "plaintext",
  content: "hello",
  lifetime: "24h",
  encrypted: false,
};

describe("creation draft", () => {
  it("counts UTF-8 bytes and validates required content", () => {
    expect(utf8Bytes("世界")).toBe(6);
    expect(validateDraft({ ...validDraft, content: "" })).toEqual({
      content: "Paste content is required.",
    });
  });

  it("rejects paste content larger than 1 MiB", () => {
    expect(
      validateDraft({ ...validDraft, content: "x".repeat(maxPasteBytes + 1) }),
    ).toEqual({ content: "Paste content exceeds the 1 MiB limit." });
  });

  it("maps each lifetime to the settled API request values", () => {
    expect(lifetimeRequest("once")).toEqual({
      expiry: "72h",
      burnAfterRead: true,
    });
    expect(lifetimeRequest("1h")).toEqual({
      expiry: "1h",
      burnAfterRead: false,
    });
    expect(lifetimeRequest("24h")).toEqual({
      expiry: "24h",
      burnAfterRead: false,
    });
    expect(lifetimeRequest("72h")).toEqual({
      expiry: "72h",
      burnAfterRead: false,
    });
  });
});
