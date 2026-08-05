import { describe, expect, it } from "vitest";
import { liveRoomPath, pastePath, resolveRoute } from "./router";

describe("route resolution", () => {
  it("resolves the create route and a clean paste path", () => {
    expect(resolveRoute("/")).toEqual({ kind: "create" });
    expect(resolveRoute("/quietbrightotter")).toEqual({
      kind: "paste",
      slug: "quietbrightotter",
    });
  });

  it("resolves the separate live creation and room namespace", () => {
    expect(resolveRoute("/live")).toEqual({ kind: "live-create" });
    expect(resolveRoute("/live/quietbrightotter")).toEqual({
      kind: "live-room",
      slug: "quietbrightotter",
    });
    expect(resolveRoute("/live/not-a-slug")).toEqual({
      kind: "live-room",
      slug: "",
    });
    expect(liveRoomPath("quietbrightotter")).toBe("/live/quietbrightotter");
    expect(() => liveRoomPath("not-a-slug")).toThrow("invalid live room slug");
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
