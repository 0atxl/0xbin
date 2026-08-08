import {
  ChangeSet,
  EditorState,
  Text,
  type Transaction,
} from "@codemirror/state";
import {
  getSyncedVersion,
  sendableUpdates,
  type Update,
} from "@codemirror/collab";
import type { LiveRoomDocument } from "./live-api";

const operationAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz";

export type LiveOutboundUpdate = {
  baseVersion: number;
  update: Update & { origin: Transaction };
};

export const liveQueueLimits = {
  maxUpdates: 64,
  maxBytes: 48 * 1024,
} as const;

export type LiveQueueLimits = {
  maxUpdates: number;
  maxBytes: number;
};

export type LiveQueueState = {
  updates: number;
  bytes: number;
  full: boolean;
};

/**
 * The live wire protocol submits one CodeMirror update per document at a
 * time. The authority acknowledges it before the next queued update is sent,
 * so every update has the revision it was created against. This deliberately
 * avoids folding several unconfirmed CodeMirror updates into one wire update:
 * receiveUpdates acknowledges one local update per accepted authority update.
 */
export function nextLiveOutboundUpdate(
  state: EditorState,
): LiveOutboundUpdate | undefined {
  const update = sendableUpdates(state)[0];
  return update ? { baseVersion: getSyncedVersion(state), update } : undefined;
}

// Queue limits apply to all unacknowledged CodeMirror updates in a room, not
// per tab. They are intentionally measured from the exact wire JSON.
export function liveQueueState(
  states: Iterable<EditorState>,
  limits: LiveQueueLimits = liveQueueLimits,
): LiveQueueState {
  let updates = 0;
  let bytes = 0;
  const encoder = new TextEncoder();
  for (const state of states) {
    for (const update of sendableUpdates(state)) {
      updates += 1;
      bytes += encoder.encode(
        JSON.stringify(update.changes.toJSON()),
      ).byteLength;
    }
  }
  return {
    updates,
    bytes,
    full: updates > limits.maxUpdates || bytes > limits.maxBytes,
  };
}

export function randomLiveID(prefix: string): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let value = prefix;
  for (const byte of bytes)
    value += operationAlphabet[byte % operationAlphabet.length];
  return value;
}

export function liveChangesJSON(changes: ChangeSet): unknown[] {
  return changes.toJSON() as unknown[];
}

export function applyLiveChanges(content: string, changes: ChangeSet): string {
  return changes.apply(Text.of(content.split("\n"))).toString();
}

/**
 * Produce a bounded single-range diff for reconnect snapshots. Normal live
 * edits use CodeMirror change sets; this helper is only for bridging a full
 * HTTP snapshot received while the browser has unsent local edits.
 */
export function diffLiveDocuments(before: string, after: string): ChangeSet {
  if (before === after) return ChangeSet.empty(before.length);
  let prefix = 0;
  const limit = Math.min(before.length, after.length);
  while (
    prefix < limit &&
    before.charCodeAt(prefix) === after.charCodeAt(prefix)
  ) {
    prefix += 1;
  }
  let suffix = 0;
  while (
    suffix < before.length - prefix &&
    suffix < after.length - prefix &&
    before.charCodeAt(before.length - suffix - 1) ===
      after.charCodeAt(after.length - suffix - 1)
  ) {
    suffix += 1;
  }
  return ChangeSet.of(
    {
      from: prefix,
      to: before.length - suffix,
      insert: after.slice(prefix, after.length - suffix),
    },
    before.length,
  );
}

export function normalizeLiveDocuments(
  documents: LiveRoomDocument[],
): LiveRoomDocument[] {
  return documents
    .map((document) => ({ ...document }))
    .sort((left, right) => (left.position ?? 0) - (right.position ?? 0));
}

export function livePasteExport(
  documents: LiveRoomDocument[],
  activeDocumentID: string,
  mode: "current" | "every",
): { title: string; language: string; content: string } {
  const active =
    documents.find((document) => document.id === activeDocumentID) ??
    documents[0];
  if (!active) return { title: "", language: "plaintext", content: "" };
  if (mode === "current") {
    return {
      title: active.name,
      language: active.language,
      content: active.content,
    };
  }
  return {
    title: "Live room",
    language: "plaintext",
    content: documents
      .map((document) => `--- ${document.name} ---\n\n${document.content}`)
      .join("\n\n"),
  };
}
