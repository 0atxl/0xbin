export const livePanelLayouts = [
  { id: "columns", label: "Two columns", count: 2 },
  { id: "rows", label: "Two rows", count: 2 },
  { id: "three", label: "Three columns", count: 3 },
  { id: "grid", label: "Four-pane grid", count: 4 },
] as const;
export type LivePanelLayout = (typeof livePanelLayouts)[number]["id"];
export type LiveLayoutPreset = LivePanelLayout | "single";

export function isLiveLayoutPreset(value: unknown): value is LiveLayoutPreset {
  return (
    value === "single" || livePanelLayouts.some((item) => item.id === value)
  );
}

type LayoutPreference = { followCreator: boolean; preset: LiveLayoutPreset };
const storageKey = (slug: string) => `0xbin.live.layout.v1:${slug}`;

export function readLiveLayoutPreference(
  slug: string,
  expiresAt: string,
): LayoutPreference {
  try {
    const value: unknown = JSON.parse(
      localStorage.getItem(storageKey(slug)) ?? "null",
    );
    if (
      value &&
      typeof value === "object" &&
      "expiresAt" in value &&
      value.expiresAt === expiresAt &&
      Date.parse(expiresAt) > Date.now() &&
      "followCreator" in value &&
      typeof value.followCreator === "boolean" &&
      "preset" in value &&
      isLiveLayoutPreset(value.preset)
    ) {
      return { followCreator: value.followCreator, preset: value.preset };
    }
  } catch {
    /* Storage can be unavailable in private browser contexts. */
  }
  return { followCreator: true, preset: "single" };
}

export function saveLiveLayoutPreference(
  slug: string,
  expiresAt: string,
  preference: LayoutPreference,
) {
  try {
    localStorage.setItem(
      storageKey(slug),
      JSON.stringify({ ...preference, expiresAt }),
    );
  } catch {
    /* The workspace remains usable without persistent browser storage. */
  }
}
