import { describe, expect, it } from "vitest";
import { pastePath, resolveRoute } from "./router";

describe("route resolution", () => {
  it("resolves the create route and a clean paste path", () => {
    expect(resolveRoute("/")).toEqual({ kind: "create" });
    expect(resolveRoute("/quietbrightotter")).toEqual({
      kind: "paste",
      slug: "quietbrightotter",
    });
  });

  it("keeps malformed paths in the paste unavailable boundary", () => {
    expect(resolveRoute("/not-a-slug")).toEqual({ kind: "paste", slug: "" });
    expect(pastePath("quietbrightotter")).toBe("/quietbrightotter");
    expect(() => pastePath("not-a-slug")).toThrow("invalid paste slug");
  });
});
