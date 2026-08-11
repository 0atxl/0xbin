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
  if (options.encrypted) await page.getByTitle("Encrypt").click();
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
  assert.equal(
    await page.locator(".create-canvas .primary-action svg").count(),
    0,
    "paste creation should use a text-only Create action",
  );
  assert.equal(
    await page
      .getByTitle("Encrypt")
      .evaluate((control) => Math.round(control.getBoundingClientRect().width)),
    32,
    "paste encryption should use the compact icon-only control",
  );
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.getByTitle("Encrypt").click();
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
  const liveCreateTabName = page.getByLabel("Tab name");
  assert.equal(
    await liveCreateTabName.inputValue(),
    "",
    "the internal default tab name should not appear in the live creator",
  );
  assert.equal(
    await liveCreateTabName.getAttribute("placeholder"),
    "Untitled tab",
    "the live creator should mirror the paste creator's untitled placeholder",
  );
  assert.equal(
    await page.locator(".live-create-canvas .byte-count").textContent(),
    "0 B / 1 MiB",
    "LiveBin should use the paste creator's MiB limit presentation",
  );
  assert.equal(
    await liveCreateTabName.evaluate(
      (input) => getComputedStyle(input).borderTopWidth,
    ),
    "0px",
    "the initial tab name should reuse the borderless paste-title treatment",
  );
  assert.equal(
    await page.locator(".live-create-canvas .primary-action svg").count(),
    0,
    "LiveBin creation should use a text-only Create action",
  );
  assert.equal(
    await page.locator(".live-metadata-bar").evaluate((bar) => {
      const input = bar.querySelector("input");
      const language = bar.querySelector(".custom-select > button");
      if (!(input instanceof HTMLElement) || !(language instanceof HTMLElement))
        return false;
      return (
        input.getBoundingClientRect().right <=
        language.getBoundingClientRect().left
      );
    }),
    true,
    "the language selector should remain to the right of the title-style tab name",
  );
  await liveCreateTabName.fill("temporary");
  await liveCreateTabName.fill("");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expectVisible(page, "Tab name cannot be empty");
  assert.equal(
    await liveCreateTabName.inputValue(),
    "",
    "an empty initial tab name should reset to the untitled placeholder state",
  );
  assert.equal(
    new URL(page.url()).pathname,
    "/live",
    "correcting an empty tab name should not submit the room",
  );
  await liveCreateTabName.fill("main");
  await page
    .locator(".live-create-canvas .cm-content")
    .fill("shared main content");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await page.waitForURL((url) => url.pathname.startsWith("/live/"));
  const liveRoomURL = page.url();
  await expectLiveConnected(page);
  await assert.equal(
    await page.locator(".live-room-identity").count(),
    0,
    "the workspace should not render a visible Live room identity block",
  );
  await assert.equal(
    await page.getByRole("button", { name: "Move tab earlier" }).count(),
    0,
    "the compact workspace should not render ordering arrows",
  );
  await assert.equal(
    (
      await page.getByRole("button", { name: "Copy room link" }).textContent()
    )?.trim(),
    "",
    "copy room link should remain icon-only",
  );
  await assert.equal(
    await page
      .getByRole("button", { name: "Copy room link" })
      .locator("svg")
      .evaluate((icon) => {
        const bounds = icon.getBBox();
        const viewBox = icon.viewBox.baseVal;
        return (
          bounds.x > viewBox.x &&
          bounds.y > viewBox.y &&
          bounds.x + bounds.width < viewBox.x + viewBox.width &&
          bounds.y + bounds.height < viewBox.y + viewBox.height
        );
      }),
    true,
    "copy room link icon should fit inside its view box",
  );
  await assert.equal(
    (await page.locator(".live-connection-status").textContent())?.trim(),
    "1",
    "connection status should place the participant count after its dot",
  );
  await assert.equal(
    await page.locator(".live-room-topbar").evaluate((topbar) => {
      const tabs = topbar.querySelector(".live-tab-strip");
      const actions = topbar.querySelector(".live-room-actions-bar");
      if (!(tabs instanceof HTMLElement) || !(actions instanceof HTMLElement))
        return false;
      return (
        Math.abs(
          tabs.getBoundingClientRect().top -
            actions.getBoundingClientRect().top,
        ) < 1
      );
    }),
    true,
    "tabs and room actions should share one top row",
  );
  await assert.equal(
    await page
      .locator(".live-room-actions-bar")
      .evaluate((actions) => getComputedStyle(actions).borderBottomWidth),
    "0px",
    "copy and connection controls should not have a second bottom rule",
  );
  const liveFooter = page.locator(".live-room-bottom-toolbar");
  assert.match(
    (await liveFooter.locator(".byte-count").textContent()) ?? "",
    /^\d+(?:\.\d+)? (?:B|KiB|MiB) \/ \d+(?:\.\d+)? MiB$/,
    "live room size should scale from bytes while keeping the limit in MiB",
  );
  const languageTrigger = page.locator(
    ".live-room-language-control .custom-select > button",
  );
  await languageTrigger.click();
  const languageList = page.getByRole("listbox", { name: "Language" });
  await languageList.waitFor({ state: "visible" });
  await assert.equal(
    await languageList.evaluate((list) => {
      const trigger = document.querySelector(
        ".live-room-language-control .custom-select > button",
      );
      return (
        trigger instanceof HTMLElement &&
        list.getBoundingClientRect().top >=
          trigger.getBoundingClientRect().bottom
      );
    }),
    true,
    "the top language menu should open downward like the creation-page selector",
  );
  await page.keyboard.press("Escape");
  await languageList.waitFor({ state: "hidden" });
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
  const longWorkspaceText = Array.from(
    { length: 180 },
    (_, index) => `fixed footer line ${index + 1}`,
  ).join("\n");
  await page.locator(".live-code-editor .cm-content").fill(longWorkspaceText);
  await assert.equal(
    await page.evaluate(() => {
      const canvas = document.querySelector(".live-room-canvas");
      const footer = document.querySelector(".live-room-bottom-toolbar");
      const scroller = document.querySelector(".live-code-editor .cm-scroller");
      if (
        !(canvas instanceof HTMLElement) ||
        !(footer instanceof HTMLElement) ||
        !(scroller instanceof HTMLElement)
      )
        return false;
      const canvasBounds = canvas.getBoundingClientRect();
      const footerBounds = footer.getBoundingClientRect();
      return (
        Math.abs(footerBounds.height - 56) < 1 &&
        Math.abs(footerBounds.bottom - canvasBounds.bottom) < 1 &&
        scroller.scrollHeight > scroller.clientHeight
      );
    }),
    true,
    "long documents should scroll while the 56px footer remains pinned",
  );
  await page
    .locator(".live-code-editor .cm-content")
    .fill("shared main content");
  progress("checking LiveBin participant popover interactions");
  const participantTrigger = page.locator(".live-connection-status");
  const participantPopover = page.locator(".live-participant-popover");
  await participantTrigger.hover();
  await participantPopover.waitFor({ state: "visible" });
  await page.locator(".live-tab-strip").hover();
  await participantPopover.waitFor({ state: "hidden" });
  await participantTrigger.click();
  await participantPopover.waitFor({ state: "visible" });
  await assert.equal(
    await page.evaluate(() => {
      const popover = document.querySelector(".live-participant-popover");
      const language = document.querySelector(
        ".live-room-language-control .custom-select > button",
      );
      const indicator = document.querySelector(".live-connection-status");
      if (
        !(popover instanceof HTMLElement) ||
        !(language instanceof HTMLElement) ||
        !(indicator instanceof HTMLElement)
      )
        return false;
      const popoverBounds = popover.getBoundingClientRect();
      const languageBounds = language.getBoundingClientRect();
      const indicatorBounds = indicator.getBoundingClientRect();
      return (
        Math.abs(popoverBounds.right - languageBounds.right) < 1 &&
        popoverBounds.top >= languageBounds.bottom &&
        popoverBounds.left <= indicatorBounds.right
      );
    }),
    true,
    "the participant popover should align beneath the right-side language control",
  );
  await page.keyboard.press("Escape");
  await participantPopover.waitFor({ state: "hidden" });
  await assertFocused(participantTrigger);
  await page.locator(".live-code-editor .cm-content").click();
  await participantTrigger.focus();
  await participantPopover.waitFor({ state: "visible" });
  await page.locator(".live-code-editor .cm-content").click();
  await participantPopover.waitFor({ state: "hidden" });

  progress("checking stable browser identity across tabs and reload");
  await participantTrigger.click();
  await participantPopover
    .getByRole("button", { name: "Rename your participant name" })
    .click();
  await page.getByLabel("Participant name").fill("Persistent Otter");
  await page.getByLabel("Participant name").press("Enter");
  await expectVisible(page, "Persistent Otter");
  const stableParticipantRow = page
    .locator(".live-participant-row")
    .filter({ hasText: "Persistent Otter" });
  const stableParticipantID = await stableParticipantRow.getAttribute(
    "data-participant-id",
  );
  assert.ok(stableParticipantID, "renamed participant should be authoritative");
  await assert.equal(
    (
      await stableParticipantRow
        .locator(".live-participant-name-button")
        .textContent()
    )
      ?.replace(/\s+/g, " ")
      .trim(),
    "Persistent Otter(You)",
    "the local marker should stay beside the participant name",
  );
  await assert.equal(
    (await stableParticipantRow.locator("small").textContent())
      ?.replace(/\s+/g, " ")
      .trim(),
    "creator · connected",
    "the compact participant detail should contain only designation and connection",
  );
  await assert.equal(
    await participantPopover
      .getByRole("button", { name: "Lock", exact: true })
      .evaluate((button) => button.getBoundingClientRect().height <= 28),
    true,
    "the creator lock action should remain compact",
  );

  const sameBrowserTab = await context.newPage();
  await sameBrowserTab.goto(liveRoomURL);
  await expectLiveConnected(sameBrowserTab);
  await stableParticipantRow.waitFor({ state: "visible" });
  await assert.equal(
    await page.locator(".live-participant-row").count(),
    1,
    "normal tabs in one browser must share one participant row",
  );
  const primaryIdentityEditor = page.locator(".live-code-editor .cm-content");
  const secondaryIdentityEditor = sameBrowserTab.locator(
    ".live-code-editor .cm-content",
  );
  await primaryIdentityEditor.click();
  await primaryIdentityEditor.press("End");
  await secondaryIdentityEditor.click();
  await secondaryIdentityEditor.press("End");
  await secondaryIdentityEditor.press("ArrowLeft");
  await page.locator(".live-connection-status").click();
  await page.waitForFunction(() => {
    const row = document.querySelector(".live-participant-row");
    return (
      row?.getAttribute("data-connection-count") === "2" &&
      row?.getAttribute("data-cursor-count") === "2"
    );
  });
  await sameBrowserTab.close();
  await page.waitForFunction(() => {
    const row = document.querySelector(".live-participant-row");
    return (
      row?.getAttribute("data-connection-count") === "1" &&
      row.textContent?.includes("connected")
    );
  });
  await page.reload();
  await expectLiveConnected(page);
  await page.locator(".live-connection-status").click();
  const reloadedParticipantRow = page
    .locator(".live-participant-row")
    .filter({ hasText: "Persistent Otter" });
  await reloadedParticipantRow.waitFor({ state: "visible" });
  assert.equal(
    await reloadedParticipantRow.getAttribute("data-participant-id"),
    stableParticipantID,
    "reload must preserve participant ID and authoritative nickname",
  );

  const collaboratorContext = await browser.newContext();
  const collaborator = await collaboratorContext.newPage();
  await collaborator.goto(liveRoomURL);
  await expectLiveConnected(collaborator);
  await page.waitForFunction(
    () => document.querySelectorAll(".live-participant-row").length === 2,
  );
  const participantIDs = await page
    .locator(".live-participant-row")
    .evaluateAll((rows) =>
      rows.map((row) => row.getAttribute("data-participant-id")),
    );
  assert.equal(
    new Set(participantIDs).size,
    2,
    "a separate browser context must receive a distinct participant",
  );
  let rapidText = "shared rapid α🙂 edits";
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
  const remoteCursorLayout = await page
    .locator(".live-remote-caret")
    .first()
    .evaluate((cursor) => {
      const label = cursor.querySelector(".live-remote-label");
      const scroller = cursor.closest(".cm-scroller");
      return {
        insideContent: cursor.closest(".cm-content") !== null,
        cursorWidth: cursor.getBoundingClientRect().width,
        labelTop: label?.getBoundingClientRect().top,
        scrollerTop: scroller?.getBoundingClientRect().top,
      };
    });
  assert.equal(
    remoteCursorLayout.insideContent,
    false,
    "remote cursors must render in an overlay rather than the document layout",
  );
  assert.equal(
    remoteCursorLayout.cursorWidth,
    0,
    "remote cursor overlays must consume no horizontal document space",
  );
  assert.ok(
    remoteCursorLayout.labelTop !== undefined &&
      remoteCursorLayout.scrollerTop !== undefined &&
      remoteCursorLayout.labelTop >= remoteCursorLayout.scrollerTop,
    "a label on the first line must remain inside the visible editor",
  );

  const caretLeftBeforeTyping = await page
    .locator(".live-remote-caret-line")
    .first()
    .evaluate((caret) => caret.getBoundingClientRect().left);
  await collaboratorEditor.pressSequentially("!");
  rapidText += "!";
  await page.waitForFunction((expected) => {
    const content = document.querySelector(".live-code-editor .cm-content");
    return content?.textContent === expected;
  }, rapidText);
  await page.waitForFunction((previousLeft) => {
    const caret = document.querySelector(".live-remote-caret-line");
    return !!caret && caret.getBoundingClientRect().left > previousLeft;
  }, caretLeftBeforeTyping);
  const caretLeftAfterTyping = await page
    .locator(".live-remote-caret-line")
    .first()
    .evaluate((caret) => caret.getBoundingClientRect().left);
  await page.waitForTimeout(250);
  const settledCaretLeft = await page
    .locator(".live-remote-caret-line")
    .first()
    .evaluate((caret) => caret.getBoundingClientRect().left);
  assert.ok(
    Math.abs(settledCaretLeft - caretLeftAfterTyping) < 0.5,
    "the authoritative presence update must not pull the cursor behind the last typed character",
  );

  await collaborator.keyboard.down("Shift");
  await collaborator.keyboard.press("ArrowLeft");
  await collaborator.keyboard.up("Shift");
  await assert.equal(
    await liveEditorText(collaboratorEditor),
    rapidText,
    "moving or extending a cursor must not mutate collaborative text",
  );
  await collaboratorEditor.press("End");

  const ownerEditor = page.locator(".live-code-editor .cm-content");
  const multilineText = `top\n${rapidText}`;
  await ownerEditor.fill(multilineText);
  await collaborator.waitForFunction((expected) => {
    const content = document.querySelector(".live-code-editor .cm-content");
    return (
      content &&
      [...content.querySelectorAll(".cm-line")]
        .map((line) => line.textContent)
        .join("\n") === expected
    );
  }, multilineText);
  await page.waitForTimeout(150);
  await page.evaluate(() => {
    window.__remoteCaretMotionSamples = [];
    window.__remoteCaretMotionTimer = window.setInterval(() => {
      const caret = document.querySelector(".live-remote-caret-line");
      if (!caret) return;
      const bounds = caret.getBoundingClientRect();
      window.__remoteCaretMotionSamples.push({
        left: bounds.left,
        top: bounds.top,
      });
    }, 5);
  });
  await ownerEditor.press("ControlOrMeta+Home");
  await ownerEditor.press("End");
  await ownerEditor.pressSequentially(" edits", { delay: 30 });
  const editedMultilineText = `top edits\n${rapidText}`;
  await collaborator.waitForFunction((expected) => {
    const content = document.querySelector(".live-code-editor .cm-content");
    return (
      content &&
      [...content.querySelectorAll(".cm-line")]
        .map((line) => line.textContent)
        .join("\n") === expected
    );
  }, editedMultilineText);
  await page.waitForTimeout(250);
  const lowerCaretMotion = await page.evaluate(() => {
    window.clearInterval(window.__remoteCaretMotionTimer);
    return window.__remoteCaretMotionSamples;
  });
  assert.ok(
    lowerCaretMotion.length > 5,
    "the lower remote cursor should remain measurable during earlier edits",
  );
  const lowerCaretLefts = lowerCaretMotion.map((sample) => sample.left);
  const lowerCaretTops = lowerCaretMotion.map((sample) => sample.top);
  assert.ok(
    Math.max(...lowerCaretLefts) - Math.min(...lowerCaretLefts) < 0.5 &&
      Math.max(...lowerCaretTops) - Math.min(...lowerCaretTops) < 0.5,
    "editing an earlier line must not jitter a lower remote cursor",
  );
  await ownerEditor.fill(rapidText);
  await collaborator.waitForFunction((expected) => {
    const content = document.querySelector(".live-code-editor .cm-content");
    return content?.textContent === expected;
  }, rapidText);
  await collaboratorEditor.press("End");

  const secondCollaboratorConnection = await collaboratorContext.newPage();
  await secondCollaboratorConnection.goto(liveRoomURL);
  await expectLiveConnected(secondCollaboratorConnection);
  const secondCollaboratorEditor = secondCollaboratorConnection.locator(
    ".live-code-editor .cm-content",
  );
  await secondCollaboratorEditor.click();
  await secondCollaboratorEditor.press("End");
  await page.waitForFunction(() =>
    [...document.querySelectorAll(".live-remote-labels")].some(
      (labels) => labels.children.length === 2,
    ),
  );
  const coincidentLabelBounds = await page.evaluate(() => {
    const labels = [...document.querySelectorAll(".live-remote-labels")].find(
      (candidate) => candidate.children.length === 2,
    );
    return labels
      ? [...labels.children].map((label) => {
          const bounds = label.getBoundingClientRect();
          return { left: bounds.left, right: bounds.right };
        })
      : [];
  });
  assert.equal(
    coincidentLabelBounds.length,
    2,
    "coincident cursors should retain both participant labels",
  );
  assert.ok(
    coincidentLabelBounds[0].right <= coincidentLabelBounds[1].left,
    "coincident cursor labels should be laid out side by side",
  );
  await secondCollaboratorConnection.close();
  await page.locator(".live-connection-status").click();
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
  await collaboratorContext.setOffline(true);
  const offlineText = `${rapidText} offline replay`;
  await collaboratorEditor.pressSequentially(" offline replay");
  await assert.equal(
    await liveEditorText(collaboratorEditor),
    offlineText,
    "offline text should remain available in the local editor",
  );
  await collaborator.waitForFunction(
    () =>
      document.querySelector(".live-add-tab") instanceof HTMLButtonElement &&
      document.querySelector(".live-add-tab").disabled,
  );
  await collaboratorContext.setOffline(false);
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
  await collaboratorContext.close();
  await page.waitForFunction(
    () =>
      document
        .querySelector(".live-connection-status")
        ?.getAttribute("aria-label")
        ?.includes("1 participant"),
    undefined,
    { timeout: 15_000 },
  );
  await page.waitForFunction(
    () => document.querySelectorAll(".live-remote-caret").length === 0,
    undefined,
    { timeout: 15_000 },
  );
  await page.getByRole("button", { name: /Add tab/ }).click();
  await expectVisible(page, "tab2");
  await page.getByRole("button", { name: "tab2", exact: true }).click();
  await page
    .locator(".live-tab-shell.is-active")
    .filter({ hasText: "tab2" })
    .waitFor({ state: "visible" });
  await page.waitForTimeout(100);
  await page
    .locator(".live-code-editor .cm-content")
    .fill("second tab content");
  await assert.equal(
    await liveEditorText(page.locator(".live-code-editor .cm-content")),
    "second tab content",
    "the active LiveBin editor should retain local text immediately",
  );
  await page.waitForTimeout(150);
  const activeTabName = page.locator(
    ".live-tab-shell.is-active .live-tab-item",
  );
  await activeTabName.click();
  await assertFocused(page.locator(".live-tab-name-input"));
  await page.keyboard.press("Escape");
  await page.locator(".live-tab-name-input").waitFor({ state: "hidden" });
  await activeTabName.click();
  await page.locator(".live-tab-name-input").fill("notes");
  await page.locator(".live-tab-name-input").press("Enter");
  await expectVisible(page, "notes");
  await assert.equal(
    await liveEditorText(page.locator(".live-code-editor .cm-content")),
    "second tab content",
    "renaming a LiveBin tab should not replace its document state",
  );
  await assert.equal(
    await page.locator(".live-tab-shell.is-active").evaluate((tab) => {
      const pencil = tab.querySelector(".live-pencil-icon");
      const name = tab.querySelector(".live-tab-item > span");
      const close = tab.querySelector(".live-tab-close");
      if (!(name instanceof HTMLElement) || !(close instanceof HTMLElement))
        return false;
      return (
        pencil === null &&
        name.getBoundingClientRect().right <= close.getBoundingClientRect().left
      );
    }),
    true,
    "the tab name should remain uncluttered and separated from delete",
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

  progress("checking the configured LiveBin tab limit and stable top controls");
  const fixedControlBounds = await page.evaluate(() => {
    const actions = document.querySelector(".live-room-actions-bar");
    const language = document.querySelector(".live-room-language-control");
    if (!(actions instanceof HTMLElement) || !(language instanceof HTMLElement))
      return undefined;
    return {
      actionsLeft: actions.getBoundingClientRect().left,
      languageLeft: language.getBoundingClientRect().left,
    };
  });
  assert.ok(fixedControlBounds, "fixed live controls should be present");
  await page.evaluate(() => {
    const activeTab = document.querySelector(".live-tab-shell.is-active");
    if (!(activeTab instanceof HTMLElement)) return;
    window.__liveTabWidthSamples = [activeTab.getBoundingClientRect().width];
    window.__liveTabWidthObserver = new ResizeObserver(() => {
      window.__liveTabWidthSamples.push(
        activeTab.getBoundingClientRect().width,
      );
    });
    window.__liveTabWidthObserver.observe(activeTab);
  });
  const addTabButton = page.locator(".live-add-tab");
  assert.equal(
    (await addTabButton.textContent())?.trim(),
    "+",
    "the visible tab creation control should remain icon-only",
  );
  assert.equal(
    await addTabButton.getAttribute("aria-label"),
    "Add tab",
    "the icon-only tab creation control should retain its accessible name",
  );
  assert.equal(
    await addTabButton.evaluate((button) => {
      const style = getComputedStyle(button);
      return (
        Number.parseInt(style.fontWeight, 10) >= 700 &&
        style.backgroundColor !== "rgba(0, 0, 0, 0)"
      );
    }),
    true,
    "the tab creation control should use a bold plus and tinted background",
  );
  for (let index = 3; index <= 8; index += 1) {
    await addTabButton.click();
    await page
      .getByRole("button", { name: `tab${index}`, exact: true })
      .waitFor({ state: "visible" });
  }
  await assert.equal(
    await addTabButton.isDisabled(),
    true,
    "Add tab should be disabled at the configured eight-tab limit",
  );
  await assert.equal(
    await page.locator(".live-tab-strip").evaluate((tabs) => {
      const style = getComputedStyle(tabs);
      return style.overflowX === "auto" && style.overflowY === "hidden";
    }),
    true,
    "the tab strip should scroll horizontally without changing row height",
  );
  await assert.deepEqual(
    await page.evaluate(() => {
      const actions = document.querySelector(".live-room-actions-bar");
      const language = document.querySelector(".live-room-language-control");
      if (
        !(actions instanceof HTMLElement) ||
        !(language instanceof HTMLElement)
      )
        return undefined;
      return {
        actionsLeft: actions.getBoundingClientRect().left,
        languageLeft: language.getBoundingClientRect().left,
      };
    }),
    fixedControlBounds,
    "copy, connection, and language controls should not shift as tabs are added",
  );
  const limitSnapshot = await page.evaluate(async () => {
    const response = await fetch(
      `/api/v1/live/${location.pathname.split("/").pop()}`,
    );
    return response.json();
  });
  assert.equal(
    limitSnapshot.documents.length,
    8,
    "the authoritative room should stop at eight tabs",
  );
  assert.match(
    (await page
      .locator(".live-connection-status")
      .getAttribute("aria-label")) ?? "",
    /^Connected\./,
    "reaching the tab limit should not disrupt the connection",
  );
  for (let index = 8; index >= 3; index -= 1) {
    await page
      .getByRole("button", { name: `Delete tab${index}`, exact: true })
      .click();
    await page
      .getByRole("button", { name: `tab${index}`, exact: true })
      .waitFor({ state: "detached" });
  }
  await assert.equal(
    await addTabButton.isEnabled(),
    true,
    "deleting a tab should re-enable Add tab without moving fixed controls",
  );
  const activeTabWidths = await page.evaluate(() => {
    window.__liveTabWidthObserver?.disconnect();
    return window.__liveTabWidthSamples ?? [];
  });
  assert.ok(
    activeTabWidths.length > 0,
    "the active tab should remain measurable",
  );
  assert.ok(
    Math.max(...activeTabWidths) - Math.min(...activeTabWidths) < 0.5,
    "the active tab should not resize while tabs are added or removed",
  );

  progress("checking append-after-delete and Chrome-style tab reordering");
  await page.getByRole("button", { name: "main", exact: true }).click();
  await page.getByRole("button", { name: "main", exact: true }).click();
  await page.locator(".live-tab-name-input").fill("tab1");
  await page.locator(".live-tab-name-input").press("Enter");
  await page.getByRole("button", { name: "tab1", exact: true }).waitFor();
  await page.getByRole("button", { name: "notes", exact: true }).click();
  await page.getByRole("button", { name: "notes", exact: true }).click();
  await page.locator(".live-tab-name-input").fill("tab2");
  await page.locator(".live-tab-name-input").press("Enter");
  await page.getByRole("button", { name: "tab2", exact: true }).waitFor();
  await addTabButton.click();
  await page.getByRole("button", { name: "tab3", exact: true }).waitFor();
  await page.getByRole("button", { name: "Delete tab1", exact: true }).click();
  await page
    .getByRole("button", { name: "tab1", exact: true })
    .waitFor({ state: "detached" });
  await addTabButton.click();
  await page.getByRole("button", { name: "tab4", exact: true }).waitFor();
  await assert.deepEqual(
    await page
      .locator(".live-tab-shell .live-tab-item > span")
      .allTextContents(),
    ["tab2", "tab3", "tab4"],
    "a tab created after deleting the first tab should append at the far right",
  );

  const draggedTab = page
    .locator(".live-tab-shell")
    .filter({ has: page.getByRole("button", { name: "tab4", exact: true }) });
  const firstTab = page
    .locator(".live-tab-shell")
    .filter({ has: page.getByRole("button", { name: "tab2", exact: true }) });
  const draggedBounds = await draggedTab.boundingBox();
  const firstBounds = await firstTab.boundingBox();
  assert.ok(draggedBounds && firstBounds, "reorderable tabs should be visible");
  await page.mouse.move(
    draggedBounds.x + draggedBounds.width / 2,
    draggedBounds.y + draggedBounds.height / 2,
  );
  await page.mouse.down();
  await page.mouse.move(
    firstBounds.x + firstBounds.width * 0.4,
    firstBounds.y + firstBounds.height / 2,
  );
  await assert.deepEqual(
    await page
      .locator(".live-tab-shell .live-tab-item > span")
      .allTextContents(),
    ["tab2", "tab3", "tab4"],
    "dragging should not repeatedly reorder live hit targets beneath the pointer",
  );
  await assert.equal(
    await draggedTab.evaluate(
      (tab) =>
        tab.classList.contains("is-dragging") &&
        getComputedStyle(tab).transform !== "none",
    ),
    true,
    "the dragged tab should move visually before its order is committed",
  );
  await assert.equal(
    await page
      .locator(".live-tab-shell:not(.is-dragging)")
      .evaluateAll((tabs) =>
        tabs.some((tab) => getComputedStyle(tab).transform !== "none"),
      ),
    true,
    "adjacent tabs should move into the prospective gap while dragging",
  );
  await page.mouse.up();
  await page.waitForFunction(
    () =>
      [...document.querySelectorAll(".live-tab-shell .live-tab-item > span")]
        .map((name) => name.textContent)
        .join(",") === "tab4,tab2,tab3",
  );
  await page.waitForFunction(
    () =>
      document.querySelector(".live-add-tab") instanceof HTMLButtonElement &&
      !document.querySelector(".live-add-tab").disabled,
  );
  await page
    .getByRole("button", { name: "tab4", exact: true })
    .press("Alt+Shift+ArrowRight");
  await page.waitForFunction(
    () =>
      [...document.querySelectorAll(".live-tab-shell .live-tab-item > span")]
        .map((name) => name.textContent)
        .join(",") === "tab2,tab4,tab3",
  );
  const reorderedSnapshot = await page.evaluate(async () => {
    const response = await fetch(
      `/api/v1/live/${location.pathname.split("/").pop()}`,
    );
    return response.json();
  });
  assert.deepEqual(
    reorderedSnapshot.documents.map((document) => document.name),
    ["tab2", "tab4", "tab3"],
    "pointer and keyboard tab moves should reach the server-authoritative order",
  );
  await page.getByRole("button", { name: "Delete tab4", exact: true }).click();
  await page
    .getByRole("button", { name: "tab4", exact: true })
    .waitFor({ state: "detached" });
  await page.getByRole("button", { name: "tab2", exact: true }).click();
  await page.locator(".live-tab-name-input").fill("notes");
  await page.locator(".live-tab-name-input").press("Enter");
  await page.getByRole("button", { name: "notes", exact: true }).waitFor();
  await page.getByRole("button", { name: "tab3", exact: true }).click();
  await page.getByRole("button", { name: "tab3", exact: true }).click();
  await page.locator(".live-tab-name-input").fill("main");
  await page.locator(".live-tab-name-input").press("Enter");
  await page.getByRole("button", { name: "main", exact: true }).waitFor();
  await page.getByRole("button", { name: "notes", exact: true }).click();

  await page
    .locator(".live-room-language-control .custom-select > button")
    .click();
  await page
    .getByRole("listbox", { name: "Language" })
    .getByRole("button", { name: "Go", exact: true })
    .click();
  await expectVisible(page, "Go");
  await page
    .getByRole("listbox", { name: "Language" })
    .waitFor({ state: "hidden" });
  await page.getByRole("button", { name: "main", exact: true }).click();
  await assert.match(
    (await page
      .locator(".live-room-language-control .custom-select > button")
      .textContent()) ?? "",
    /Plain text/,
    "each LiveBin tab should preserve its own language",
  );
  await page.getByRole("button", { name: "notes", exact: true }).click();
  await assert.match(
    (await page
      .locator(".live-room-language-control .custom-select > button")
      .textContent()) ?? "",
    /Go/,
    "returning to a tab should restore that tab's language",
  );
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
    .getByRole("button", { name: "Create", exact: true })
    .click();
  await acknowledgementPage.waitForURL((url) =>
    url.pathname.startsWith("/live/"),
  );
  const acknowledgementRoomURL = acknowledgementPage.url();
  await expectLiveConnected(acknowledgementPage);
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
    if (!content) return false;
    const copy = content.cloneNode(true);
    copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
      cursor.remove();
    });
    return copy.textContent === "basex";
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
  await expectLiveConnected(acknowledgementObserver);
  await acknowledgementObserver.waitForFunction(() => {
    const content = document.querySelector(".live-code-editor .cm-content");
    if (!content) return false;
    const copy = content.cloneNode(true);
    copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
      cursor.remove();
    });
    return copy.textContent === "basex";
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
    .getByRole("button", { name: "Create", exact: true })
    .click();
  await disagreementPage.waitForURL((url) => url.pathname.startsWith("/live/"));
  const disagreementURL = disagreementPage.url();
  const disagreementSlug = new URL(disagreementURL).pathname.split("/").pop();
  await expectLiveConnected(disagreementPage);
  const disagreementObserver = await disagreementObserverContext.newPage();
  await disagreementObserver.goto(disagreementURL);
  await expectLiveConnected(disagreementObserver);
  const disagreementEditor = disagreementPage.locator(
    ".live-code-editor .cm-content",
  );
  await disagreementEditor.click();
  await disagreementEditor.pressSequentially(" fixed");
  await disagreementReplayed.promise;
  await disagreementAccepted.promise;
  await disagreementPage.waitForFunction(() => {
    const content = document.querySelector(".live-code-editor .cm-content");
    if (!content) return false;
    const copy = content.cloneNode(true);
    copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
      cursor.remove();
    });
    return (
      copy.textContent === "revision fixed" &&
      document
        .querySelector(".live-connection-status")
        ?.getAttribute("aria-label")
        ?.startsWith("Connected.")
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
    .getByRole("button", { name: "Create", exact: true })
    .click();
  await validationPage.waitForURL((url) => url.pathname.startsWith("/live/"));
  const validationURL = validationPage.url();
  const validationSlug = new URL(validationURL).pathname.split("/").pop();
  await expectLiveConnected(validationPage);
  const validationObserver = await validationObserverContext.newPage();
  await validationObserver.goto(validationURL);
  await expectLiveConnected(validationObserver);
  const validationEditor = validationPage.locator(
    ".live-code-editor .cm-content",
  );
  await validationEditor.click();
  await validationEditor.pressSequentially(" rejected");
  await expectVisible(validationPage, "Copy recovery text");
  await expectVisible(validationPage, "Recovery");
  assert.equal(
    await validationPage
      .locator('.live-connection-status[aria-label^="Connected."]')
      .count(),
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
  await stalePage.getByRole("button", { name: "Create", exact: true }).click();
  await stalePage.waitForURL((url) => url.pathname.startsWith("/live/"));
  const staleRoomURL = stalePage.url();
  const staleSlug = new URL(staleRoomURL).pathname.split("/").pop();
  await expectLiveConnected(stalePage);
  await stalePage.getByRole("button", { name: "Add tab" }).click();
  await expectVisible(stalePage, "tab2");

  const staleObserver = await staleObserverContext.newPage();
  await staleObserver.goto(staleRoomURL);
  await expectLiveConnected(staleObserver);
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
  await staleObserver
    .getByRole("button", { name: "tab2", exact: true })
    .click();
  await staleObserver
    .getByRole("button", { name: "Delete tab2", exact: true })
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

  await expectLiveConnected(stalePage);
  try {
    await stalePage.waitForFunction(
      () => {
        const editor = document.querySelector(".live-code-editor .cm-content");
        if (!editor) return false;
        const copy = editor.cloneNode(true);
        copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
          cursor.remove();
        });
        return (
          copy.textContent === "basex" &&
          ![...document.querySelectorAll(".live-tab-strip button")].some(
            (button) => button.textContent?.includes("tab2"),
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
  await staleObserver
    .getByRole("button", { name: "tab1", exact: true })
    .click();
  await staleObserver.waitForFunction(() => {
    const editor = document.querySelector(".live-code-editor .cm-content");
    if (!editor) return false;
    const copy = editor.cloneNode(true);
    copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
      cursor.remove();
    });
    return copy.textContent === "basexy";
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
  await failedPage.getByRole("button", { name: "Create", exact: true }).click();
  await failedPage.waitForURL((url) => url.pathname.startsWith("/live/"));
  const failedSlug = new URL(failedPage.url()).pathname.split("/").pop();
  await expectLiveConnected(failedPage);
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
  await page.goto(webOrigin);
  await page.getByRole("button", { name: "Open LiveBin" }).click();
  await page.getByTitle("Require password").click();
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expectVisible(page, "Password is required.");
  const protectedPassword = page.getByPlaceholder("Password");
  await protectedPassword.fill("correct horse");
  assert.equal(
    await protectedPassword.getAttribute("type"),
    "password",
    "the protected-room password should be hidden by default",
  );
  await page.getByRole("button", { name: "Show password" }).click();
  assert.equal(
    await protectedPassword.getAttribute("type"),
    "text",
    "the password visibility control should reveal the entered password",
  );
  await page.getByRole("button", { name: "Hide password" }).click();
  assert.equal(
    await protectedPassword.getAttribute("type"),
    "password",
    "the password visibility control should hide the password again",
  );
  await protectedPassword.press("Enter");
  assert.equal(
    new URL(page.url()).pathname,
    "/live",
    "Enter in the password field should set the password without creating a room",
  );
  assert.equal(
    await page
      .getByRole("button", { name: "Create", exact: true })
      .evaluate((button) => document.activeElement === button),
    true,
    "setting a valid password should move focus to Create",
  );
  await page
    .locator(".live-create-canvas .cm-content")
    .fill('<img src=x onerror="window.__liveXSS=true">');
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await page.waitForURL((url) => url.pathname.startsWith("/live/"));
  const protectedLiveURL = page.url();
  await page.reload();
  await expectLiveConnected(page);
  await page.locator(".live-connection-status").click();
  await expectCreatorLockControl(page);
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
  const protectedPendingText =
    '<img src=x onerror="window.__liveXSS=true"> renewal pending';
  const protectedCreatorEditor = page.locator(".live-code-editor .cm-content");
  await page.context().setOffline(true);
  await expectLiveState(page, "Offline");
  await protectedCreatorEditor.click();
  await protectedCreatorEditor.press("End");
  await protectedCreatorEditor.pressSequentially(" renewal pending");
  await assert.equal(
    await liveEditorText(protectedCreatorEditor),
    protectedPendingText,
    "the pending renewal edit must exist before authentication begins",
  );
  await page.context().clearCookies({ name: "oxbin_live_session" });
  await page.context().setOffline(false);
  await expectVisible(page, "Room password");
  await assert.equal(
    await protectedCreatorEditor.isHidden(),
    true,
    "the retained workspace must remain hidden behind the password gate",
  );
  await page.getByLabel("Room password").fill("wrong password");
  await page.getByRole("button", { name: "Unlock" }).click();
  await expectVisible(page, "Password not accepted.");
  await assert.equal(
    await page.locator(".live-room-canvas").count(),
    1,
    "a rejected renewal must keep the hidden workspace mounted",
  );
  await page.getByLabel("Room password").fill("correct horse");
  await page.getByRole("button", { name: "Unlock" }).click();
  await expectLiveConnected(page);
  await page.waitForFunction((content) => {
    const editor = document.querySelector(".live-code-editor .cm-content");
    if (!editor) return false;
    const copy = editor.cloneNode(true);
    copy.querySelectorAll(".live-remote-caret").forEach((cursor) => {
      cursor.remove();
    });
    return copy.textContent === content;
  }, protectedPendingText);
  await page.waitForFunction(
    async ({ slug, content }) => {
      const response = await fetch(`/api/v1/live/${slug}`);
      if (!response.ok) return false;
      const snapshot = await response.json();
      return snapshot.documents.some(
        (document) => document.content === content,
      );
    },
    { slug: protectedSlug, content: protectedPendingText },
  );
  await page.locator(".live-connection-status").click();
  await expectCreatorLockControl(page);
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
  await expectLiveConnected(protectedVisitor);
  await assert.equal(
    await liveEditorText(
      protectedVisitor.locator(".live-code-editor .cm-content"),
    ),
    protectedPendingText,
    "a second browser must receive the edit retained through renewal",
  );
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
  await expectLiveConnected(protectedVisitor);
  await protectedContext.close();

  const reconnectContext = await browser.newContext();
  const reconnectCreator = await reconnectContext.newPage();
  const reconnectCreatorSocket =
    await installLiveSocketControl(reconnectCreator);
  await reconnectCreator.goto(webOrigin);
  await reconnectCreator.getByRole("button", { name: "Open LiveBin" }).click();
  await reconnectCreator
    .getByRole("button", { name: "Create", exact: true })
    .click();
  await reconnectCreator.waitForURL((url) => url.pathname.startsWith("/live/"));
  await reconnectCreator.reload();
  await expectLiveConnected(reconnectCreator);
  await reconnectCreator.locator(".live-connection-status").click();
  await expectCreatorLockControl(reconnectCreator);
  await reconnectCreator.locator(".live-connection-status").click();
  await reconnectContext.clearCookies({ name: "oxbin_live_session" });
  await reconnectCreatorSocket.disconnect();
  await expectLiveConnected(reconnectCreator);
  await assert.equal(
    await reconnectCreator.getByText("Room password", { exact: true }).count(),
    0,
    "unprotected reconnect must not show the password gate",
  );
  await reconnectCreator.locator(".live-connection-status").click();
  await expectCreatorLockControl(reconnectCreator);
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
  await owner.getByRole("button", { name: "Create", exact: true }).click();
  await owner.waitForURL((url) => url.pathname.startsWith("/live/"));
  const accessRoomURL = owner.url();
  const accessURL = new URL(accessRoomURL);
  assert.equal(accessURL.search, "", "live room URL must not carry authority");
  assert.equal(accessURL.hash, "", "live room URL must not carry authority");
  await expectLiveConnected(owner);
  await owner.locator(".live-tab-shell.is-active .live-tab-item").click();
  await owner.locator(".live-tab-name-input").fill("tab draft");
  await owner.locator(".live-connection-status").click();
  await owner
    .getByRole("button", { name: "Rename your participant name" })
    .click();
  await owner.getByLabel("Participant name").fill("participant draft");
  await assert.equal(
    await owner
      .locator(".live-tab-shell.is-active")
      .textContent()
      .then((text) => text?.includes("tab draft")),
    true,
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
  await expectLiveConnected(writer);
  await writer.locator(".live-connection-status").click();
  const writerName = await writer
    .getByRole("button", { name: "Rename your participant name" })
    .textContent();
  assert.ok(writerName, "writer should have a participant identity");
  await writer.locator(".live-connection-status").click();

  const viewer = await viewerContext.newPage();
  await viewer.goto(accessRoomURL);
  await expectVisible(viewer, "View only");
  await viewer.waitForFunction(
    () =>
      document.querySelector(".live-add-tab") instanceof HTMLButtonElement &&
      document.querySelector(".live-add-tab").disabled,
  );
  await owner.waitForFunction(() =>
    document
      .querySelector(".live-connection-status")
      ?.getAttribute("aria-label")
      ?.includes("3 participants"),
  );

  const overflow = await overflowContext.newPage();
  await overflow.goto(accessRoomURL);
  await expectVisible(overflow, "Room is full");

  await owner.locator(".live-connection-status").click();
  await owner.getByRole("button", { name: "Lock", exact: true }).click();
  await expectVisible(writer, "Room locked");
  await writer.waitForFunction(
    () =>
      document.querySelector(".live-add-tab") instanceof HTMLButtonElement &&
      document.querySelector(".live-add-tab").disabled,
  );
  await owner.waitForFunction(
    () =>
      document.querySelector(".live-add-tab") instanceof HTMLButtonElement &&
      !document.querySelector(".live-add-tab").disabled,
  );
  assert.equal(
    await owner
      .getByRole("button", { name: `Remove ${writerName}`, exact: true })
      .count(),
    0,
    "live rooms must not expose participant-removal controls",
  );
  await owner.getByRole("button", { name: "Unlock", exact: true }).click();
  await expectVisible(viewer, "View only");
  await writer.waitForFunction(
    () =>
      document.querySelector(".live-add-tab") instanceof HTMLButtonElement &&
      !document.querySelector(".live-add-tab").disabled,
  );

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

async function expectLiveConnected(page) {
  await expectLiveState(page, "Connected");
}

async function expectLiveState(page, state) {
  await page
    .locator(`.live-connection-status[aria-label^="${state}."]`)
    .waitFor({ state: "visible" });
}

async function expectCreatorLockControl(page) {
  await page
    .locator(".live-participant-popover")
    .getByRole("button", { name: /^(Lock|Unlock)$/ })
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
