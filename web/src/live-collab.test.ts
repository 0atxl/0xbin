import {
  ChangeSet,
  EditorSelection,
  EditorState,
  Text,
} from "@codemirror/state";
import {
  collab,
  getSyncedVersion,
  receiveUpdates,
  rebaseUpdates,
  sendableUpdates,
} from "@codemirror/collab";
import { describe, expect, it } from "vitest";
import type { LiveRoomDocument } from "./live-api";
import { buildLiveCollabFixtures } from "./live-collab-fixtures";
import {
  applyLiveChanges,
  diffLiveDocuments,
  livePasteExport,
  nextLiveOutboundUpdate,
  normalizeLiveDocuments,
  liveQueueState,
} from "./live-collab";

function document(
  id: string,
  position: number,
  content = id,
): LiveRoomDocument {
  return {
    id,
    name: id,
    language: "plaintext",
    content,
    revision: 0,
    position,
  };
}

describe("live room client helpers", () => {
  it("bridges reconnect snapshots without changing equal documents", () => {
    expect(diffLiveDocuments("hello", "hello").empty).toBe(true);
    const changes = diffLiveDocuments("hello world", "hello brave world");
    expect(applyLiveChanges("hello world", changes)).toBe("hello brave world");
  });

  it("keeps room document order independent of response arrival order", () => {
    expect(
      normalizeLiveDocuments([document("b", 1), document("a", 0)]),
    ).toEqual([document("a", 0), document("b", 1)]);
  });

  it("exports the current tab or clearly separated tabs", () => {
    const documents = [document("main", 0, "one"), document("notes", 1, "two")];
    expect(livePasteExport(documents, "main", "current")).toEqual({
      title: "main",
      language: "plaintext",
      content: "one",
    });
    expect(livePasteExport(documents, "main", "every").content).toBe(
      "--- main ---\n\none\n\n--- notes ---\n\ntwo",
    );
  });

  it("accepts the same CodeMirror JSON shape used on the wire", () => {
    const changes = ChangeSet.of({ from: 1, to: 2, insert: "x" }, 3);
    expect(applyLiveChanges("abc", ChangeSet.fromJSON(changes.toJSON()))).toBe(
      "axc",
    );
  });

  it("submits rapid local transactions exactly once and in revision order", () => {
    const clientID = "rapid-client";
    let local = EditorState.create({
      doc: "four",
      extensions: [collab({ clientID })],
    });
    local = local.update({ changes: { from: 4, insert: " α" } }).state;
    local = local.update({
      changes: { from: 0, to: 4, insert: "five" },
    }).state;
    local = local.update({
      changes: { from: 5, to: 6, insert: "🙂" },
    }).state;

    expect(sendableUpdates(local)).toHaveLength(3);
    const localText = local.doc.toString();
    let authoritativeText = "four";
    const submitted = new Set<string>();

    for (let revision = 0; revision < 3; revision += 1) {
      const pending = nextLiveOutboundUpdate(local);
      expect(pending?.baseVersion).toBe(revision);
      expect(pending).toBeDefined();
      const wireChanges = pending!.update.changes.toJSON();
      const operationID = `rapid-${revision}`;
      expect(submitted.has(operationID)).toBe(false);
      submitted.add(operationID);
      authoritativeText = applyLiveChanges(
        authoritativeText,
        ChangeSet.fromJSON(wireChanges),
      );
      local = local.update(
        receiveUpdates(local, [{ changes: pending!.update.changes, clientID }]),
      ).state;
    }

    expect(submitted.size).toBe(3);
    expect(sendableUpdates(local)).toHaveLength(0);
    expect(authoritativeText).toBe(localText);
    expect(authoritativeText).toBe("five 🙂");
  });

  it("uses the authoritative snapshot revision for the next local edit", () => {
    const clientID = "snapshot-client";
    let local = EditorState.create({
      doc: "one",
      extensions: [collab({ clientID })],
    });
    const serverChange = ChangeSet.of({ from: 0, to: 3, insert: "two" }, 3);
    local = local.update(
      receiveUpdates(local, [{ changes: serverChange, clientID: "remote" }]),
    ).state;
    local = local.update({ changes: { from: 3, insert: "!" } }).state;

    expect(getSyncedVersion(local)).toBe(1);
    expect(nextLiveOutboundUpdate(local)?.baseVersion).toBe(1);
  });

  it("bounds queued edits across document tabs by update count and wire bytes", () => {
    const first = EditorState.create({
      doc: "",
      extensions: [collab({ clientID: "first" })],
    }).update({ changes: { from: 0, insert: "first" } }).state;
    const second = EditorState.create({
      doc: "",
      extensions: [collab({ clientID: "second" })],
    }).update({ changes: { from: 0, insert: "second" } }).state;
    expect(
      liveQueueState([first, second], { maxUpdates: 1, maxBytes: 1000 }),
    ).toMatchObject({
      updates: 2,
      full: true,
    });
    expect(liveQueueState([first], { maxUpdates: 8, maxBytes: 4 }).full).toBe(
      true,
    );
  });
});

