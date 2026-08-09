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
const hostedWebPort = 15174;
const apiOrigin = `http://127.0.0.1:${apiPort}`;
const webOrigin = `http://127.0.0.1:${webPort}`;
const hostedWebOrigin = `http://127.0.0.1:${hostedWebPort}`;
const webDirectory = fileURLToPath(new URL("../", import.meta.url));
const viteEntryPoint = fileURLToPath(
  new URL("../node_modules/vite/bin/vite.js", import.meta.url),
);
const processes = [];
const execFileAsync = promisify(execFile);

function progress(message) {
  console.log(`[e2e] ${message}`);
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((nextResolve, nextReject) => {
    resolve = nextResolve;
    reject = nextReject;
  });
  return { promise, resolve, reject };
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

async function liveEditorText(locator) {
  return locator.evaluate((content) => {
    const copy = content.cloneNode(true);
    copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
      cursor.remove();
    });
    return copy.textContent;
  });
}

async function installLiveSocketControl(page) {
  let currentSocket;
  await page.routeWebSocket(/\/api\/v1\/live\/[^/]+\/ws$/, (socket) => {
    currentSocket = socket;
    socket.connectToServer();
  });
  return {
    async disconnect() {
      assert.ok(currentSocket, "expected an active live WebSocket");
      await currentSocket.close({
        code: 4001,
        reason: "simulate access-session reconnect",
      });
    },
    sendToPage(message) {
      assert.ok(currentSocket, "expected an active live WebSocket");
      currentSocket.send(JSON.stringify(message));
    },
  };
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
      OXBIN_LIVE_MAX_WRITERS: "2",
      OXBIN_LIVE_MAX_VIEWERS: "1",
      OXBIN_LIVE_MAX_PARTICIPANTS: "3",
      OXBIN_LIVE_CREATE_RATE: "100/1h",
      OXBIN_LIVE_CONNECTION_RATE: "600/1m",
      OXBIN_LIVE_HEARTBEAT_INTERVAL: "5s",
      OXBIN_LIVE_RECONNECT_GRACE: "5s",
      OXBIN_LIVE_PARTICIPANT_TIMEOUT: "10s",
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
  start(
    process.execPath,
    [viteEntryPoint, "--host", "127.0.0.1", "--port", `${hostedWebPort}`],
    {
      cwd: webDirectory,
      env: {
        OXBIN_API_PROXY_TARGET: apiOrigin,
        OXBIN_HOSTED_PREVIEW: "true",
      },
    },
  );
  await waitFor(hostedWebOrigin);
  progress("hosted frontend preview ready");

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
    await page.getByLabel("Site menu").count(),
    0,
    "self-hosted UI should not include the policy menu",
  );

  const hostedPage = await context.newPage();
  await hostedPage.goto(`${hostedWebOrigin}/privacy`);
  await expectVisible(hostedPage, "Privacy & reports");
  await expectVisible(hostedPage, "hello@atulk.me");
  await assertNoSeriousAccessibilityIssues(hostedPage, "privacy and reports");
  await hostedPage.getByLabel("Site menu").click();
  await hostedPage
    .getByRole("link", { name: "Terms & conditions", exact: true })
    .click();
  await expectVisible(hostedPage, "Terms & conditions");
  await assert.equal(
    new URL(hostedPage.url()).pathname,
    "/terms",
    "policy menu should use client-side navigation",
  );
  await hostedPage.getByLabel("Site menu").click();
  await assert.equal(
    await hostedPage
      .getByRole("link", { name: "GitHub", exact: true })
      .getAttribute("href"),
    "https://github.com/0atxl/0xbin",
    "hosted menu should link to the public repository",
  );
  await hostedPage.keyboard.press("Escape");
  await hostedPage.close();

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

  progress("checking LiveBin room, tabs, collaboration, and paste export");
  await page.goto(webOrigin);
  await page.getByRole("button", { name: "Open LiveBin" }).click();
  await page.getByLabel("Tab name").fill("main");
  await page
    .locator(".live-create-canvas .cm-content")
    .fill("shared main content");
  await page.getByRole("button", { name: "Create LiveBin room" }).click();
  await page.waitForURL((url) => url.pathname.startsWith("/live/"));
  const liveRoomURL = page.url();
  await expectVisible(page, "Connected");
  await assertNoSeriousAccessibilityIssues(page, "live room");
  for (const width of [700, 420, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await assert.equal(
      await page.evaluate(() => {
        const canvas = document.querySelector(".live-room-canvas");
        return (
          canvas instanceof HTMLElement &&
          canvas.scrollWidth <= document.documentElement.clientWidth
        );
      }),
      true,
      `live room should not overflow horizontally at ${width}px`,
    );
  }
  await page.setViewportSize({ width: 1280, height: 900 });
  progress("checking LiveBin participant popover interactions");
  const participantTrigger = page.locator(".live-connection-status");
  const participantPopover = page.locator(".live-participant-popover");
  await participantTrigger.hover();
  await participantPopover.waitFor({ state: "visible" });
  await page.locator(".live-room-identity").hover();
  await participantPopover.waitFor({ state: "hidden" });
  await participantTrigger.click();
  await participantPopover.waitFor({ state: "visible" });
  await page.keyboard.press("Escape");
  await participantPopover.waitFor({ state: "hidden" });
  await assertFocused(participantTrigger);
  await page.locator(".live-code-editor .cm-content").click();
  await participantTrigger.focus();
  await participantPopover.waitFor({ state: "visible" });
  await page.locator(".live-code-editor .cm-content").click();
  await participantPopover.waitFor({ state: "hidden" });
  const collaborator = await context.newPage();
  await collaborator.goto(liveRoomURL);
  await expectVisible(collaborator, "Connected");
  const rapidText = "shared rapid α🙂 edits";
  const collaboratorEditor = collaborator.locator(
    ".live-code-editor .cm-content",
  );
  await collaboratorEditor.click();
  await collaborator.keyboard.press("ControlOrMeta+A");
  await collaboratorEditor.pressSequentially(rapidText);
  await assert.equal(
    await liveEditorText(collaboratorEditor),
    rapidText,
    "rapid keyboard input should remain in the local collaborative editor",
  );
  await page.waitForFunction((expected) => {
    const content = document.querySelector(".live-code-editor .cm-content");
    if (!content) return false;
    const copy = content.cloneNode(true);
    copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
      cursor.remove();
    });
    return copy.textContent === expected;
  }, rapidText);
  await page
    .locator(".live-code-editor .cm-content")
    .waitFor({ state: "visible" });
  await page.waitForFunction(
    () => document.querySelectorAll(".live-remote-caret").length > 0,
  );
  await page.locator(".live-connection-status").click();
  await expectVisible(page, "joined");
  await expectVisible(page, "main");
  await page.locator(".live-connection-status").click();
  const rapidSnapshot = await page.evaluate(async () => {
    const response = await fetch(
      `/api/v1/live/${location.pathname.split("/").pop()}`,
    );
    return response.json();
  });
  await assert.equal(
    rapidSnapshot.documents.some((document) => document.content === rapidText),
    true,
    "rapid local and remote edits should match the authoritative HTTP snapshot",
  );
  await context.setOffline(true);
  const offlineText = `${rapidText} offline replay`;
  await collaboratorEditor.pressSequentially(" offline replay");
  await assert.equal(
    await liveEditorText(collaboratorEditor),
    offlineText,
    "offline text should remain available in the local editor",
  );
  await page.waitForFunction(
    () =>
      document.querySelector(".live-add-tab") instanceof HTMLButtonElement &&
      document.querySelector(".live-add-tab").disabled,
  );
  await context.setOffline(false);
  await page.waitForFunction((expected) => {
    const content = document.querySelector(".live-code-editor .cm-content");
    if (!content) return false;
    const copy = content.cloneNode(true);
    copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
      cursor.remove();
    });
    return copy.textContent === expected;
  }, offlineText);
  const replaySnapshot = await page.evaluate(async () => {
    const response = await fetch(
      `/api/v1/live/${location.pathname.split("/").pop()}`,
    );
    return response.json();
  });
  await assert.equal(
    replaySnapshot.documents.some(
      (document) => document.content === offlineText,
    ),
    true,
    "offline edits should converge after reconnect without structural changes",
  );
  await collaborator.close();
  await page.waitForFunction(
    () =>
      document.querySelector(".live-participant-count")?.textContent?.trim() ===
      "1",
    undefined,
    { timeout: 15_000 },
  );
  await page.waitForFunction(
    () => document.querySelectorAll(".live-remote-caret").length === 0,
    undefined,
    { timeout: 15_000 },
  );
  await page.getByRole("button", { name: /Add tab/ }).click();
  await expectVisible(page, "tab-2");
  await page.getByRole("button", { name: "tab-2", exact: true }).click();
  await page
    .locator(".live-tab-strip button.is-active")
    .filter({ hasText: "tab-2" })
    .waitFor({ state: "visible" });
  await page.waitForTimeout(100);
  await page
    .locator(".live-code-editor .cm-content")
    .fill("second tab content");
  await assert.equal(
    await page.locator(".live-code-editor .cm-content").textContent(),
    "second tab content",
    "the active LiveBin editor should retain local text immediately",
  );
  await page.waitForTimeout(150);
  await page
    .locator(".live-room-bottom-toolbar")
    .getByRole("button", { name: "Rename", exact: true })
    .click();
  await assertFocused(page.locator(".live-tab-rename-popover input"));
  await page.keyboard.press("Escape");
  await page.locator(".live-tab-rename-popover").waitFor({ state: "hidden" });
  await assertFocused(
    page
      .locator(".live-room-bottom-toolbar")
      .getByRole("button", { name: "Rename", exact: true }),
  );
  await page
    .locator(".live-room-bottom-toolbar")
    .getByRole("button", { name: "Rename", exact: true })
    .click();
  await page.locator(".live-tab-rename-popover input").fill("notes");
  await page
    .locator(".live-tab-rename-popover")
    .getByRole("button", { name: "Save", exact: true })
    .click();
  await expectVisible(page, "notes");
  await assert.equal(
    await page.locator(".live-code-editor .cm-content").textContent(),
    "second tab content",
    "renaming a LiveBin tab should not replace its document state",
  );
  await page.waitForTimeout(500);
  const liveRoomSnapshot = await page.evaluate(async () => {
    const response = await fetch(
      `/api/v1/live/${location.pathname.split("/").pop()}`,
    );
    return response.json();
  });
  await assert.equal(
    liveRoomSnapshot.documents.some(
      (document) => document.content === "second tab content",
    ),
    true,
    "LiveBin edits should reach the server-authoritative document",
  );
  await page
    .locator(".live-room-bottom-toolbar .custom-select > button")
    .click();
  await page
    .getByRole("listbox", { name: "Language" })
    .getByRole("button", { name: "Go", exact: true })
    .click();
  await expectVisible(page, "Go");
  await page
    .getByRole("button", { name: "Save as paste", exact: true })
    .click();
  await assertFocused(
    page.getByRole("menuitem", { name: "Current tab", exact: true }),
  );
  await page.keyboard.press("End");
  await assertFocused(
    page.getByRole("menuitem", { name: "Every tab", exact: true }),
  );
  await page.keyboard.press("Escape");
  await assertFocused(
    page.getByRole("button", { name: "Save as paste", exact: true }),
  );
  await page
    .getByRole("button", { name: "Save as paste", exact: true })
    .click();
  await page
    .getByRole("menuitem", { name: "Current tab", exact: true })
    .click();
  await page.waitForURL((url) => url.pathname === "/");
  await assert.equal(
    await page.locator(".title-field input").inputValue(),
    "notes",
    "saving a LiveBin tab should carry its tab name into the paste title",
  );
  await page
    .locator(".create-canvas .cm-content")
    .waitFor({ state: "visible" });
  await assert.equal(
    await page.locator(".create-canvas .cm-content").textContent(),
    "second tab content",
    "saving the current LiveBin tab should return to the normal paste editor",
  );

  progress("checking committed edit recovery after a lost acknowledgement");
  const acknowledgementContext = await browser.newContext();
  const acknowledgementPage = await acknowledgementContext.newPage();
  let droppedAcknowledgement = false;
  await acknowledgementPage.routeWebSocket(
    /\/api\/v1\/live\/[^/]+\/ws$/,
    (socket) => {
      const server = socket.connectToServer();
      server.onMessage((message) => {
        const text = Buffer.isBuffer(message) ? message.toString() : message;
        let event;
        try {
          event = JSON.parse(text);
        } catch {
          socket.send(message);
          return;
        }
        if (!droppedAcknowledgement && event.type === "changes") {
          droppedAcknowledgement = true;
          void socket.close({
            code: 4002,
            reason: "drop committed acknowledgement",
          });
          return;
        }
        socket.send(message);
      });
    },
  );
  await acknowledgementPage.goto(webOrigin);
  await acknowledgementPage
    .getByRole("button", { name: "Open LiveBin" })
    .click();
  await acknowledgementPage
    .locator(".live-create-canvas .cm-content")
    .fill("base");
  await acknowledgementPage
    .getByRole("button", { name: "Create LiveBin room" })
    .click();
  await acknowledgementPage.waitForURL((url) =>
    url.pathname.startsWith("/live/"),
  );
  const acknowledgementRoomURL = acknowledgementPage.url();
  await expectVisible(acknowledgementPage, "Connected");
  const resynchronized = acknowledgementPage.waitForResponse((response) => {
    const request = response.request();
    return (
      request.method() === "GET" &&
      new URL(response.url()).pathname ===
        `/api/v1/live/${new URL(acknowledgementRoomURL).pathname.split("/").pop()}` &&
      request.headers()["x-0xbin-live-client-id"] !== undefined
    );
  });
  const acknowledgementEditor = acknowledgementPage.locator(
    ".live-code-editor .cm-content",
  );
  await acknowledgementEditor.click();
  await acknowledgementEditor.pressSequentially("x");
  await resynchronized;
  await acknowledgementPage.waitForFunction(() => {
    const content = document.querySelector(".live-code-editor .cm-content");
    return content?.textContent === "basex";
  });
  await assert.equal(
    await liveEditorText(acknowledgementEditor),
    "basex",
    "HTTP reconciliation must not apply a committed local edit twice",
  );
  const acknowledgementSnapshot = await acknowledgementPage.evaluate(
    async () => {
      const response = await fetch(
        `/api/v1/live/${location.pathname.split("/").pop()}`,
      );
      return response.json();
    },
  );
  await assert.equal(
    acknowledgementSnapshot.documents[0].content,
    "basex",
    "authoritative HTTP state should contain one copy of the recovered edit",
  );
  const acknowledgementObserver = await acknowledgementContext.newPage();
  await acknowledgementObserver.goto(acknowledgementRoomURL);
  await expectVisible(acknowledgementObserver, "Connected");
  await acknowledgementObserver.waitForFunction(() => {
    const content = document.querySelector(".live-code-editor .cm-content");
    return content?.textContent === "basex";
  });
  await acknowledgementContext.close();

  progress("checking revision-disagreement replay");
  const disagreementContext = await browser.newContext();
  const disagreementObserverContext = await browser.newContext();
  const disagreementPage = await disagreementContext.newPage();
  let disagreementInjected = false;
  let disagreementOperationID = "";
  const replayedOperationIDs = [];
  const disagreementReplayed = deferred();
  const disagreementAccepted = deferred();
  await disagreementPage.routeWebSocket(
    /\/api\/v1\/live\/[^/]+\/ws$/,
    (socket) => {
      const server = socket.connectToServer();
      server.onMessage((message) => {
        const text = Buffer.isBuffer(message) ? message.toString() : message;
        let event;
        try {
          event = JSON.parse(text);
        } catch {
          socket.send(message);
          return;
        }
        if (
          event.type === "changes" &&
          event.operation_id === disagreementOperationID
        ) {
          disagreementAccepted.resolve();
        }
        if (
          event.type === "error" &&
          event.operation_id === disagreementOperationID
        ) {
          disagreementAccepted.reject(
            new Error(`replayed operation rejected: ${text}`),
          );
        }
        socket.send(message);
      });
      socket.onMessage((message) => {
        const text = Buffer.isBuffer(message) ? message.toString() : message;
        let event;
        try {
          event = JSON.parse(text);
        } catch {
          server.send(message);
          return;
        }
        if (!disagreementInjected && event.type === "push_changes") {
          disagreementInjected = true;
          disagreementOperationID = event.operation_id;
          socket.send(
            JSON.stringify({
              type: "error",
              code: "resync_required",
              message: "Revision disagreement",
              operation_id: event.operation_id,
              status: "resync_required",
            }),
          );
          return;
        }
        if (
          disagreementInjected &&
          event.type === "push_changes" &&
          event.operation_id === disagreementOperationID
        ) {
          replayedOperationIDs.push(event.operation_id);
          disagreementReplayed.resolve();
        }
        server.send(message);
      });
    },
  );
  await disagreementPage.goto(webOrigin);
  await disagreementPage.getByRole("button", { name: "Open LiveBin" }).click();
  await disagreementPage
    .locator(".live-create-canvas .cm-content")
    .fill("revision");
  await disagreementPage
    .getByRole("button", { name: "Create LiveBin room" })
    .click();
  await disagreementPage.waitForURL((url) => url.pathname.startsWith("/live/"));
  const disagreementURL = disagreementPage.url();
  const disagreementSlug = new URL(disagreementURL).pathname.split("/").pop();
  await expectVisible(disagreementPage, "Connected");
  const disagreementObserver = await disagreementObserverContext.newPage();
  await disagreementObserver.goto(disagreementURL);
  await expectVisible(disagreementObserver, "Connected");
  const disagreementEditor = disagreementPage.locator(
    ".live-code-editor .cm-content",
  );
  await disagreementEditor.click();
  await disagreementEditor.pressSequentially(" fixed");
  await disagreementReplayed.promise;
  await disagreementAccepted.promise;
  await disagreementPage.waitForFunction(() => {
    const content = document.querySelector(".live-code-editor .cm-content");
    return (
      content?.textContent === "revision fixed" &&
      document
        .querySelector(".live-connection-status")
        ?.textContent?.includes("Connected")
    );
  });
  await disagreementObserver.waitForFunction(() => {
    const content = document.querySelector(".live-code-editor .cm-content");
    if (!content) return false;
    const copy = content.cloneNode(true);
    copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
      cursor.remove();
    });
    return copy.textContent === "revision fixed";
  });
  const disagreementSnapshot = await disagreementObserver.evaluate(
    async (slug) => {
      const response = await fetch(`/api/v1/live/${slug}`);
      return response.json();
    },
    disagreementSlug,
  );
  assert.ok(disagreementOperationID, "expected a rejected operation ID");
  assert.deepEqual(
    replayedOperationIDs,
    [disagreementOperationID],
    "revision recovery should replay one stable operation ID",
  );
  assert.equal(await liveEditorText(disagreementEditor), "revision fixed");
  assert.equal(
    await liveEditorText(
      disagreementObserver.locator(".live-code-editor .cm-content"),
    ),
    "revision fixed",
  );
  assert.equal(disagreementSnapshot.documents[0].content, "revision fixed");
  await disagreementContext.close();
  await disagreementObserverContext.close();

  progress("checking validation rejection recovery");
  const validationContext = await browser.newContext();
  const validationObserverContext = await browser.newContext();
  const validationPage = await validationContext.newPage();
  let validationInjected = false;
  await validationPage.routeWebSocket(
    /\/api\/v1\/live\/[^/]+\/ws$/,
    (socket) => {
      const server = socket.connectToServer();
      socket.onMessage((message) => {
        const text = Buffer.isBuffer(message) ? message.toString() : message;
        let event;
        try {
          event = JSON.parse(text);
        } catch {
          server.send(message);
          return;
        }
        if (!validationInjected && event.type === "push_changes") {
          validationInjected = true;
          socket.send(
            JSON.stringify({
              type: "error",
              code: "invalid_request",
              message: "Rejected change",
              operation_id: event.operation_id,
              status: "validation",
            }),
          );
          return;
        }
        server.send(message);
      });
    },
  );
  await validationPage.goto(webOrigin);
  await validationPage.getByRole("button", { name: "Open LiveBin" }).click();
  await validationPage
    .locator(".live-create-canvas .cm-content")
    .fill("accepted");
  await validationPage
    .getByRole("button", { name: "Create LiveBin room" })
    .click();
  await validationPage.waitForURL((url) => url.pathname.startsWith("/live/"));
  const validationURL = validationPage.url();
  const validationSlug = new URL(validationURL).pathname.split("/").pop();
  await expectVisible(validationPage, "Connected");
  const validationObserver = await validationObserverContext.newPage();
  await validationObserver.goto(validationURL);
  await expectVisible(validationObserver, "Connected");
  const validationEditor = validationPage.locator(
    ".live-code-editor .cm-content",
  );
  await validationEditor.click();
  await validationEditor.pressSequentially(" rejected");
  await expectVisible(validationPage, "Copy recovery text");
  await expectVisible(validationPage, "Recovery");
  assert.equal(
    await validationPage.getByText("Connected", { exact: true }).count(),
    0,
    "terminal validation recovery must not claim to be connected",
  );
  assert.equal(
    await liveEditorText(validationEditor),
    "accepted rejected",
    "validation rejection should preserve local editor text",
  );
  const validationSnapshot = await validationObserver.evaluate(async (slug) => {
    const response = await fetch(`/api/v1/live/${slug}`);
    return response.json();
  }, validationSlug);
  assert.equal(
    await liveEditorText(
      validationObserver.locator(".live-code-editor .cm-content"),
    ),
    "accepted",
  );
  assert.equal(validationSnapshot.documents[0].content, "accepted");
  await validationContext.close();
  await validationObserverContext.close();

  progress("checking stale HTTP snapshot reconciliation");
  const staleContext = await browser.newContext();
  const staleObserverContext = await browser.newContext();
  const stalePage = await staleContext.newPage();
  const staleSocket = await installLiveSocketControl(stalePage);
  await stalePage.goto(webOrigin);
  await stalePage.getByRole("button", { name: "Open LiveBin" }).click();
  await stalePage.locator(".live-create-canvas .cm-content").fill("base");
  await stalePage.getByRole("button", { name: "Create LiveBin room" }).click();
  await stalePage.waitForURL((url) => url.pathname.startsWith("/live/"));
  const staleRoomURL = stalePage.url();
  const staleSlug = new URL(staleRoomURL).pathname.split("/").pop();
  await expectVisible(stalePage, "Connected");
  await stalePage.getByRole("button", { name: "Add tab" }).click();
  await expectVisible(stalePage, "tab-2");

  const staleObserver = await staleObserverContext.newPage();
  await staleObserver.goto(staleRoomURL);
  await expectVisible(staleObserver, "Connected");
  const snapshotCaptured = deferred();
  const releaseSnapshot = deferred();
  let delayedSnapshotRequests = 0;
  await stalePage.route(
    new RegExp(`/api/v1/live/${staleSlug}$`),
    async (route) => {
      if (!route.request().headers()["x-0xbin-live-client-id"]) {
        await route.continue();
        return;
      }
      delayedSnapshotRequests += 1;
      const response = await route.fetch();
      snapshotCaptured.resolve();
      await releaseSnapshot.promise;
      await route.fulfill({ response });
    },
    { times: 1 },
  );
  staleSocket.sendToPage({
    type: "status",
    status: "http_resync_required",
  });
  await snapshotCaptured.promise;

  const staleObserverEditor = staleObserver.locator(
    ".live-code-editor .cm-content",
  );
  await staleObserverEditor.click();
  await staleObserverEditor.pressSequentially("x");
  await staleObserver.getByRole("button", { name: "tab-2" }).click();
  await staleObserver
    .locator(".live-room-bottom-toolbar")
    .getByRole("button", { name: "Delete", exact: true })
    .click();
  await staleObserver.waitForFunction(async (slug) => {
    const response = await fetch(`/api/v1/live/${slug}`);
    const snapshot = await response.json();
    return (
      snapshot.documents.length === 1 &&
      snapshot.documents[0].content === "basex" &&
      snapshot.metadata_revision >= 2
    );
  }, staleSlug);
  releaseSnapshot.resolve();

  await expectVisible(stalePage, "Connected");
  try {
    await stalePage.waitForFunction(
      () => {
        const editor = document.querySelector(".live-code-editor .cm-content");
        return (
          editor?.textContent === "basex" &&
          ![...document.querySelectorAll(".live-tab-strip button")].some(
            (button) => button.textContent?.includes("tab-2"),
          )
        );
      },
      undefined,
      { timeout: 10_000 },
    );
  } catch (error) {
    const diagnostics = await stalePage.evaluate(() => ({
      editor: document.querySelector(".live-code-editor .cm-content")
        ?.textContent,
      tabs: [...document.querySelectorAll(".live-tab-strip button")].map(
        (button) => button.textContent,
      ),
      connection: document.querySelector(".live-connection-status")
        ?.textContent,
      warning: document.querySelector(".live-queue-warning")?.textContent,
    }));
    throw new Error(
      `stale snapshot diagnostics: ${JSON.stringify(diagnostics)}`,
      {
        cause: error,
      },
    );
  }
  const staleEditor = stalePage.locator(".live-code-editor .cm-content");
  await staleEditor.click();
  await staleEditor.pressSequentially("y");
  await staleObserver.getByRole("button", { name: "main" }).click();
  await staleObserver.waitForFunction(() => {
    const editor = document.querySelector(".live-code-editor .cm-content");
    return editor?.textContent === "basexy";
  });
  const convergedSnapshot = await staleObserver.evaluate(async (slug) => {
    const response = await fetch(`/api/v1/live/${slug}`);
    return response.json();
  }, staleSlug);
  assert.equal(delayedSnapshotRequests, 1);
  assert.equal(await liveEditorText(staleEditor), "basexy");
  assert.equal(await liveEditorText(staleObserverEditor), "basexy");
  assert.equal(convergedSnapshot.documents[0].content, "basexy");
  await staleContext.close();
  await staleObserverContext.close();

  progress("checking terminal HTTP resynchronization recovery");
  const failedContext = await browser.newContext();
  const failedPage = await failedContext.newPage();
  let failedSocket;
  let dropFailedUpdate = false;
  const updateDropped = deferred();
  await failedPage.routeWebSocket(/\/api\/v1\/live\/[^/]+\/ws$/, (socket) => {
    failedSocket = socket;
    const server = socket.connectToServer();
    socket.onMessage((message) => {
      const text = Buffer.isBuffer(message) ? message.toString() : message;
      let event;
      try {
        event = JSON.parse(text);
      } catch {
        server.send(message);
        return;
      }
      if (dropFailedUpdate && event.type === "push_changes") {
        updateDropped.resolve();
        return;
      }
      server.send(message);
    });
  });
  await failedPage.goto(webOrigin);
  await failedPage.getByRole("button", { name: "Open LiveBin" }).click();
  await failedPage.locator(".live-create-canvas .cm-content").fill("recover");
  await failedPage.getByRole("button", { name: "Create LiveBin room" }).click();
  await failedPage.waitForURL((url) => url.pathname.startsWith("/live/"));
  const failedSlug = new URL(failedPage.url()).pathname.split("/").pop();
  await expectVisible(failedPage, "Connected");
  let failedSnapshotRequests = 0;
  await failedPage.route(
    new RegExp(`/api/v1/live/${failedSlug}$`),
    async (route) => {
      if (!route.request().headers()["x-0xbin-live-client-id"]) {
        await route.continue();
        return;
      }
      failedSnapshotRequests += 1;
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "service_unavailable",
            message: "Temporarily unavailable",
          },
        }),
      });
    },
  );
  dropFailedUpdate = true;
  const failedEditor = failedPage.locator(".live-code-editor .cm-content");
  await failedEditor.click();
  await failedEditor.pressSequentially(" local");
  await updateDropped.promise;
  assert.ok(failedSocket, "expected a failed-resync WebSocket");
  failedSocket.send(
    JSON.stringify({ type: "status", status: "http_resync_required" }),
  );
  await expectVisible(failedPage, "Copy recovery text");
  await expectVisible(failedPage, "Recovery");
  assert.equal(
    await liveEditorText(failedEditor),
    "recover local",
    "terminal resynchronization must preserve unsent local text",
  );
  assert.equal(
    failedSnapshotRequests,
    4,
    "terminal resynchronization should stop at its HTTP attempt bound",
  );
  await failedContext.close();

  progress("checking protected LiveBin access and hostile text rendering");
  const protectedCreatorSocket = await installLiveSocketControl(page);
  await page.goto(webOrigin);
  await page.getByRole("button", { name: "Open LiveBin" }).click();
  await page.getByText("Require password", { exact: true }).click();
  await page.locator(".live-password-field input").fill("correct horse");
  await page
    .locator(".live-create-canvas .cm-content")
    .fill('<img src=x onerror="window.__liveXSS=true">');
  await page.getByRole("button", { name: "Create LiveBin room" }).click();
  await page.waitForURL((url) => url.pathname.startsWith("/live/"));
  const protectedLiveURL = page.url();
  await page.reload();
  await expectVisible(page, "Connected");
  await page.locator(".live-connection-status").click();
  await expectVisible(page, "Room controls");
  await page.locator(".live-connection-status").click();
  progress("checking protected creator reauthentication after access expiry");
  const protectedSlug = new URL(protectedLiveURL).pathname.split("/").pop();
  const protectedCreatorCookies = await page
    .context()
    .cookies(`${apiOrigin}/api/v1/live/${protectedSlug}`);
  const protectedCreatorToken = protectedCreatorCookies.find(
    (cookie) => cookie.name === "oxbin_live_creator",
  );
  assert.ok(
    protectedCreatorToken?.httpOnly,
    "protected creator capability should be an HttpOnly cookie",
  );
  await page.context().clearCookies({ name: "oxbin_live_session" });
  await protectedCreatorSocket.disconnect();
  await expectVisible(page, "Room password");
  await page.getByLabel("Room password").fill("correct horse");
  await page.getByRole("button", { name: "Unlock" }).click();
  await expectVisible(page, "Connected");
  await page.locator(".live-connection-status").click();
  await expectVisible(page, "Room controls");
  const renewedCreatorCookies = await page
    .context()
    .cookies(`${apiOrigin}/api/v1/live/${protectedSlug}`);
  assert.equal(
    renewedCreatorCookies.some(
      (cookie) =>
        cookie.name === "oxbin_live_creator" &&
        cookie.value === protectedCreatorToken.value,
    ),
    true,
    "password renewal must preserve the creator capability cookie",
  );
  await page.locator(".live-connection-status").click();
  progress("checking protected LiveBin password gate");
  const protectedContext = await browser.newContext();
  const protectedVisitor = await protectedContext.newPage();
  const protectedVisitorSocket =
    await installLiveSocketControl(protectedVisitor);
  await protectedVisitor.goto(protectedLiveURL);
  await expectVisible(protectedVisitor, "Room password");
  await assertNoSeriousAccessibilityIssues(
    protectedVisitor,
    "live room password gate",
  );
  await protectedVisitor.getByLabel("Room password").fill("wrong password");
  await protectedVisitor.getByRole("button", { name: "Unlock" }).click();
  await expectVisible(protectedVisitor, "Password not accepted.");
  progress("checking protected LiveBin successful unlock");
  await protectedVisitor.getByLabel("Room password").fill("correct horse");
  await protectedVisitor.getByRole("button", { name: "Unlock" }).click();
  await expectVisible(protectedVisitor, "Connected");
  await assert.equal(
    await protectedVisitor.evaluate(() => "__liveXSS" in window),
    false,
    "live room text must not execute as HTML",
  );
  progress(
    "checking protected participant reauthentication after access expiry",
  );
  await protectedContext.clearCookies({ name: "oxbin_live_session" });
  await protectedVisitorSocket.disconnect();
  await expectVisible(protectedVisitor, "Room password");
  await protectedVisitor.getByLabel("Room password").fill("correct horse");
  await protectedVisitor.getByRole("button", { name: "Unlock" }).click();
  await expectVisible(protectedVisitor, "Connected");
  await protectedContext.close();

  const reconnectContext = await browser.newContext();
  const reconnectCreator = await reconnectContext.newPage();
  const reconnectCreatorSocket =
    await installLiveSocketControl(reconnectCreator);
  await reconnectCreator.goto(webOrigin);
  await reconnectCreator.getByRole("button", { name: "Open LiveBin" }).click();
  await reconnectCreator
    .getByRole("button", { name: "Create LiveBin room" })
    .click();
  await reconnectCreator.waitForURL((url) => url.pathname.startsWith("/live/"));
  await reconnectCreator.reload();
  await expectVisible(reconnectCreator, "Connected");
  await reconnectCreator.locator(".live-connection-status").click();
  await expectVisible(reconnectCreator, "Room controls");
  await reconnectCreator.locator(".live-connection-status").click();
  await reconnectContext.clearCookies({ name: "oxbin_live_session" });
  await reconnectCreatorSocket.disconnect();
  await expectVisible(reconnectCreator, "Connected");
  await assert.equal(
    await reconnectCreator.getByText("Room password", { exact: true }).count(),
    0,
    "unprotected reconnect must not show the password gate",
  );
  await reconnectCreator.locator(".live-connection-status").click();
  await expectVisible(reconnectCreator, "Room controls");
  await reconnectContext.close();

  progress(
    "checking LiveBin creator authority, watch-only access, and capacity",
  );
  const ownerContext = await browser.newContext();
  const writerContext = await browser.newContext();
  const viewerContext = await browser.newContext();
  const overflowContext = await browser.newContext();
  const owner = await ownerContext.newPage();
  await owner.goto(webOrigin);
  await owner.getByRole("button", { name: "Open LiveBin" }).click();
  await owner
    .locator(".live-create-canvas .cm-content")
    .fill("creator controls coverage");
  await owner.getByRole("button", { name: "Create LiveBin room" }).click();
  await owner.waitForURL((url) => url.pathname.startsWith("/live/"));
  const accessRoomURL = owner.url();
  const accessURL = new URL(accessRoomURL);
  assert.equal(accessURL.search, "", "live room URL must not carry authority");
  assert.equal(accessURL.hash, "", "live room URL must not carry authority");
  await expectVisible(owner, "Connected");
  await owner
    .locator(".live-room-bottom-toolbar")
    .getByRole("button", { name: "Rename", exact: true })
    .click();
  await owner.locator(".live-tab-rename-popover input").fill("tab draft");
  await owner.locator(".live-connection-status").click();
  await owner
    .locator(".live-rename-form")
    .getByRole("button", { name: "Rename", exact: true })
    .click();
  await owner
    .getByLabel("Temporary participant name")
    .fill("participant draft");
  await assert.equal(
    await owner.locator(".live-tab-rename-popover input").inputValue(),
    "tab draft",
    "participant rename state must not overwrite tab rename state",
  );
  await owner.keyboard.press("Escape");
  const ownerCookies = await ownerContext.cookies(
    `${apiOrigin}/api/v1/live/${accessURL.pathname.split("/").pop()}`,
  );
  assert.equal(
    ownerCookies.some(
      (cookie) => cookie.name === "oxbin_live_session" && cookie.httpOnly,
    ),
    true,
    "creator authority should remain in an HttpOnly session cookie",
  );
  assert.equal(
    ownerCookies.some(
      (cookie) => cookie.name === "oxbin_live_creator" && cookie.httpOnly,
    ),
    true,
    "creator capability should remain in a room-scoped HttpOnly cookie",
  );
  assert.equal(
    await owner.evaluate(
      (tokens) => {
        const storage = [localStorage, sessionStorage]
          .flatMap((area) =>
            Array.from({ length: area.length }, (_, index) => {
              const key = area.key(index) ?? "";
              return `${key}:${area.getItem(key) ?? ""}`;
            }),
          )
          .join("\n");
        return (
          !storage.includes("oxbin_live_session") &&
          !storage.includes("oxbin_live_creator") &&
          tokens.every((token) => !storage.includes(token))
        );
      },
      ownerCookies
        .filter(
          (cookie) =>
            cookie.name === "oxbin_live_session" ||
            cookie.name === "oxbin_live_creator",
        )
        .map((cookie) => cookie.value),
    ),
    true,
    "creator authority must not be placed in browser storage",
  );
  const writer = await writerContext.newPage();
  await writer.goto(accessRoomURL);
  await expectVisible(writer, "Connected");
  await writer.locator(".live-connection-status").click();
  const writerName = await writer
    .locator(".live-participant-row")
    .filter({ hasText: "You · writer" })
    .locator("strong")
    .textContent();
  assert.ok(writerName, "writer should have a participant identity");
  await writer.locator(".live-connection-status").click();

  const viewer = await viewerContext.newPage();
  await viewer.goto(accessRoomURL);
  await expectVisible(viewer, "You’re watching this room");
  await viewer.waitForFunction(
    () =>
      document.querySelector(".live-add-tab") instanceof HTMLButtonElement &&
      document.querySelector(".live-add-tab").disabled,
  );
  await owner.waitForFunction(
    () =>
      document.querySelector(".live-participant-count")?.textContent?.trim() ===
      "3",
  );

  const overflow = await overflowContext.newPage();
  await overflow.goto(accessRoomURL);
  await expectVisible(overflow, "Room is full. Ask someone to leave");

  await owner.locator(".live-connection-status").click();
  await owner
    .getByRole("button", { name: "Make room watch-only", exact: true })
    .click();
  await expectVisible(writer, "You’re watching this room");
  await writer.waitForFunction(
    () =>
      document.querySelector(".live-add-tab") instanceof HTMLButtonElement &&
      document.querySelector(".live-add-tab").disabled,
  );
  await owner
    .getByRole("button", { name: `Remove ${writerName}`, exact: true })
    .click();
  await expectVisible(writer, "You were removed from this room");
  await writer.reload();
  await expectVisible(writer, "You’re watching this room");

  await ownerContext.close();
  await writerContext.close();
  await viewerContext.close();
  await overflowContext.close();

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
