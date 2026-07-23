import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { chromium } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const root = new URL("../..", import.meta.url);
const apiPort = 18080;
const webPort = 15173;
const apiOrigin = `http://127.0.0.1:${apiPort}`;
const webOrigin = `http://127.0.0.1:${webPort}`;
const webDirectory = fileURLToPath(new URL("../", import.meta.url));
const viteEntryPoint = fileURLToPath(
  new URL("../node_modules/vite/bin/vite.js", import.meta.url),
);
const processes = [];
const execFileAsync = promisify(execFile);

function progress(message) {
  console.log(`[e2e] ${message}`);
}

function start(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: options.cwd ?? root,
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
  progress("stopping test processes");
  await Promise.all(processes.map(stopProcess));
}

async function stopProcess(child) {
  if (hasExited(child)) return;

  const exited = new Promise((resolve) => child.once("exit", resolve));
  child.kill("SIGTERM");
  await Promise.race([exited, delay(2_000)]);
  if (hasExited(child)) return;

  child.kill("SIGKILL");
  await Promise.race([exited, delay(2_000)]);
  if (!hasExited(child)) {
    throw new Error(`Timed out stopping E2E child process ${child.pid}`);
  }
}

function hasExited(child) {
  return child.exitCode !== null || child.signalCode !== null;
}

async function createPaste(page, content, options = {}) {
  await page.goto(webOrigin);
  await page.locator(".cm-content").fill(content);
  if (options.title)
    await page.getByPlaceholder("Untitled paste").fill(options.title);
  if (options.lifetime) {
    const labels = { "24h": "1d", "72h": "3d" };
    await page
      .getByRole("button", {
        name: labels[options.lifetime] ?? options.lifetime,
      })
      .click();
  }
  if (options.encrypted)
    await page.getByText("Encrypt", { exact: true }).click();
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await page.waitForURL((url) => url.pathname !== "/");
  return page.url();
}

