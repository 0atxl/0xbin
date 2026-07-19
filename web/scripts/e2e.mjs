import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "@playwright/test";

const root = new URL("../..", import.meta.url);
const apiPort = 18080;
const webPort = 15173;
const apiOrigin = `http://127.0.0.1:${apiPort}`;
const webOrigin = `http://127.0.0.1:${webPort}`;
const processes = [];

function start(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: root,
    env: { ...process.env, ...options.env },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let output = "";
  child.stdout.on("data", (chunk) => (output += chunk));
  child.stderr.on("data", (chunk) => (output += chunk));
  child.once("exit", (code) => {
    if (code !== 0 && !child.killed) {
      console.error(`${command} exited early (${code}):\n${output}`);
    }
  });
  processes.push(child);
  return child;
}

async function waitFor(url) {
  let lastError;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
      lastError = new Error(`${url} responded ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await delay(100);
  }
  throw lastError ?? new Error(`Timed out waiting for ${url}`);
}

async function stopAll() {
  await Promise.all(
    processes.map(
      (child) =>
        new Promise((resolve) => {
          if (child.exitCode !== null) return resolve();
          child.once("exit", resolve);
          child.kill("SIGTERM");
          setTimeout(() => child.kill("SIGKILL"), 2_000).unref();
        }),
    ),
  );
}

async function createPaste(page, content, options = {}) {
  await page.goto(webOrigin);
  await page.locator(".cm-content").fill(content);
  if (options.title)
    await page.getByPlaceholder("Untitled paste").fill(options.title);
  if (options.lifetime)
    await page.getByRole("button", { name: options.lifetime }).click();
  if (options.encrypted) await page.getByLabel("Encrypt").check();
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await page.waitForURL((url) => url.pathname !== "/");
  return page.url();
}

const dataDir = await mkdtemp(join(tmpdir(), "0xbin-e2e-"));
let browser;
try {
  start("go", ["run", "./cmd/0xbin"], {
    env: {
      OXBIN_LISTEN_ADDR: `127.0.0.1:${apiPort}`,
      OXBIN_BASE_URL: webOrigin,
      OXBIN_DATA_DIR: dataDir,
    },
  });
  await waitFor(`${apiOrigin}/health/ready`);
  start(
    "npm",
    [
      "--prefix",
      "web",
      "run",
      "dev",
      "--",
      "--host",
      "127.0.0.1",
      "--port",
      `${webPort}`,
    ],
    {
      env: { OXBIN_API_PROXY_TARGET: apiOrigin },
    },
  );
  await waitFor(webOrigin);

  browser = await chromium.launch({
    headless: true,
    executablePath:
      process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ?? "/usr/bin/chromium",
  });
  const context = await browser.newContext();
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: webOrigin,
  });
  const page = await context.newPage();

  const plaintextURL = await createPaste(page, "package main\n", {
    title: "main.go",
    lifetime: "1h",
  });
  await expectVisible(page, "main.go");
  await expectVisible(page, "package main");
  assert.match(new URL(plaintextURL).pathname, /^\/[a-z]+$/);

  const secret = "client-side secret must not reach the server";
  const requests = [];
  page.on("request", (request) => {
    if (request.url().startsWith(webOrigin)) {
      requests.push({
        url: request.url(),
        headers: request.headers(),
        body: request.postData() ?? "",
      });
    }
  });
  const encryptedURL = await createPaste(page, secret, {
    title: "encrypted.txt",
    lifetime: "24h",
    encrypted: true,
  });
  const key = new URL(encryptedURL).hash.slice(1);
  assert.equal(
    key.length > 0,
    true,
    "encrypted URL must contain a key fragment",
  );
  await expectVisible(page, secret);
  for (const request of requests) {
    const observed = JSON.stringify(request);
    assert.equal(
      request.url.includes("#"),
      false,
      "request target contained a fragment",
    );
    assert.equal(
      observed.includes(secret),
      false,
      "encrypted plaintext reached a request",
    );
    assert.equal(
      observed.includes(key),
      false,
      "encryption key reached a request",
    );
  }

  const noKeyURL = encryptedURL.split("#")[0];
  await page.goto(noKeyURL);
  await expectVisible(page, "Encrypted paste");
  await page
    .getByLabel("Paste decryption key")
    .fill("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
  await page.getByRole("button", { name: "Decrypt" }).click();
  await expectVisible(page, "Unable to decrypt — check the key.");
  await page.getByLabel("Paste decryption key").fill(encryptedURL);
  await page.getByRole("button", { name: "Decrypt" }).click();
  await expectVisible(page, secret);

  const burnURL = await createPaste(page, "destroy me", { lifetime: "Once" });
  await expectVisible(page, "View-once paste");
  await page.getByRole("button", { name: "Reveal and destroy" }).click();
  await expectVisible(page, "destroy me");
  await page.goto(burnURL);
  await expectVisible(page, "Paste unavailable");

  await page.goto(`${webOrigin}/quietbrightotter`);
  await expectVisible(page, "Paste unavailable");
  await context.close();
} finally {
  await browser?.close();
  await stopAll();
  await rm(dataDir, { recursive: true, force: true });
}

async function expectVisible(page, text) {
  await page
    .getByText(text, { exact: false })
    .first()
    .waitFor({ state: "visible" });
}

console.log("Browser journeys passed.");
