import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

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

  await page.goto(webOrigin);
  await assertNoSeriousAccessibilityIssues(page, "create screen");

  const plaintextURL = await createPaste(page, "package main\n", {
    title: "main.go",
    lifetime: "1h",
  });
  await expectVisible(page, "main.go");
  await expectVisible(page, "package main");
  await assertNoSeriousAccessibilityIssues(page, "plaintext viewer");
  assert.match(new URL(plaintextURL).pathname, /^\/[a-z]+$/);

  await createPaste(page, "three-day paste", { lifetime: "3d" });
  await expectVisible(page, "three-day paste");

  await page.goto(plaintextURL);
  await page.keyboard.press("Control+F");
  await page.getByLabel("Search paste").fill("package");
  await page.keyboard.press("Escape");
  await page.setViewportSize({ width: 390, height: 844 });
  await expectVisible(page, "main.go");
  await page.getByRole("button", { name: "Search" }).click();
  await page.getByLabel("Search paste").waitFor({ state: "visible" });
  await assert.equal(
    await page
      .getByLabel("Search paste")
      .evaluate((input) => input.matches(":focus")),
    true,
    "search input should be focused after opening",
  );
  await page.setViewportSize({ width: 1280, height: 900 });

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

async function assertNoSeriousAccessibilityIssues(page, screen) {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa"])
    .analyze();
  const serious = results.violations.filter(
    (violation) =>
      violation.impact === "serious" || violation.impact === "critical",
  );
  assert.deepEqual(
    serious,
    [],
    `${screen} has serious accessibility violations: ${serious
      .map((violation) => violation.id)
      .join(", ")}`,
  );
}

console.log("Browser journeys passed.");
