export type Route = { kind: "create" } | { kind: "paste"; slug: string };

const slugPattern = /^[a-z]{1,128}$/;

export function resolveRoute(pathname: string): Route {
  const segments = pathname.split("/").filter(Boolean);
  if (segments.length === 0) {
    return { kind: "create" };
  }
  if (segments.length === 1 && slugPattern.test(segments[0])) {
    return { kind: "paste", slug: segments[0] };
  }
  return { kind: "paste", slug: "" };
}

export function pastePath(slug: string): string {
  if (!slugPattern.test(slug)) {
    throw new Error("invalid paste slug");
  }
  return `/${slug}`;
}
