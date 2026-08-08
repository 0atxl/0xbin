import { describe, expect, it } from "vitest";
import {
  classifyLiveOperationError,
  LiveOperationTracker,
} from "./live-operations";

describe("live document operation tracking", () => {
  it("replays an ambiguous failure with the stable ID exactly once per connection", () => {
    const tracker = new LiveOperationTracker();
    tracker.track({
      id: "op-stable",
      kind: "document",
      generation: 1,
      documentID: "main",
      recoveryText: "local edit",
      message: { type: "push_changes", operation_id: "op-stable" },
    });
    expect(tracker.shouldSend("op-stable", 1)).toBe(true);
    expect(tracker.markSent("op-stable", 1)).toBe(true);
    expect(tracker.shouldSend("op-stable", 1)).toBe(false);
    expect(tracker.shouldSend("op-stable", 2)).toBe(true);
    expect(tracker.markSent("op-stable", 2)).toBe(true);
    expect(tracker.settle("op-stable", 2)?.id).toBe("op-stable");
    expect(tracker.get("op-stable")).toBeUndefined();
  });

  it("keeps rejected text recoverable and ignores stale completion callbacks", () => {
    const tracker = new LiveOperationTracker();
    tracker.track({
      id: "op-current",
      kind: "document",
      generation: 3,
      documentID: "main",
      recoveryText: "preserve this text",
      message: { type: "push_changes", operation_id: "op-current" },
    });
    expect(tracker.settle("op-current", 2)).toBeUndefined();
    const rejected = tracker.reject("op-current", 3);
    expect(rejected?.recoveryText).toBe("preserve this text");
    expect(tracker.shouldSend("op-current", 4)).toBe(false);
    tracker.clear("op-current");
    expect(tracker.get("op-current")).toBeUndefined();
  });

  it("classifies every server failure path before changing UI connectivity", () => {
    expect(classifyLiveOperationError("service_unavailable")).toBe("retryable");
    expect(classifyLiveOperationError("invalid_request")).toBe("validation");
    expect(
      classifyLiveOperationError("invalid_request", "resync_required"),
    ).toBe("resync");
    expect(classifyLiveOperationError("unauthorized")).toBe("auth");
    expect(classifyLiveOperationError("room_limit_reached")).toBe("overload");
    expect(classifyLiveOperationError("room_expired", "terminal")).toBe(
      "terminal",
    );
  });
});
