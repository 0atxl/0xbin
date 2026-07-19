import { describe, expect, it } from "vitest";
import { loadTheme, saveTheme, selectTheme, themeStorageKey } from "./theme";

describe("theme preference", () => {
  it("uses a saved preference before the system preference", () => {
    expect(selectTheme("light", true)).toBe("light");
    expect(selectTheme(null, true)).toBe("dark");
  });

  it("persists only the theme value", () => {
    const values = new Map<string, string>();
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
    };
    saveTheme(storage, "dark");
    expect(values).toEqual(new Map([[themeStorageKey, "dark"]]));
    expect(loadTheme(storage, false)).toBe("dark");
  });
});
