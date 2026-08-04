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
import {
  buildLiveCollabFixtures,
  liveCollabFixtureSpecs,
} from "./live-collab-fixtures";

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
