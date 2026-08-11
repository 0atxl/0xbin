import { ChangeSet } from "@codemirror/state";
import { receiveUpdates } from "@codemirror/collab";
import { describe, expect, it } from "vitest";
import {
  livePresenceSelection,
  makeLiveEditorState,
  mapRemoteCursors,
  reconcileRemoteCursors,
  type RemoteCursor,
} from "./live-editor";

function cursor(anchor: number, head = anchor): RemoteCursor {
  return {
    id: "participant:connection",
    nickname: "Quiet Otter",
    color: "#8c2741",
    revision: 0,
    anchor,
    head,
    active: true,
  };
}

describe("remote cursor mapping", () => {
  it("sends pending-edit selections in authoritative revision coordinates", () => {
    const initial = makeLiveEditorState(
      {
        id: "main",
        name: "main",
        language: "plaintext",
        content: "",
        revision: 0,
        position: 0,
      },
      "client",
    );
    const withPendingText = initial.update({
      changes: { from: 0, insert: "typed" },
      selection: { anchor: 5 },
    }).state;

    expect(livePresenceSelection(withPendingText)).toEqual({
      revision: 0,
      anchor: 0,
      head: 0,
    });
  });

  it("moves a collapsed cursor after text inserted at that cursor", () => {
    const changes = ChangeSet.of([{ from: 0, insert: "typed" }], 0);

    expect(mapRemoteCursors([cursor(0)], changes, 5)[0]).toMatchObject({
      anchor: 5,
      head: 5,
    });
  });

  it("preserves forward and backward selection affinity", () => {
    const changes = ChangeSet.of([{ from: 0, insert: "new " }], 4);

    expect(mapRemoteCursors([cursor(0, 4)], changes, 8)[0]).toMatchObject({
      anchor: 0,
      head: 8,
    });
    expect(mapRemoteCursors([cursor(4, 0)], changes, 8)[0]).toMatchObject({
      anchor: 8,
      head: 0,
    });
  });

  it("maps a distant cursor once when content above it changes", () => {
    const changes = ChangeSet.of([{ from: 2, insert: "steady " }], 12);

    expect(mapRemoteCursors([cursor(10)], changes, 19)[0]).toMatchObject({
      anchor: 17,
      head: 17,
    });
  });

  it("maps authoritative cursor coordinates through pending local edits", () => {
    const initial = makeLiveEditorState(
      {
        id: "main",
        name: "main",
        language: "plaintext",
        content: "hello",
        revision: 0,
        position: 0,
      },
      "client",
    );
    const withPendingPrefix = initial.update({
      changes: { from: 0, insert: "!" },
    }).state;

    expect(
      reconcileRemoteCursors([cursor(5)], [cursor(5)], withPendingPrefix)[0],
    ).toMatchObject({ anchor: 6, head: 6, revision: 0 });
  });

  it("does not let stale presence overwrite a cursor already mapped forward", () => {
    const initial = makeLiveEditorState(
      {
        id: "main",
        name: "main",
        language: "plaintext",
        content: "hello",
        revision: 0,
        position: 0,
      },
      "client",
    );
    const changes = ChangeSet.of([{ from: 0, insert: "!" }], 5);
    const synced = receiveUpdates(initial, [
      { changes, clientID: "remote" },
    ]).state;
    const existing = { ...cursor(6), revision: 1 };

    expect(reconcileRemoteCursors([cursor(5)], [existing], synced)[0]).toEqual(
      existing,
    );
  });
});
