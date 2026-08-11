import { ChangeSet, EditorSelection, Text } from "@codemirror/state";

type ChangeSpec = {
  from: number;
  to?: number;
  insert?: string;
};

export type LiveCollabFixtureSpec = {
  name: string;
  document: string;
  updates: Array<{
    clientID: string;
    changes: ChangeSpec | ChangeSpec[];
  }>;
  selections: Array<{ anchor: number; head: number }>;
};

export type LiveCollabFixture = {
  name: string;
  document: string;
  updates: Array<{
    clientID: string;
    changes: unknown;
  }>;
  selections: Array<{ anchor: number; head: number }>;
  expectedDocument: string;
  mappedSelections: Array<{ anchor: number; head: number }>;
};

// These cases are deliberately generated with ChangeSet.of rather than hand
// encoding CodeMirror's JSON. The generated JSON is also replayed by Go tests.
const liveCollabFixtureSpecs: LiveCollabFixtureSpec[] = [
  {
    name: "insert-insert",
    document: "abcd",
    updates: [
      { clientID: "alice", changes: { from: 2, insert: "X" } },
      { clientID: "bob", changes: { from: 2, insert: "Y" } },
    ],
    selections: [
      { anchor: 2, head: 2 },
      { anchor: 1, head: 3 },
    ],
  },
  {
    name: "insert-delete",
    document: "abcdef",
    updates: [
      { clientID: "alice", changes: { from: 1, to: 3 } },
      { clientID: "bob", changes: { from: 2, insert: "X" } },
    ],
    selections: [
      { anchor: 2, head: 2 },
      { anchor: 1, head: 4 },
    ],
  },
  {
    name: "overlapping-replace",
    document: "0123456789",
    updates: [
      { clientID: "alice", changes: { from: 2, to: 6, insert: "AB" } },
      { clientID: "bob", changes: { from: 4, to: 8, insert: "xyz" } },
    ],
    selections: [
      { anchor: 3, head: 7 },
      { anchor: 4, head: 4 },
    ],
  },
  {
    name: "multi-range",
    document: "abcdefghij",
    updates: [
      {
        clientID: "alice",
        changes: [
          { from: 1, to: 2, insert: "X" },
          { from: 5, to: 7, insert: "YZ" },
        ],
      },
      {
        clientID: "bob",
        changes: [
          { from: 0, insert: "<" },
          { from: 8, to: 10, insert: ">" },
        ],
      },
    ],
    selections: [
      { anchor: 0, head: 10 },
      { anchor: 6, head: 6 },
    ],
  },
  {
    name: "empty-document",
    document: "",
    updates: [
      { clientID: "alice", changes: { from: 0, insert: "🙂" } },
      { clientID: "bob", changes: { from: 0, insert: "x" } },
    ],
    selections: [{ anchor: 0, head: 0 }],
  },
  {
    name: "unicode",
    document: "a🙂b漢c",
    updates: [
      { clientID: "alice", changes: { from: 3, insert: "界" } },
      { clientID: "bob", changes: { from: 1, to: 3, insert: "😀" } },
    ],
    selections: [
      { anchor: 3, head: 3 },
      { anchor: 1, head: 4 },
    ],
  },
];

export function buildLiveCollabFixtures(
  specs: LiveCollabFixtureSpec[] = liveCollabFixtureSpecs,
): LiveCollabFixture[] {
  return specs.map((fixture) => {
    const updates = fixture.updates.map((update) => ({
      clientID: update.clientID,
      changes: ChangeSet.of(update.changes, fixture.document.length).toJSON(),
    }));
    const first = ChangeSet.fromJSON(updates[0].changes);
    const second = ChangeSet.fromJSON(updates[1].changes);
    const document = Text.of(fixture.document.split("\n"));
    const expectedDocument = first
      .map(second, true)
      .apply(second.apply(document))
      .toString();
    const mappedSelections = fixture.selections.map((range) => {
      const mapped = EditorSelection.range(range.anchor, range.head).map(first);
      return { anchor: mapped.anchor, head: mapped.head };
    });
    return {
      name: fixture.name,
      document: fixture.document,
      updates,
      selections: fixture.selections,
      expectedDocument,
      mappedSelections,
    };
  });
}
