import { describe, expect, it } from "vitest";
import {
  captureLiveRevisionAuthority,
  isAuthorityEvent,
  isCurrentLiveSnapshot,
  snapshotCanReconcile,
} from "./live-reconciliation";

describe("live HTTP reconciliation authority", () => {
  it("rejects an HTTP snapshot that would move a document or metadata backward", () => {
    const current = captureLiveRevisionAuthority(3, [
      { id: "main", revision: 5 },
      { id: "notes", revision: 2 },
    ]);
    expect(
      snapshotCanReconcile(
        current,
        captureLiveRevisionAuthority(2, [
          { id: "main", revision: 5 },
          { id: "notes", revision: 2 },
        ]),
      ),
    ).toBe(false);
    expect(
      snapshotCanReconcile(
        current,
        captureLiveRevisionAuthority(3, [
          { id: "main", revision: 4 },
          { id: "notes", revision: 2 },
        ]),
      ),
    ).toBe(false);
  });

  it("allows deletion only with a later metadata revision", () => {
    const current = captureLiveRevisionAuthority(4, [
      { id: "main", revision: 2 },
      { id: "notes", revision: 1 },
    ]);
    expect(
      snapshotCanReconcile(
        current,
        captureLiveRevisionAuthority(4, [{ id: "main", revision: 2 }]),
      ),
    ).toBe(false);
    expect(
      snapshotCanReconcile(
        current,
        captureLiveRevisionAuthority(5, [{ id: "main", revision: 2 }]),
      ),
    ).toBe(true);
  });

  it("marks only durable document and metadata messages for buffering", () => {
    expect(isAuthorityEvent("changes")).toBe(true);
    expect(isAuthorityEvent("document_deleted")).toBe(true);
    expect(isAuthorityEvent("presence_updated")).toBe(false);
    expect(isAuthorityEvent("joined")).toBe(false);
  });

  it("ignores an overlapping response once a newer request owns authority", () => {
    expect(isCurrentLiveSnapshot(1, 2)).toBe(false);
    expect(isCurrentLiveSnapshot(2, 2)).toBe(true);
  });
});
