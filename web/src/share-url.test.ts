import { describe, expect, it } from "vitest";
import { browserShareURL } from "./share-url";

describe("browserShareURL", () => {
  it("uses the browser's public origin instead of the server listener URL", () => {
    expect(
      browserShareURL(
        "http://localhost:8080/quietbrightotter",
        "https://paste.example",
      ),
    ).toBe("https://paste.example/quietbrightotter");
  });

  it("preserves an encrypted link fragment", () => {
    expect(
      browserShareURL(
        "http://localhost:8080/quietbrightotter#secret-key",
        "https://paste.example",
      ),
    ).toBe("https://paste.example/quietbrightotter#secret-key");
  });
});
