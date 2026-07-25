import { describe, expect, it } from "vitest";
import { hostedPageFromPath, hostedPagePaths, isHostedService } from "./hosted";

describe("hosted public routes", () => {
  it("recognizes permanent policy paths", () => {
    expect(hostedPageFromPath(hostedPagePaths.about)).toBe("about");
    expect(hostedPageFromPath(hostedPagePaths.terms)).toBe("terms");
    expect(hostedPageFromPath(hostedPagePaths.privacy)).toBe("privacy");
    expect(hostedPageFromPath("/quietbrightotter")).toBeUndefined();
  });

  it("enables hosted features only for the hosted domain or runtime marker", () => {
    expect(isHostedService("0xbin.app")).toBe(true);
    expect(isHostedService("www.0xbin.app")).toBe(true);
    expect(isHostedService("127.0.0.1", "true")).toBe(true);
    expect(isHostedService("paste.example", "false")).toBe(false);
  });
});
