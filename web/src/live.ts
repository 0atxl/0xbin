import { utf8Bytes, type CreateDraft } from "./create";

export const defaultLiveDocumentName = "main";
export const maxLiveDocumentNameBytes = 64;
export const maxLivePasswordBytes = 256;
export const fallbackLiveRoomBytes = 1 << 20;

export type LiveDocumentDraft = {
  name: string;
  language: string;
  content: string;
};

export type LiveDraft = {
  document: LiveDocumentDraft;
};

export type LiveCreateValidation = Partial<
  Record<"name" | "password" | "content", string>
>;

export function blankLiveDraft(): LiveDraft {
  return {
    document: {
      name: defaultLiveDocumentName,
      language: "plaintext",
      content: "",
    },
  };
}

export function liveDraftFromCreateDraft(draft: CreateDraft): LiveDraft {
  return {
    document: {
      name: draft.title.trim() || defaultLiveDocumentName,
      language: draft.language,
      content: draft.content,
    },
  };
}

export function validateLiveDraft(
  draft: LiveDraft,
  requirePassword: boolean,
  password: string,
  maxRoomBytes = fallbackLiveRoomBytes,
): LiveCreateValidation {
  const errors: LiveCreateValidation = {};
  const name = draft.document.name;
  if (!name) {
    errors.name = "Tab name is required.";
  } else if (name.trim() !== name) {
    errors.name = "Tab name cannot start or end with whitespace.";
  } else if (utf8Bytes(name) > maxLiveDocumentNameBytes) {
    errors.name = `Tab name must be at most ${maxLiveDocumentNameBytes} bytes.`;
  } else if ([...name].some((character) => /\p{Cc}/u.test(character))) {
    errors.name = "Tab name cannot contain control characters.";
  }
  if (utf8Bytes(draft.document.content) > maxRoomBytes) {
    errors.content = `Live room content exceeds the ${formatBytes(maxRoomBytes)} limit.`;
  }
  if (requirePassword) {
    if (!password) {
      errors.password = "Password is required.";
    } else if (utf8Bytes(password) > maxLivePasswordBytes) {
      errors.password = `Password must be at most ${maxLivePasswordBytes} bytes.`;
    }
  }
  return errors;
}

function formatBytes(bytes: number): string {
  if (bytes % (1 << 20) === 0) return `${bytes / (1 << 20)} MiB`;
  if (bytes % (1 << 10) === 0) return `${bytes / (1 << 10)} KiB`;
  return `${bytes} bytes`;
}
