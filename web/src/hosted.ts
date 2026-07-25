export const hostedPagePaths = {
  about: "/about",
  privacy: "/privacy",
  terms: "/terms",
} as const;

export type HostedPage = keyof typeof hostedPagePaths;

const hostedPagesByPath = new Map<string, HostedPage>(
  Object.entries(hostedPagePaths).map(([page, path]) => [
    path,
    page as HostedPage,
  ]),
);

export function hostedPageFromPath(pathname: string): HostedPage | undefined {
  return hostedPagesByPath.get(pathname);
}

export function isHostedService(
  hostname: string,
  runtimeMarker?: string,
): boolean {
  return (
    runtimeMarker === "true" ||
    hostname === "0xbin.app" ||
    hostname === "www.0xbin.app"
  );
}
