import http from "node:http";

const key = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8";
const server = http.createServer((request, response) => {
  const serializedHeaders = JSON.stringify(request.headers);
  if (request.url.includes(key) || serializedHeaders.includes(key)) {
    response.statusCode = 500;
    response.end("fragment leaked into HTTP request");
    return;
  }
  response.end("ok");
});

await new Promise((resolve, reject) => {
  server.once("error", reject);
  server.listen(0, "127.0.0.1", resolve);
});

try {
  const address = server.address();
  if (address === null || typeof address === "string") {
    throw new Error("test server did not expose a TCP address");
  }
  const response = await fetch(
    `http://127.0.0.1:${address.port}/quietbrightotter#${key}`,
  );
  if (!response.ok) {
    throw new Error("fragment leaked into HTTP request");
  }
} finally {
  await new Promise((resolve, reject) =>
    server.close((error) => (error ? reject(error) : resolve())),
  );
}
