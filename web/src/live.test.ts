import { describe, expect, it } from "vitest";
import { defaultCreateDraft } from "./create";
import {
  blankLiveDraft,
  liveDraftFromCreateDraft,
  validateLiveDraft,
} from "./live";

describe("live draft handoff", () => {
  it("maps an unsaved create draft to the first live document", () => {
    expect(
      liveDraftFromCreateDraft({
        ...defaultCreateDraft(),
        title: "  notes.go  ",
        language: "go",
        content: "package main",
      }),
    ).toEqual({
      document: {
        name: "notes.go",
        language: "go",
        content: "package main",
      },
    });
  });

  it("starts viewer-originated handoffs blank", () => {
    const draft = blankLiveDraft();
    expect(draft.document.content).toBe("");
    expect(draft.document.name).toBe("main");
    expect(draft.document.language).toBe("plaintext");
  });

  it("validates live-only name, password, and room limits", () => {
    const draft = blankLiveDraft();
    expect(validateLiveDraft(draft, true, "")).toEqual({
      password: "Password is required.",
    });
    expect(
      validateLiveDraft(
        {
          document: {
            name: ` ${"x".repeat(64)} `,
            language: "plaintext",
            content: "x".repeat(1 << 20),
          },
        },
        true,
        "x".repeat(257),
      ),
    ).toEqual({
      name: "Tab name cannot start or end with whitespace.",
      password: "Password must be at most 256 bytes.",
    });
    expect(
      validateLiveDraft(
        {
          document: {
            name: "main",
            language: "plaintext",
            content: "x".repeat(1 << 20),
          },
        },
        false,
        "ignored",
      ),
    ).toEqual({});
    expect(
      validateLiveDraft(
        {
          document: {
            name: "main",
            language: "plaintext",
            content: "x".repeat((2 << 20) + 1),
          },
        },
        false,
        "",
        2 << 20,
      ),
    ).toEqual({ content: "Live room content exceeds the 2 MiB limit." });
  });

  it("uses server-provided limits below and above the former 1 MiB default", () => {
    const draft = blankLiveDraft();
    draft.document.content = "x".repeat((512 << 10) + 1);
    expect(validateLiveDraft(draft, false, "", 512 << 10)).toEqual({
      content: "Live room content exceeds the 512 KiB limit.",
    });
    expect(validateLiveDraft(draft, false, "", 2 << 20)).toEqual({});
  });
});
