export type Theme = "light" | "dark";

export const themeStorageKey = "0xbin-theme";

export type ThemeStorage = Pick<Storage, "getItem" | "setItem">;

export function selectTheme(
  storedTheme: string | null,
  prefersDark: boolean,
): Theme {
  if (storedTheme === "light" || storedTheme === "dark") {
    return storedTheme;
  }
  return prefersDark ? "dark" : "light";
}

export function loadTheme(storage: ThemeStorage, prefersDark: boolean): Theme {
  return selectTheme(storage.getItem(themeStorageKey), prefersDark);
}

export function saveTheme(storage: ThemeStorage, theme: Theme): void {
  storage.setItem(themeStorageKey, theme);
}
