import { describe, expect, it } from "vitest";
import { findSearchMatches } from "./search";

describe("findSearchMatches", () => {
  it("returns non-overlapping case-insensitive ranges in one ordered list", () => {
    expect(findSearchMatches("Needle needle NEEDLE", "needle")).toEqual([
      { from: 0, to: 6 },
      { from: 7, to: 13 },
      { from: 14, to: 20 },
    ]);
  });

  it("does not return overlapping matches", () => {
    expect(findSearchMatches("aaaa", "aa")).toEqual([
      { from: 0, to: 2 },
      { from: 2, to: 4 },
    ]);
  });

  it("handles a dense large-log query without repeated scanning", () => {
    const content = Array.from(
      { length: 10_000 },
      (__, index) => `line ${index}: ${"x".repeat(100)} repeated log marker`,
    ).join("\n");

    const matches = findSearchMatches(content, "marker");

    expect(matches).toHaveLength(10_000);
    expect(matches[0]).toEqual({
      from: content.indexOf("marker"),
      to: content.indexOf("marker") + "marker".length,
    });
    expect(content.length).toBeGreaterThan(1_000_000);
    expect(matches.at(-1)?.to).toBeLessThanOrEqual(content.length);
  });
});
