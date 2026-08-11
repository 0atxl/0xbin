import { describe, expect, it } from "vitest";
import type { LiveRoomDocument } from "./live-api";
import type { LiveParticipant } from "./live-wire";
import {
  aggregateLiveRoomBytes,
  formatLiveRoomLifetime,
  liveRemoteCursors,
  nextLiveTabName,
  nextLiveMenuItemIndex,
  reorderLiveTabIDs,
} from "./live-room-ui";

const documents: LiveRoomDocument[] = [
  {
    id: "one",
    name: "one",
    language: "text",
    content: "x",
    revision: 1,
    position: 0,
  },
  {
    id: "two",
    name: "two",
    language: "text",
    content: "🙂",
    revision: 2,
    position: 1,
  },
];

const participant = (
  overrides: Partial<LiveParticipant> = {},
): LiveParticipant => ({
  id: "other",
  nickname: "Other",
  color: "#112233",
  role: "writer",
  accessClass: "collaborator",
  canEdit: true,
  connectionCount: 1,
  status: "connected",
  joinedAt: "2026-08-08T10:00:00Z",
  lastSeenAt: "2026-08-08T10:00:04Z",
  currentTab: "one",
  cursor: { documentID: "one", revision: 1, anchor: 2, head: 3 },
  cursors: [
    {
      connectionID: "connection-other",
      documentID: "one",
      revision: 1,
      anchor: 2,
      head: 3,
    },
  ],
  ...overrides,
});

describe("live room UI helpers", () => {
  it("shows aggregate UTF-8 bytes and the configured room lifetime", () => {
    expect(
      aggregateLiveRoomBytes(documents, (document) => document.content),
    ).toBe(5);
    expect(formatLiveRoomLifetime(90 * 60)).toBe("90m");
  });

  it("renders connection-specific cursors with their source revisions", () => {
    expect(
      liveRemoteCursors(
        [
          participant(),
          participant({
            id: "multi-tab",
            connectionCount: 2,
            currentTab: "two",
            cursors: [
              {
                connectionID: "tab-one",
                documentID: "one",
                revision: 1,
                anchor: 4,
                head: 4,
              },
              {
                connectionID: "tab-two",
                documentID: "two",
                revision: 2,
                anchor: 0,
                head: 0,
              },
            ],
          }),
          participant({
            id: "stale",
            cursors: [
              {
                connectionID: "stale-tab",
                documentID: "one",
                revision: 3,
                anchor: 0,
                head: 0,
              },
            ],
          }),
        ],
        "local",
        "one",
        Date.parse("2026-08-08T10:00:06Z"),
      ),
    ).toEqual([
      expect.objectContaining({ id: "other:connection-other", active: true }),
      expect.objectContaining({ id: "multi-tab:tab-one", active: true }),
      expect.objectContaining({ id: "stale:stale-tab", revision: 3 }),
    ]);
  });

  it("wraps export menu keyboard navigation and ignores unrelated keys", () => {
    expect(nextLiveMenuItemIndex(1, 2, "ArrowDown")).toBe(0);
    expect(nextLiveMenuItemIndex(0, 2, "ArrowUp")).toBe(1);
    expect(nextLiveMenuItemIndex(1, 2, "Home")).toBe(0);
    expect(nextLiveMenuItemIndex(0, 2, "End")).toBe(1);
    expect(nextLiveMenuItemIndex(0, 2, "Enter")).toBeUndefined();
  });

  it("keeps generated names sequential after an earlier tab is deleted", () => {
    expect(
      nextLiveTabName([{ name: "tab1" }, { name: "tab2" }, { name: "tab3" }]),
    ).toBe("tab4");
    expect(nextLiveTabName([{ name: "tab2" }, { name: "tab3" }])).toBe("tab4");
    expect(nextLiveTabName([{ name: "main" }])).toBe("tab2");
    expect(nextLiveTabName([{ name: "tab-2" }, { name: "notes" }])).toBe(
      "tab3",
    );
  });

  it("moves a dragged tab before or after an authoritative target", () => {
    const order = ["one", "two", "three"];
    expect(reorderLiveTabIDs(order, "one", "three", "after")).toEqual([
      "two",
      "three",
      "one",
    ]);
    expect(reorderLiveTabIDs(order, "three", "one", "before")).toEqual([
      "three",
      "one",
      "two",
    ]);
    expect(reorderLiveTabIDs(order, "two", "two", "before")).toEqual(order);
    expect(reorderLiveTabIDs(order, "missing", "one", "before")).toEqual(order);
  });
});
