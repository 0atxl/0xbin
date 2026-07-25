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

  it("reserves permanent policy routes only for the hosted service", () => {
    expect(resolveRoute("/about", true)).toEqual({
      kind: "hosted",
      page: "about",
    });
    expect(resolveRoute("/terms", true)).toEqual({
      kind: "hosted",
      page: "terms",
    });
    expect(resolveRoute("/privacy", true)).toEqual({
      kind: "hosted",
      page: "privacy",
    });
    expect(resolveRoute("/privacy", false)).toEqual({
      kind: "paste",
      slug: "privacy",
    });
  });

  it("keeps malformed paths in the paste unavailable boundary", () => {
    expect(resolveRoute("/not-a-slug")).toEqual({ kind: "paste", slug: "" });
    expect(pastePath("quietbrightotter")).toBe("/quietbrightotter");
    expect(() => pastePath("not-a-slug")).toThrow("invalid paste slug");
  });
});
