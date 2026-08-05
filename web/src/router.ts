import { hostedPageFromPath, type HostedPage } from "./hosted";

export type Route =
  | { kind: "create" }
  | { kind: "paste"; slug: string }
  | { kind: "live-create" }
  | { kind: "live-room"; slug: string }
  | { kind: "hosted"; page: HostedPage };

const slugPattern = /^[a-z]{1,128}$/;

export function resolveRoute(pathname: string, hosted = false): Route {
  const segments = pathname.split("/").filter(Boolean);
  if (segments.length === 0) {
    return { kind: "create" };
  }
  if (hosted) {
    const page = hostedPageFromPath(pathname);
    if (page) return { kind: "hosted", page };
  }
  if (segments.length === 1 && segments[0] === "live") {
    return { kind: "live-create" };
  }
  if (segments.length === 2 && segments[0] === "live") {
    return {
      kind: "live-room",
      slug: slugPattern.test(segments[1]) ? segments[1] : "",
    };
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

export function liveRoomPath(slug: string): string {
  if (!slugPattern.test(slug)) {
    throw new Error("invalid live room slug");
  }
  return `/live/${slug}`;
}
