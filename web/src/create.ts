export const maxPasteBytes = 1 << 20;
export const maxTitleBytes = 200;
export const maxLanguageBytes = 64;

export type Lifetime = "once" | "1h" | "24h" | "72h";

export type CreateDraft = {
  title: string;
  language: string;
  content: string;
  lifetime: Lifetime;
  encrypted: boolean;
};

export type CreateValidation = Partial<
  Record<"title" | "language" | "content", string>
>;

export function utf8Bytes(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

export function validateDraft(draft: CreateDraft): CreateValidation {
  const errors: CreateValidation = {};
  if (utf8Bytes(draft.title) > maxTitleBytes) {
    errors.title = `Title must be at most ${maxTitleBytes} bytes.`;
  }
  if (utf8Bytes(draft.language) > maxLanguageBytes) {
    errors.language = `Language must be at most ${maxLanguageBytes} bytes.`;
  }
  if (!draft.content) {
    errors.content = "Paste content is required.";
  } else if (utf8Bytes(draft.content) > maxPasteBytes) {
    errors.content = "Paste content exceeds the 1 MiB limit.";
  }
  return errors;
}

export function lifetimeRequest(lifetime: Lifetime): {
  expiry: "1h" | "24h" | "72h";
  burnAfterRead: boolean;
} {
  switch (lifetime) {
    case "once":
      return { expiry: "72h", burnAfterRead: true };
    case "1h":
      return { expiry: "1h", burnAfterRead: false };
    case "24h":
      return { expiry: "24h", burnAfterRead: false };
    case "72h":
      return { expiry: "72h", burnAfterRead: false };
  }
}
