import { describe, expect, it } from "vitest";
import type { LiveRoomDocument } from "./live-api";
import type { LiveParticipant } from "./live-wire";
import {
  aggregateLiveRoomBytes,
  formatLiveRoomLifetime,
  liveRemoteCursors,
  nextLiveMenuItemIndex,
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
  status: "connected",
  joinedAt: "2026-08-08T10:00:00Z",
  lastSeenAt: "2026-08-08T10:00:04Z",
  currentTab: "one",
  cursor: { documentID: "one", revision: 1, anchor: 2, head: 3 },
  ...overrides,
});

describe("live room UI helpers", () => {
  it("shows aggregate UTF-8 bytes and the configured room lifetime", () => {
    expect(
      aggregateLiveRoomBytes(documents, (document) => document.content),
    ).toBe(5);
    expect(formatLiveRoomLifetime(90 * 60)).toBe("90m");
  });

  it("removes stale and off-tab remote cursors when tabs change", () => {
    expect(
      liveRemoteCursors(
        [
          participant(),
          participant({ id: "wrong-tab", currentTab: "two" }),
          participant({
            id: "stale",
            cursor: { documentID: "one", revision: 3, anchor: 0, head: 0 },
          }),
        ],
        "local",
        "one",
        1,
        Date.parse("2026-08-08T10:00:06Z"),
      ),
    ).toEqual([expect.objectContaining({ id: "other", active: true })]);
  });

  it("wraps export menu keyboard navigation and ignores unrelated keys", () => {
    expect(nextLiveMenuItemIndex(1, 2, "ArrowDown")).toBe(0);
    expect(nextLiveMenuItemIndex(0, 2, "ArrowUp")).toBe(1);
    expect(nextLiveMenuItemIndex(1, 2, "Home")).toBe(0);
    expect(nextLiveMenuItemIndex(0, 2, "End")).toBe(1);
    expect(nextLiveMenuItemIndex(0, 2, "Enter")).toBeUndefined();
  });
});