const dataDir = await mkdtemp(join(tmpdir(), "0xbin-e2e-"));
const binaryPath = join(dataDir, "0xbin");
let browser;
try {
  progress("building Go server");
  await execFileAsync("go", ["build", "-o", binaryPath, "./cmd/0xbin"], {
    cwd: root,
  });
  start(binaryPath, [], {
    env: {
      OXBIN_LISTEN_ADDR: `127.0.0.1:${apiPort}`,
      OXBIN_BASE_URL: webOrigin,
      OXBIN_DATA_DIR: dataDir,
    },
  });
  await waitFor(`${apiOrigin}/health/ready`);
  progress("backend ready");
  start(
    process.execPath,
    [viteEntryPoint, "--host", "127.0.0.1", "--port", `${webPort}`],
    {
      cwd: webDirectory,
      env: { OXBIN_API_PROXY_TARGET: apiOrigin },
    },
  );
  await waitFor(webOrigin);
  progress("frontend ready");

  progress("launching browser");
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

  progress("checking create screen and responsive layout");
  await page.goto(webOrigin);
  await assertNoSeriousAccessibilityIssues(page, "create screen");
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.getByText("Encrypt", { exact: true }).click();
  await expectVisible(page, "The key stays in the copied link.");
  await assert.equal(
    await page
      .locator(".toast-timer")
      .evaluate((timer) => getComputedStyle(timer).animationName),
    "none",
    "notification cooldown animation should stop when reduced motion is enabled",
  );
  await page.emulateMedia({ reducedMotion: "no-preference" });
  await page.reload();
  await assert.equal(
    await page.getByRole("button", { name: "Site menu" }).count(),
    0,
    "self-hosted UI should not include the policy menu",
  );
  await page.locator(".code-editor .cm-content").fill("x".repeat(2_000));
  await assert.equal(
    await page
      .locator(".code-editor .cm-scroller")
      .evaluate((scroller) => scroller.scrollWidth <= scroller.clientWidth),
    true,
    "creation editor should wrap long lines without horizontal overflow",
  );
  await page.reload();
  await page.setViewportSize({ width: 425, height: 844 });
  await expectVisible(page, "Plain text");
  await page.setViewportSize({ width: 375, height: 844 });
  await page.getByRole("button", { name: "3d", exact: true }).click();
  await page.waitForTimeout(220);
  await assert.equal(
    await page.locator(".lifetime-indicator").evaluate((indicator) => {
      const selected = document.querySelector(
        '.lifetime-selector button[aria-pressed="true"]',
      );
      if (!(selected instanceof HTMLElement)) return false;
      return (
        Math.abs(
          indicator.getBoundingClientRect().left -
            selected.getBoundingClientRect().left,
        ) < 1
      );
    }),
    true,
    "expiry indicator should stay aligned with centered options on narrow screens",
  );
  await assert.equal(
    await page.locator(".lifetime-selector").evaluate((selector) => {
      const create = document.querySelector(".primary-action");
      if (!(create instanceof HTMLElement)) return false;
      const lifetime = selector.getBoundingClientRect();
      const action = create.getBoundingClientRect();
      return lifetime.bottom <= action.top || action.bottom <= lifetime.top;
    }),
    true,
    "mobile lifetime selector and Create action should not overlap",
  );
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expectVisible(page, "Empty paste");
  await assert.equal(
    await page.getByText("Paste content is required", { exact: true }).count(),
    0,
    "validation should use the notification stack instead of bottom text",
  );

  progress("checking plaintext paste and search");
  const plaintextURL = await createPaste(page, "package main\npackage docs\n", {
    title: "main.go",
    lifetime: "1h",
  });
  await expectVisible(page, "main.go");
  await expectVisible(page, "package main");
  await page.locator(".flip-clock").waitFor({ state: "visible" });
  assert.equal(
    await page.locator(".flip-clock .flip-digit").count(),
    4,
    "expiry countdown should render four split-flap digits",
  );
  await assertNoSeriousAccessibilityIssues(page, "plaintext viewer");
  await assert.equal(
    await page
      .locator(".readonly-paste-editor .cm-scroller")
      .evaluate((scroller) => getComputedStyle(scroller).overflowX),
    "hidden",
    "viewer should not expose a horizontal scrollbar",
  );
  assert.match(new URL(plaintextURL).pathname, /^\/[a-z]+$/);

  await createPaste(page, "three-day paste", { lifetime: "3d" });
  await expectVisible(page, "three-day paste");

  await page.goto(plaintextURL);
  await expectVisible(page, "main.go");
  await page.keyboard.press("Control+F");
  await page.getByLabel("Search paste").fill("package");
  await expectVisible(page, "1 / 2");
  await page.getByRole("button", { name: "Next match" }).click();
  await expectVisible(page, "2 / 2");
  await page.getByRole("button", { name: "Previous match" }).click();
  await expectVisible(page, "1 / 2");
  await page.getByLabel("Search paste").focus();
  await page.keyboard.press("Escape");
  await page.getByLabel("Search paste").waitFor({ state: "hidden" });
  await page.setViewportSize({ width: 390, height: 844 });
  await expectVisible(page, "main.go");
  await assert.equal(
    await page.locator(".viewer-expiry-row").evaluate((row) => {
      const clock = row.querySelector(".flip-clock");
      const actions = document.querySelector(".viewer-actions");
      if (!(clock instanceof HTMLElement) || !(actions instanceof HTMLElement))
        return false;
      const rowBounds = row.getBoundingClientRect();
      const clockBounds = clock.getBoundingClientRect();
      const actionBounds = actions.getBoundingClientRect();
      const topGap = clockBounds.top - rowBounds.top;
      const bottomGap = rowBounds.bottom - clockBounds.bottom;
      return (
        topGap >= 0 &&
        bottomGap >= 0 &&
        Math.abs(topGap - bottomGap) < 2 &&
        clockBounds.bottom <= actionBounds.top
      );
    }),
    true,
    "mobile flip clock should be centered within its own row",
  );
  await page.getByRole("button", { name: "Download" }).waitFor({
    state: "visible",
  });
  await assert.equal(
    await page.locator(".viewer-actions").evaluate((actions) => {
      const title = document.querySelector("#viewer-heading");
      return (
        title !== null &&
        actions.getBoundingClientRect().top >
          title.getBoundingClientRect().bottom
      );
    }),
    true,
    "mobile actions should be a row below the title",
  );
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByLabel("Search paste").waitFor({ state: "visible" });
  await assert.equal(
    await page.locator(".viewer-action-icons").evaluate((row) => {
      const buttons = row.querySelectorAll(".action-button");
      const first = buttons.item(0)?.getBoundingClientRect();
      const last = buttons.item(buttons.length - 1)?.getBoundingClientRect();
      const bounds = row.getBoundingClientRect();
      return (
        first !== undefined &&
        last !== undefined &&
        Math.abs(first.left - bounds.left) < 2 &&
        Math.abs(last.right - bounds.right) < 2
      );
    }),
    true,
    "mobile action icons should span the full action row",
  );
  await assert.equal(
    await page.locator(".viewer-search-row").evaluate((row) => {
      const control = row.querySelector(".search-control");
      const sections = control?.querySelectorAll("input, .action-button");
      const bounds = row.getBoundingClientRect();
      return (
        control instanceof HTMLElement &&
        sections?.length === 3 &&
        Math.abs(control.getBoundingClientRect().left - bounds.left + 8) < 2 &&
        Math.abs(control.getBoundingClientRect().right - bounds.right - 8) < 2
      );
    }),
    true,
    "mobile search should be a three-section control extending eight pixels past each row edge",
  );
  await assertFocused(page.getByLabel("Search paste"));
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByLabel("Search paste").waitFor({ state: "hidden" });
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await assertFocused(page.getByLabel("Search paste"));
  await page.getByLabel("Search paste").evaluate((input) => input.blur());
  await page.keyboard.press("Control+F");
  await assertFocused(page.getByLabel("Search paste"));
  await page.setViewportSize({ width: 1280, height: 900 });

  progress("checking encrypted paste flow");
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
  await page.getByPlaceholder("Decryption key here").waitFor({
    state: "visible",
  });
  await assert.equal(
    await page
      .getByRole("button", { name: "0xbin: create a new paste" })
      .count(),
    0,
    "key gate should not show application chrome",
  );
  await page
    .getByLabel("Paste decryption key")
    .fill("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
  await page.getByRole("button", { name: "Decrypt" }).click();
  await assert.equal(
    await page
      .getByLabel("Paste decryption key")
      .evaluate((input) => input.getAttribute("aria-invalid")),
    "true",
    "wrong keys should mark the compact key field invalid",
  );
  await expectVisible(page, "Unable to decrypt — check the key.");
  await page.getByLabel("Paste decryption key").fill(encryptedURL);
  await page.getByRole("button", { name: "Decrypt" }).click();
  await expectVisible(page, secret);

  progress("checking burn-after-read flow");
  const burnURL = await createPaste(page, "destroy me", { lifetime: "Once" });
  await expectVisible(page, "View-once paste");
  await page.getByRole("button", { name: "Reveal and destroy" }).click();
  await expectVisible(page, "destroy me");
  await page.goto(burnURL);
  await expectVisible(page, "Paste unavailable");

  await page.goto(`${webOrigin}/quietbrightotter`);
  await expectVisible(page, "Paste unavailable");

  progress("checking large and hostile paste handling");
  const largeContent = Array.from(
    { length: 10_000 },
    (_, index) => `${String(index + 1).padStart(5, "0")} ${"x".repeat(97)}`,
  ).join("\n");
  const largePasteURL = await createServerPaste(largeContent, "large.txt");
  await page.goto(largePasteURL);
  await expectVisible(page, "large.txt");
  const renderedLineCount = await page
    .locator(".readonly-paste-editor .cm-line")
    .count();
  assert.equal(
    renderedLineCount > 0 && renderedLineCount < 1_000,
    true,
    "viewer should virtualize the 10,000-line paste",
  );
  await page.locator(".readonly-paste-editor .cm-scroller").evaluate((node) => {
    node.scrollTop = node.scrollHeight;
  });
  await expectVisible(page, "10000");

  const hostilePasteURL = await createServerPaste(
    '<img src=x onerror="window.__0xbinXSS=true">\n<script>window.__0xbinXSS=true</script>',
    "untrusted.html",
  );
  await page.goto(hostilePasteURL);
  await expectVisible(page, "untrusted.html");
  await assert.equal(
    await page.evaluate(() => "__0xbinXSS" in window),
    false,
    "paste content must not execute as HTML or script",
  );

  progress("checking error handling and clipboard fallback");
  await expectCreateFailure(
    page,
    429,
    "rate_limited",
    "Too many requests — try again later",
  );
  await expectCreateFailure(
    page,
    503,
    "service_unavailable",
    "Could not create paste — try again",
  );

  const clipboardFailureContext = await browser.newContext();
  await clipboardFailureContext.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: () => Promise.reject(new Error("blocked")) },
    });
  });
  const clipboardFailurePage = await clipboardFailureContext.newPage();
  await createPaste(clipboardFailurePage, "copy failure coverage");
  await expectVisible(
    clipboardFailurePage,
    "Paste created — copy the link manually",
  );
  await clipboardFailureContext.close();
  await context.close();
  progress("all browser journeys passed");
} finally {
  progress("cleaning up");
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

async function assertFocused(locator) {
  await locator.waitFor({ state: "visible" });
  for (let attempt = 0; attempt < 30; attempt += 1) {
    if (await locator.evaluate((element) => element.matches(":focus"))) return;
    await delay(10);
  }
  await assert.equal(
    await locator.evaluate((element) => element.matches(":focus")),
    true,
    "expected element to be focused",
  );
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

async function expectCreateFailure(page, status, code, expectedMessage) {
  await page.route("**/api/v1/pastes", (route) =>
    route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code, message: "Request failed", request_id: "e2e" },
      }),
    }),
  );
  await page.goto(webOrigin);
  await page.locator(".cm-content").fill("failure coverage");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expectVisible(page, expectedMessage);
  await page.unroute("**/api/v1/pastes");
}

async function createServerPaste(content, title) {
  const response = await fetch(`${apiOrigin}/api/v1/pastes`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      mode: "plaintext",
      payload: { version: 1, title, language: "plaintext", content },
      expiry: "24h",
      burn_after_read: false,
    }),
  });
  assert.equal(response.ok, true, "server should accept the large test paste");
  const created = await response.json();
  return created.url;
}

console.log("Browser journeys passed.");
