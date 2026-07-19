import http from "node:http";

const key = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8";
const plaintext = "server must not receive this plaintext";
const requests = [];
const server = http.createServer((request, response) => {
  const chunks = [];
  request.on("data", (chunk) => chunks.push(chunk));
  request.on("end", () => {
    const observed = {
      method: request.method,
      url: request.url,
      headers: request.headers,
      body: Buffer.concat(chunks).toString("utf8"),
    };
    requests.push(observed);
    const serverVisibleData = JSON.stringify(observed);
    if (
      serverVisibleData.includes(key) ||
      serverVisibleData.includes(plaintext)
    ) {
      response.statusCode = 500;
      response.end("sensitive browser data leaked into HTTP request");
      return;
    }
    response.end("ok");
  });
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
  const viewResponse = await fetch(
    `http://127.0.0.1:${address.port}/quietbrightotter#${key}`,
  );
  if (!viewResponse.ok) {
    throw new Error("fragment leaked into view request");
  }
  const createResponse = await fetch(
    `http://127.0.0.1:${address.port}/api/v1/pastes#${key}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        mode: "encrypted",
        payload: {
          version: 1,
          algorithm: "A256GCM",
          iv: "AAECAwQFBgcICQoL",
          ciphertext: "AAECAwQFBgcICQoLDA0ODw",
        },
        expiry: "1h",
      }),
    },
  );
  if (!createResponse.ok) {
    throw new Error("sensitive data leaked into create request");
  }
  if (
    requests.length !== 2 ||
    requests.some((request) => request.url.includes("#"))
  ) {
    throw new Error("network test did not observe clean request targets");
  }
} finally {
  await new Promise((resolve, reject) =>
    server.close((error) => (error ? reject(error) : resolve())),
  );
}
