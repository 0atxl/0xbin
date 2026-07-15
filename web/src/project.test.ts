import { describe, expect, it } from "vitest";
import { projectName } from "./project";

describe("projectName", () => {
  it("identifies the application", () => {
    expect(projectName).toBe("0xbin");
  });
});