const fixtures = buildLiveCollabFixtures();

describe("CodeMirror live collaboration compatibility fixtures", () => {
  it("serializes and rehydrates every fixture through ChangeSet JSON", () => {
    for (const fixture of fixtures) {
      for (const update of fixture.updates) {
        expect(ChangeSet.fromJSON(update.changes).toJSON()).toEqual(
          update.changes,
        );
      }
    }
  });

  it("converges concurrent updates using CodeMirror mapping", () => {
    for (const fixture of fixtures) {
      const first = ChangeSet.fromJSON(fixture.updates[0].changes);
      const second = ChangeSet.fromJSON(fixture.updates[1].changes);
      const document = Text.of(fixture.document.split("\n"));
      const firstThenSecond = first
        .map(second, true)
        .apply(second.apply(document))
        .toString();
      const secondThenFirst = second
        .map(first)
        .apply(first.apply(document))
        .toString();
      expect(firstThenSecond).toBe(secondThenFirst);
      expect(firstThenSecond).toBe(fixture.expectedDocument);
    }
  });

  it("rebases stale updates and filters already accepted client updates", () => {
    for (const fixture of fixtures) {
      const first = ChangeSet.fromJSON(fixture.updates[0].changes);
      const second = ChangeSet.fromJSON(fixture.updates[1].changes);
      const rebased = rebaseUpdates(
        [{ changes: second, clientID: fixture.updates[1].clientID }],
        [{ changes: first.desc, clientID: fixture.updates[0].clientID }],
      );
      expect(rebased).toHaveLength(1);
      expect(rebased[0].changes.toJSON()).toEqual(second.map(first).toJSON());

      const filtered = rebaseUpdates(
        [{ changes: first, clientID: fixture.updates[0].clientID }],
        [{ changes: first.desc, clientID: fixture.updates[0].clientID }],
      );
      expect(filtered).toHaveLength(0);
    }
  });

  it("maps cursor and selection ranges through accepted changes", () => {
    for (const fixture of fixtures) {
      const first = ChangeSet.fromJSON(fixture.updates[0].changes);
      const selection = EditorSelection.create(
        fixture.selections.map((range) =>
          EditorSelection.range(range.anchor, range.head),
        ),
      );
      const mapped = selection.map(first);
      expect(mapped.ranges.length).toBeGreaterThan(0);
      expect(mapped.mainIndex).toBe(0);
      expect(
        fixture.selections.map((range) => {
          const mappedRange = EditorSelection.range(
            range.anchor,
            range.head,
          ).map(first);
          return { anchor: mappedRange.anchor, head: mappedRange.head };
        }),
      ).toEqual(fixture.mappedSelections);
    }
  });

  it("exposes the collab client/version/update contract", () => {
    const state = EditorState.create({
      doc: "hello",
      extensions: [collab({ clientID: "fixture-client" })],
    });
    const changed = state.update({ changes: { from: 5, insert: "!" } }).state;
    const updates = sendableUpdates(changed);
    expect(getSyncedVersion(changed)).toBe(0);
    expect(updates).toHaveLength(1);
    expect(updates[0].clientID).toBe("fixture-client");
    expect(updates[0].changes.toJSON()).toEqual([5, [0, "!"]]);

    const acknowledged = changed.update(
      receiveUpdates(changed, [
        { changes: updates[0].changes, clientID: "fixture-client" },
      ]),
    ).state;
    expect(getSyncedVersion(acknowledged)).toBe(1);
    expect(sendableUpdates(acknowledged)).toHaveLength(0);

    const remote = ChangeSet.of({ from: 0, insert: "!" }, 5);
    const remoteState = changed.update(
      receiveUpdates(changed, [{ changes: remote, clientID: "remote" }]),
    ).state;
    expect(remoteState.doc.toString()).toBe("!hello!");
    expect(getSyncedVersion(remoteState)).toBe(1);
    expect(sendableUpdates(remoteState)[0].changes.toJSON()).toEqual([
      6,
      [0, "!"],
    ]);
  });
});
