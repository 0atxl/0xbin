// The API's configured base URL can be an internal listener address when a
// self-hosted instance sits behind a reverse proxy. Browser-created links must
// use the public origin the visitor is actually using, while retaining the
// server-issued path and any local encryption fragment.
export function browserShareURL(
  serverURL: string,
  browserOrigin = window.location.origin,
): string {
  const destination = new URL(serverURL);
  return new URL(
    `${destination.pathname}${destination.search}${destination.hash}`,
    browserOrigin,
  ).toString();
}
