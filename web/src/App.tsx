import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Compartment, EditorState, type Extension } from "@codemirror/state";
import { Decoration, EditorView, lineNumbers } from "@codemirror/view";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags } from "@lezer/highlight";
import {
  createPasteAPI,
  consumePaste,
  createEncryptedPaste,
  createPlaintextPaste,
  getPaste,
  PasteAPIError,
  type CreatedPaste,
  type RetrievedEncryptedPaste,
  type RetrievedPaste,
} from "./api";
import type { CreatedLiveRoom } from "./live-api";
import {
  decryptPayload,
  encryptPayload,
  keyFromFragmentOrURL,
  withKeyFragment,
  type PlaintextPayload as EncryptedPlaintextPayload,
} from "./crypto";
import {
  lifetimeRequest,
  maxPasteBytes,
  maxTitleBytes,
  utf8Bytes,
  validateDraft,
  defaultCreateDraft,
  type CreateDraft,
  type Lifetime,
} from "./create";
import { resolveRoute, type Route } from "./router";
import {
  liveDraftFromCreateDraft,
  type LiveDraft,
  blankLiveDraft,
} from "./live";
import { beginLoading } from "./loading";
import { loadTheme, saveTheme, type Theme } from "./theme";
import { loadEditorLanguage } from "./languages";
import { CodeEditor, LanguageMenu } from "./editor";
import { browserShareURL } from "./share-url";
import { findSearchMatches, type SearchMatch } from "./search";
import { isHostedService } from "./hosted";
import { HostedMenu, PolicyPage } from "./policies";
import "./styles.css";

const LiveRoute = lazy(() => import("./live-page"));

const toastDurationMs = 6000;
const themeTransitionMs = 450;

type Toast = {
  id: number;
  message: string;
};

const editorHighlightStyle = HighlightStyle.define([
  {
    tag: [tags.keyword, tags.definitionKeyword, tags.operatorKeyword],
    color: "var(--syntax-keyword)",
    fontWeight: "600",
  },
  {
    tag: [tags.function(tags.variableName), tags.labelName],
    color: "var(--syntax-function)",
  },
  {
    tag: [tags.string, tags.special(tags.string)],
    color: "var(--syntax-string)",
  },
  { tag: [tags.number, tags.bool, tags.null], color: "var(--syntax-number)" },
  { tag: [tags.typeName, tags.className], color: "var(--syntax-type)" },
  { tag: tags.comment, color: "var(--syntax-comment)", fontStyle: "italic" },
]);

function currentRoute(hostedService: boolean): Route {
  return resolveRoute(window.location.pathname, hostedService);
}

export function App() {
  const hostedService = isHostedService(
    window.location.hostname,
    document.documentElement.dataset.hostedService,
  );
  const [route, setRoute] = useState<Route>(() => currentRoute(hostedService));
  const [theme, setTheme] = useState<Theme>(() =>
    loadTheme(
      localStorage,
      window.matchMedia("(prefers-color-scheme: dark)").matches,
    ),
  );
  const [statuses, setStatuses] = useState<Toast[]>([]);
  const [keyGateOpen, setKeyGateOpen] = useState(false);
  const [notificationsPaused, setNotificationsPaused] = useState(false);
  const nextStatusID = useRef(0);
  const themeTransitionTimeout = useRef<number | undefined>(undefined);
  const [shareURL, setShareURL] = useState<string>();
  const [copyFailed, setCopyFailed] = useState(false);
  const [liveDraft, setLiveDraft] = useState<LiveDraft>(() => blankLiveDraft());
  const [liveGateOpen, setLiveGateOpen] = useState(false);
  const createDraftRef = useRef(defaultCreateDraft());

  useEffect(() => {
    const onPopState = () => setRoute(currentRoute(hostedService));
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [hostedService]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    saveTheme(localStorage, theme);
  }, [theme]);

  useEffect(() => {
    if (statuses.length === 0) setNotificationsPaused(false);
  }, [statuses.length]);

  useEffect(
    () => () => {
      if (themeTransitionTimeout.current !== undefined) {
        window.clearTimeout(themeTransitionTimeout.current);
      }
      document.documentElement.classList.remove("theme-transition");
    },
    [],
  );

  function navigate(path: string) {
    setKeyGateOpen(false);
    window.history.pushState({}, "", path);
    setRoute(currentRoute(hostedService));
  }

  function navigateToNewPaste() {
    createDraftRef.current = defaultCreateDraft();
    setLiveDraft(blankLiveDraft());
    navigate("/");
  }

  function handleLiveShare() {
    setCopyFailed(false);
    setShareURL(undefined);
    setLiveDraft(
      route.kind === "create"
        ? liveDraftFromCreateDraft(createDraftRef.current)
        : blankLiveDraft(),
    );
    navigate("/live");
  }

  function showStatus(message: string) {
    nextStatusID.current += 1;
    const id = nextStatusID.current;
    setStatuses((current) => [
      ...current,
      {
        id,
        message,
      },
    ]);
  }

  function dismissStatus(id: number) {
    setStatuses((current) => current.filter((status) => status.id !== id));
  }

  function toggleTheme() {
    document.documentElement.classList.add("theme-transition");
    if (themeTransitionTimeout.current !== undefined) {
      window.clearTimeout(themeTransitionTimeout.current);
    }
    setTheme((current) => (current === "dark" ? "light" : "dark"));
    themeTransitionTimeout.current = window.setTimeout(() => {
      document.documentElement.classList.remove("theme-transition");
      themeTransitionTimeout.current = undefined;
    }, themeTransitionMs);
  }

  async function handleCreated(created: CreatedPaste) {
    const shareURL = browserShareURL(created.url);
    let copied = true;
    try {
      await navigator.clipboard.writeText(shareURL);
    } catch {
      copied = false;
    }
    setShareURL(shareURL);
    setCopyFailed(!copied);
    showStatus(
      copied ? "Link copied" : "Paste created — copy the link manually",
    );
    const destination = new URL(shareURL);
    navigate(destination.pathname + destination.hash);
  }

  async function handleLiveCreated(created: CreatedLiveRoom) {
    const shareURL = browserShareURL(created.url);
    let copied = true;
    try {
      await navigator.clipboard.writeText(shareURL);
    } catch {
      copied = false;
    }
    setShareURL(shareURL);
    setCopyFailed(!copied);
    showStatus(
      copied
        ? "LiveBin room link copied"
        : "LiveBin room created — copy the link manually",
    );
    const destination = new URL(shareURL);
    navigate(destination.pathname + destination.search + destination.hash);
  }

  async function retryCopy() {
    if (!shareURL) return;
    try {
      await navigator.clipboard.writeText(shareURL);
      setCopyFailed(false);
      showStatus("Link copied");
    } catch {
      showStatus("Could not copy the link");
    }
  }

  return (
    <div
      className={
        keyGateOpen || liveGateOpen ? "app-shell key-gate-open" : "app-shell"
      }
    >
      {!keyGateOpen && !liveGateOpen ? (
        <header className="site-header">
          <button
            className="icon-button brand-icon"
            type="button"
            aria-label="0xbin: create a new paste"
            title="New paste"
            onClick={navigateToNewPaste}
          >
            <LogoIcon />
          </button>
          <div className="header-actions">
            {route.kind === "create" || route.kind === "paste" ? (
              <button
                className="header-live-bin"
                type="button"
                aria-label="Open LiveBin"
                title="Open LiveBin"
                onClick={handleLiveShare}
              >
                LiveBin
              </button>
            ) : null}
            <button
              className="icon-button theme-toggle"
              type="button"
              aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
              title={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
              onClick={toggleTheme}
            >
              {theme === "dark" ? <SunIcon /> : <MoonIcon />}
            </button>
          </div>
        </header>
      ) : null}

      {route.kind === "create" ? (
        <CreationCanvas
          onStatus={showStatus}
          onCreated={handleCreated}
          onDraftChange={(draft) => {
            createDraftRef.current = draft;
          }}
        />
      ) : route.kind === "paste" ? (
        <PasteViewer
          slug={route.slug}
          shareURL={shareURL}
          copyFailed={copyFailed}
          onRetryCopy={retryCopy}
          onStatus={showStatus}
          onNewPaste={navigateToNewPaste}
          onKeyGateChange={setKeyGateOpen}
        />
      ) : route.kind === "live-create" ? (
        <Suspense fallback={<LoadingAnnouncement label="Loading LiveBin…" />}>
          <LiveRoute
            mode="create"
            initialDraft={liveDraft}
            onStatus={showStatus}
            onCreated={handleLiveCreated}
          />
        </Suspense>
      ) : route.kind === "live-room" ? (
        <Suspense fallback={<LoadingAnnouncement label="Loading LiveBin…" />}>
          <LiveRoute
            mode="room"
            slug={route.slug}
            onSecurityGateChange={setLiveGateOpen}
            copyFailed={copyFailed}
            shareURL={shareURL}
            onRetryCopy={retryCopy}
          />
        </Suspense>
      ) : (
        <PolicyPage page={route.page} />
      )}

      {hostedService && !keyGateOpen ? (
        <HostedMenu
          currentPage={route.kind === "hosted" ? route.page : undefined}
          onNavigate={navigate}
        />
      ) : null}

      {statuses.length > 0 ? (
        <div
          className="status-stack"
          aria-label="Notifications"
          onMouseEnter={() => setNotificationsPaused(true)}
          onMouseLeave={() => setNotificationsPaused(false)}
          onFocusCapture={() => setNotificationsPaused(true)}
          onBlurCapture={(event) => {
            const next = event.relatedTarget;
            if (
              !(next instanceof Node) ||
              !event.currentTarget.contains(next)
            ) {
              setNotificationsPaused(false);
            }
          }}
        >
          {statuses.map((status) => (
            <StatusToast
              key={status.id}
              message={status.message}
              durationMs={toastDurationMs}
              paused={notificationsPaused}
              onDismiss={() => dismissStatus(status.id)}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function CreationCanvas({
  onStatus,
  onCreated,
  onDraftChange,
}: {
  onStatus: (message: string) => void;
  onCreated: (created: CreatedPaste) => Promise<void>;
  onDraftChange: (draft: CreateDraft) => void;
}) {
  const [draft, setDraft] = useState<CreateDraft>(() => defaultCreateDraft());
  const [errors, setErrors] = useState<ReturnType<typeof validateDraft>>({});
  const [submitting, setSubmitting] = useState(false);
  const contentBytes = utf8Bytes(draft.content);

  function updateDraft(update: Partial<CreateDraft>) {
    const next = { ...draft, ...update };
    setDraft(next);
    onDraftChange(next);
  }

  async function submit() {
    const nextErrors = validateDraft(draft);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) {
      onStatus(
        nextErrors.content === "Paste content is required."
          ? "Empty paste"
          : (Object.values(nextErrors)[0] ?? "Paste details need attention"),
      );
      return;
    }
    const request = lifetimeRequest(draft.lifetime);
    setSubmitting(true);
    try {
      const created = draft.encrypted
        ? await createEncryptedDraft(createPasteAPI(), draft, request)
        : await createPlaintextPaste(createPasteAPI(), {
            title: draft.title,
            language: draft.language,
            content: draft.content,
            expiry: request.expiry,
            burnAfterRead: request.burnAfterRead,
          });
      await onCreated(created);
    } catch (error) {
      onStatus(createFailureMessage(error));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="create-canvas" aria-labelledby="create-heading">
      <h1 className="sr-only" id="create-heading">
        Create a paste
      </h1>
      <div className="metadata-bar">
        <label className="title-field">
          <span className="sr-only">Title, optional</span>
          <input
            value={draft.title}
            maxLength={maxTitleBytes}
            placeholder="Untitled paste"
            aria-invalid={Boolean(errors.title)}
            onChange={(event) => updateDraft({ title: event.target.value })}
          />
        </label>
        <LanguageMenu
          value={draft.language}
          onChange={(language) => updateDraft({ language })}
        />
      </div>

      <CodeEditor
        value={draft.content}
        language={draft.language}
        onChange={(content) => updateDraft({ content })}
        onSubmit={() => void submit()}
      />

      <footer className="creation-toolbar">
        <div className="toolbar-spacer" />
        <span
          className={
            contentBytes > maxPasteBytes
              ? "byte-count over-limit"
              : "byte-count"
          }
        >
          {formatBytes(contentBytes)} / 1 MiB
        </span>
        <fieldset
          className="lifetime-selector"
          data-selected-lifetime={draft.lifetime}
        >
          <legend className="sr-only">Lifetime</legend>
          <span className="lifetime-indicator" aria-hidden="true" />
          <LifetimeButton
            lifetime="once"
            label="Once"
            selected={draft.lifetime}
            onSelect={(lifetime) => {
              updateDraft({ lifetime });
              onStatus("Destroyed after one read.");
            }}
          />
          <LifetimeButton
            lifetime="1h"
            label="1h"
            selected={draft.lifetime}
            onSelect={(lifetime) => updateDraft({ lifetime })}
          />
          <LifetimeButton
            lifetime="24h"
            label="1d"
            selected={draft.lifetime}
            onSelect={(lifetime) => updateDraft({ lifetime })}
          />
          <LifetimeButton
            lifetime="72h"
            label="3d"
            selected={draft.lifetime}
            onSelect={(lifetime) => updateDraft({ lifetime })}
          />
        </fieldset>
        <label className="encrypt-toggle">
          <input
            type="checkbox"
            checked={draft.encrypted}
            onChange={(event) => {
              const encrypted = event.target.checked;
              updateDraft({ encrypted });
              if (encrypted) {
                onStatus("The key stays in the copied link.");
              }
            }}
          />
          <LockIcon />
          <span>Encrypt</span>
        </label>
        <button
          className="primary-action"
          type="button"
          disabled={submitting}
          onClick={() => void submit()}
        >
          {submitting ? "Creating…" : "Create"}
          <ArrowIcon />
        </button>
      </footer>
    </main>
  );
}

async function createEncryptedDraft(
  api: ReturnType<typeof createPasteAPI>,
  draft: CreateDraft,
  request: ReturnType<typeof lifetimeRequest>,
): Promise<CreatedPaste> {
  const encrypted = await encryptPayload({
    version: 1,
    title: draft.title,
    language: draft.language,
    content: draft.content,
  });
  const created = await createEncryptedPaste(api, {
    envelope: encrypted.envelope,
    expiry: request.expiry,
    burnAfterRead: request.burnAfterRead,
  });
  return { ...created, url: withKeyFragment(created.url, encrypted.key) };
}

function LifetimeButton({
  lifetime,
  label,
  selected,
  onSelect,
}: {
  lifetime: Lifetime;
  label: string;
  selected: Lifetime;
  onSelect: (lifetime: Lifetime) => void;
}) {
  return (
    <button
      type="button"
      className={selected === lifetime ? "selected" : undefined}
      aria-pressed={selected === lifetime}
      onClick={() => onSelect(lifetime)}
    >
      {label}
    </button>
  );
}

function PasteViewer({
  slug,
  shareURL,
  copyFailed,
  onRetryCopy,
  onStatus,
  onNewPaste,
  onKeyGateChange,
}: {
  slug: string;
  shareURL?: string;
  copyFailed: boolean;
  onRetryCopy: () => void;
  onStatus: (message: string) => void;
  onNewPaste: () => void;
  onKeyGateChange: (open: boolean) => void;
}) {
  const [paste, setPaste] = useState<
    RetrievedPaste | RetrievedEncryptedPaste
  >();
  const [state, setState] = useState<
    "loading" | "ready" | "key" | "burn" | "unavailable" | "error"
  >("loading");
  const [decryptedPayload, setDecryptedPayload] =
    useState<EncryptedPlaintextPayload>();
  const [keyInput, setKeyInput] = useState("");
  const [keyError, setKeyError] = useState(false);
  const [burnEncrypted, setBurnEncrypted] = useState(false);
  const [consuming, setConsuming] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchClosing, setSearchClosing] = useState(false);
  const [query, setQuery] = useState("");
  const [activeMatch, setActiveMatch] = useState(0);
  const [currentTime, setCurrentTime] = useState(() => Date.now());
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    onKeyGateChange(state === "key");
    return () => onKeyGateChange(false);
  }, [onKeyGateChange, state]);

  useEffect(() => {
    if (state !== "loading") return;
    return beginLoading();
  }, [state]);

  useEffect(() => {
    if (!slug) {
      setState("unavailable");
      return;
    }
    const controller = new AbortController();
    setPaste(undefined);
    setDecryptedPayload(undefined);
    setBurnEncrypted(false);
    setKeyInput("");
    setState("loading");
    getPaste(createPasteAPI(), slug, controller.signal)
      .then((result) => {
        if (controller.signal.aborted) return;
        if ("burnAfterRead" in result) {
          setBurnEncrypted(result.isEncrypted);
          setState("burn");
          return;
        }
        setPaste(result);
        if (!("envelope" in result)) {
          setState("ready");
          return;
        }
        try {
          const key = keyFromFragmentOrURL(window.location.hash);
          void decryptPayload(result.envelope, key)
            .then((payload) => {
              if (controller.signal.aborted) return;
              setDecryptedPayload(payload);
              setState("ready");
            })
            .catch(() => {
              if (!controller.signal.aborted) setState("key");
            });
        } catch {
          if (!controller.signal.aborted) setState("key");
        }
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setState(
          error instanceof PasteAPIError && error.code === "not_found"
            ? "unavailable"
            : "error",
        );
      });
    return () => controller.abort();
  }, [slug]);

  useEffect(() => {
    const openSearch = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "f") {
        event.preventDefault();
        focusSearch();
      }
    };
    window.addEventListener("keydown", openSearch);
    return () => window.removeEventListener("keydown", openSearch);
  }, []);

  useEffect(() => {
    if (!searchOpen) return;
    searchRef.current?.focus();
  }, [searchOpen]);

  useEffect(() => {
    if (!paste || !isOneHourPaste(paste)) return;
    const timer = window.setInterval(() => setCurrentTime(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [paste]);

  const payload =
    paste && "payload" in paste ? paste.payload : decryptedPayload;
  const matches = useMemo(
    () => (payload ? findSearchMatches(payload.content, query) : []),
    [payload, query],
  );
  const matchCount = matches.length;
  const visibleActiveMatch =
    matchCount === 0 ? 0 : Math.min(activeMatch, matchCount - 1);
  const oneHourPaste = paste ? isOneHourPaste(paste) : false;
  const expiryCountdown =
    paste && oneHourPaste
      ? formatExpiryCountdown(paste.expiresAt, currentTime)
      : undefined;
  const expiryLastMinute =
    paste && oneHourPaste
      ? (() => {
          const remainingMs = Date.parse(paste.expiresAt) - currentTime;
          return remainingMs > 0 && remainingMs <= 60 * 1_000;
        })()
      : false;

  function focusSearch() {
    setSearchOpen(true);
    window.setTimeout(() => searchRef.current?.focus(), 0);
  }

  function toggleSearch() {
    if (searchClosing) {
      setSearchClosing(false);
      setSearchOpen(true);
      return;
    }
    if (searchOpen) return closeSearch();
    focusSearch();
  }

  function closeSearch() {
    setQuery("");
    setSearchOpen(false);
    setSearchClosing(true);
    window.setTimeout(() => setSearchClosing(false), 140);
  }

  async function copyContent() {
    const payload =
      paste && "payload" in paste ? paste.payload : decryptedPayload;
    if (!payload) return;
    try {
      await navigator.clipboard.writeText(payload.content);
      onStatus("Paste copied");
    } catch {
      onStatus("Could not copy paste");
    }
  }

  function downloadContent() {
    const payload =
      paste && "payload" in paste ? paste.payload : decryptedPayload;
    if (!paste || !payload) return;
    const blob = new Blob([payload.content], {
      type: "text/plain;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = safeFilename(payload.title, paste.slug);
    link.click();
    URL.revokeObjectURL(url);
  }

  async function revealAndDestroy() {
    let key: string | undefined;
    if (burnEncrypted) {
      try {
        key = keyFromFragmentOrURL(keyInput || window.location.hash);
      } catch {
        setKeyError(true);
        return;
      }
    }
    setConsuming(true);
    try {
      const result = await consumePaste(createPasteAPI(), slug);
      setPaste(result);
      if ("envelope" in result) {
        const payload = await decryptPayload(result.envelope, key!);
        setDecryptedPayload(payload);
      }
      setState("ready");
    } catch (error) {
      setState(
        error instanceof PasteAPIError && error.code === "not_found"
          ? "unavailable"
          : "error",
      );
    } finally {
      setConsuming(false);
    }
  }

  if (state === "loading") {
    return <LoadingAnnouncement label="Loading paste…" />;
  }
  if (state === "unavailable") {
    return (
      <CenteredState
        label="Paste unavailable"
        accentLabel
        detail="It may have expired, been consumed, been deleted, or never existed."
        action={
          <button type="button" onClick={onNewPaste}>
            Create new paste
          </button>
        }
      />
    );
  }
  if (state === "error") {
    return (
      <CenteredState
        label="Service unavailable"
        detail="Try again in a moment."
      />
    );
  }
  if (state === "burn") {
    return (
      <main className="centered-state">
        <h1>View-once paste</h1>
        <p>Opening this paste will permanently destroy the server copy.</p>
        {burnEncrypted ? (
          <>
            <p>0xbin cannot verify this key before the paste is consumed.</p>
            <label className="sr-only" htmlFor="burn-decryption-key">
              Paste decryption key
            </label>
            <input
              id="burn-decryption-key"
              value={keyInput}
              onChange={(event) => setKeyInput(event.target.value)}
            />
            {keyError ? (
              <p role="alert">Unable to decrypt — check the key.</p>
            ) : null}
          </>
        ) : null}
        <button
          type="button"
          disabled={consuming}
          onClick={() => void revealAndDestroy()}
        >
          {consuming ? "Revealing…" : "Reveal and destroy"}
        </button>
      </main>
    );
  }
  if (state === "key") {
    return (
      <main className="key-gate">
        <form
          className="key-entry-form"
          onSubmit={(event) => {
            event.preventDefault();
            if (!paste || !("envelope" in paste)) return;
            let key: string;
            try {
              key = keyFromFragmentOrURL(keyInput);
            } catch {
              setKeyError(true);
              onStatus("Unable to decrypt — check the key.");
              return;
            }
            setKeyError(false);
            setState("loading");
            void decryptPayload(paste.envelope, key)
              .then((payload) => {
                setDecryptedPayload(payload);
                setState("ready");
              })
              .catch(() => {
                setKeyError(true);
                setState("key");
                onStatus("Unable to decrypt — check the key.");
              });
          }}
        >
          <label className="sr-only" htmlFor="decryption-key">
            Paste decryption key
          </label>
          <input
            id="decryption-key"
            aria-invalid={keyError}
            value={keyInput}
            placeholder="Decryption key here"
            onChange={(event) => setKeyInput(event.target.value)}
          />
          <button type="submit" aria-label="Decrypt" title="Decrypt">
            <ArrowIcon />
          </button>
        </form>
      </main>
    );
  }
  if (!paste) return null;
  if (!payload) return <CenteredState label="Decrypting…" />;

  return (
    <main className="viewer-canvas" aria-labelledby="viewer-heading">
      <header className="viewer-toolbar">
        <div className="viewer-identity">
          {payload.title ? (
            <h1 id="viewer-heading">{payload.title}</h1>
          ) : (
            <h1 className="sr-only" id="viewer-heading">
              Paste
            </h1>
          )}
        </div>
        {expiryCountdown ? (
          <span className="viewer-expiry-row">
            <FlipCountdown value={expiryCountdown} urgent={expiryLastMinute} />
          </span>
        ) : null}
        <div className="viewer-actions" aria-label="Paste actions">
          {searchOpen || searchClosing ? (
            <div
              className={
                searchClosing
                  ? "viewer-search-row is-closing"
                  : "viewer-search-row"
              }
            >
              <div className="search-control">
                <div className="search-input-cell">
                  <input
                    ref={searchRef}
                    className={query ? "has-query" : undefined}
                    type="search"
                    value={query}
                    placeholder="Find"
                    aria-label="Search paste"
                    onChange={(event) => {
                      setActiveMatch(0);
                      setQuery(event.target.value);
                    }}
                    onKeyDown={(event) => {
                      if (event.key === "Escape") {
                        closeSearch();
                      }
                    }}
                  />
                  {query ? (
                    <button
                      className="search-clear"
                      type="button"
                      aria-label="Clear search"
                      title="Clear search"
                      onClick={() => {
                        setActiveMatch(0);
                        setQuery("");
                        searchRef.current?.focus();
                      }}
                    >
                      ×
                    </button>
                  ) : null}
                </div>
                <span className="search-count" aria-live="polite">
                  {matchCount > 0
                    ? `${visibleActiveMatch + 1} / ${matchCount}`
                    : "0 / 0"}
                </span>
                <div className="search-navigation">
                  <ActionButton
                    label="Previous match"
                    disabled={matchCount === 0}
                    onClick={() =>
                      setActiveMatch((current) =>
                        current === 0 ? matchCount - 1 : current - 1,
                      )
                    }
                  >
                    <PreviousIcon />
                  </ActionButton>
                  <ActionButton
                    label="Next match"
                    disabled={matchCount === 0}
                    onClick={() =>
                      setActiveMatch((current) => (current + 1) % matchCount)
                    }
                  >
                    <NextIcon />
                  </ActionButton>
                </div>
              </div>
            </div>
          ) : null}
          <div className="viewer-action-icons">
            <ActionButton label="Search" onClick={toggleSearch}>
              <SearchIcon />
            </ActionButton>
            <ActionButton label="Copy" onClick={() => void copyContent()}>
              <CopyIcon />
            </ActionButton>
            {"payload" in paste ? (
              <a
                className="action-button"
                href={`/api/v1/pastes/${encodeURIComponent(slug)}/raw`}
                target="_blank"
                rel="noreferrer"
                aria-label="Open raw paste"
                title="Raw"
              >
                <RawIcon />
              </a>
            ) : null}
            <ActionButton label="Download" onClick={downloadContent}>
              <DownloadIcon />
            </ActionButton>
            <ActionButton label="Create new paste" onClick={onNewPaste}>
              <PlusIcon />
            </ActionButton>
          </div>
        </div>
      </header>

      {copyFailed && shareURL ? (
        <button className="copy-link-retry" type="button" onClick={onRetryCopy}>
          Paste created — copy link
        </button>
      ) : null}

      <div className="paste-content">
        <ReadonlyPasteViewer
          content={payload.content}
          language={payload.language}
          matches={matches}
          activeMatch={visibleActiveMatch}
        />
      </div>
    </main>
  );
}

function LoadingAnnouncement({ label }: { label: string }) {
  return (
    <main className="sr-only" role="status" aria-live="polite">
      {label}
    </main>
  );
}

function ReadonlyPasteViewer({
  content,
  language,
  matches,
  activeMatch,
}: {
  content: string;
  language: string;
  matches: SearchMatch[];
  activeMatch: number;
}) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | undefined>(undefined);
  const languageConfig = useRef(new Compartment());
  const searchMatchesConfig = useRef(new Compartment());
  const activeSearchMatchConfig = useRef(new Compartment());
  const previousMatches = useRef<SearchMatch[] | undefined>(undefined);

  useEffect(() => {
    if (!host.current) return;
    const editor = new EditorView({
      state: EditorState.create({
        doc: content,
        extensions: [
          lineNumbers(),
          EditorState.readOnly.of(true),
          EditorView.editable.of(false),
          EditorView.contentAttributes.of({
            "aria-label": "Paste content",
            tabindex: "0",
          }),
          languageConfig.current.of([]),
          EditorView.lineWrapping,
          searchMatchesConfig.current.of(searchHighlights(matches)),
          activeSearchMatchConfig.current.of(
            activeSearchHighlight(matches[activeMatch]),
          ),
          syntaxHighlighting(editorHighlightStyle, { fallback: true }),
        ],
      }),
      parent: host.current,
    });
    view.current = editor;
    return () => {
      view.current = undefined;
      editor.destroy();
    };
  }, [content]);

  useEffect(() => {
    let active = true;
    void loadEditorLanguage(language)
      .then((extension) => {
        if (!active || !view.current) return;
        view.current.dispatch({
          effects: languageConfig.current.reconfigure(extension),
        });
      })
      .catch(() => {
        if (!active || !view.current) return;
        view.current.dispatch({
          effects: languageConfig.current.reconfigure([]),
        });
      });
    return () => {
      active = false;
    };
  }, [language]);

  useEffect(() => {
    if (!view.current) return;
    view.current.dispatch({
      effects: searchMatchesConfig.current.reconfigure(
        searchHighlights(matches),
      ),
    });
  }, [matches]);

  useEffect(() => {
    if (!view.current) return;
    const match = matches[activeMatch];
    const shouldScroll = previousMatches.current === matches;
    view.current.dispatch({
      effects: activeSearchMatchConfig.current.reconfigure(
        activeSearchHighlight(match),
      ),
      selection: match ? { anchor: match.from, head: match.to } : undefined,
      scrollIntoView: shouldScroll && Boolean(match),
    });
    previousMatches.current = matches;
  }, [activeMatch, matches]);

  return <div className="readonly-paste-editor" ref={host} />;
}

function isOneHourPaste(
  paste: RetrievedPaste | RetrievedEncryptedPaste,
): boolean {
  const createdAt = Date.parse(paste.createdAt);
  const expiresAt = Date.parse(paste.expiresAt);
  const lifetime = expiresAt - createdAt;
  return lifetime >= 59 * 60 * 1000 && lifetime <= 61 * 60 * 1000;
}

function formatExpiryCountdown(expiresAt: string, currentTime: number): string {
  const remainingSeconds = Math.max(
    0,
    Math.ceil((Date.parse(expiresAt) - currentTime) / 1_000),
  );
  const minutes = Math.floor(remainingSeconds / 60);
  const seconds = remainingSeconds % 60;
  return `${minutes}:${seconds.toString().padStart(2, "0")}`;
}

function FlipCountdown({ value, urgent }: { value: string; urgent: boolean }) {
  const [minutes = "00", seconds = "00"] = value.split(":");
  const minuteDigits = minutes.padStart(2, "0").slice(-2).split("");
  const secondDigits = seconds.padStart(2, "0").slice(-2).split("");
  return (
    <span
      className={urgent ? "flip-clock expiry-last-minute" : "flip-clock"}
      role="timer"
      aria-label={`Expires in ${Number(minutes)} minutes ${Number(seconds)} seconds`}
    >
      <span className="flip-digit-group" aria-hidden="true">
        {minuteDigits.map((digit, index) => (
          <FlipDigit digit={digit} key={`minute-${index}-${digit}`} />
        ))}
      </span>
      <span className="flip-colon" aria-hidden="true">
        :
      </span>
      <span className="flip-digit-group" aria-hidden="true">
        {secondDigits.map((digit, index) => (
          <FlipDigit digit={digit} key={`second-${index}-${digit}`} />
        ))}
      </span>
    </span>
  );
}

function FlipDigit({ digit }: { digit: string }) {
  return <span className="flip-digit">{digit}</span>;
}

function searchHighlights(matches: SearchMatch[]): Extension {
  if (matches.length === 0) return [];
  return EditorView.decorations.of(
    Decoration.set(
      matches.map((match) =>
        Decoration.mark({ class: "cm-search-match" }).range(
          match.from,
          match.to,
        ),
      ),
      true,
    ),
  );
}

function activeSearchHighlight(match: SearchMatch | undefined): Extension {
  if (!match) return [];
  return EditorView.decorations.of(
    Decoration.set(
      [
        Decoration.mark({
          class: "cm-search-match cm-search-match-active",
        }).range(match.from, match.to),
      ],
      true,
    ),
  );
}

function ActionButton({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      className="action-button"
      type="button"
      disabled={disabled}
      aria-label={label}
      title={label}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

function CenteredState({
  label,
  detail,
  action,
  accentLabel = false,
}: {
  label: string;
  detail?: string;
  action?: ReactNode;
  accentLabel?: boolean;
}) {
  return (
    <main className="centered-state">
      <h1 className={accentLabel ? "accent-label" : undefined}>{label}</h1>
      {detail ? <p>{detail}</p> : null}
      {action}
    </main>
  );
}

function StatusToast({
  message,
  durationMs,
  paused,
  onDismiss,
}: {
  message: string;
  durationMs: number;
  paused: boolean;
  onDismiss: () => void;
}) {
  const remainingMs = useRef(durationMs);

  useEffect(() => {
    if (paused) return;
    const startedAt = performance.now();
    const timeout = window.setTimeout(onDismiss, remainingMs.current);
    return () => {
      window.clearTimeout(timeout);
      remainingMs.current = Math.max(
        0,
        remainingMs.current - (performance.now() - startedAt),
      );
    };
  }, [onDismiss, paused]);

  return (
    <div
      className="status-toast"
      role="status"
      aria-live="polite"
      style={{ "--toast-duration": `${durationMs}ms` } as React.CSSProperties}
    >
      <span className="toast-message" title={message}>
        {message}
      </span>
      <button
        className="toast-close"
        type="button"
        aria-label="Dismiss notification"
        title="Dismiss"
        onClick={onDismiss}
      >
        ×
      </button>
      <span
        className={paused ? "toast-timer paused" : "toast-timer"}
        aria-hidden="true"
      />
    </div>
  );
}

function createFailureMessage(error: unknown): string {
  if (!(error instanceof PasteAPIError))
    return "Could not create paste — try again";
  switch (error.code) {
    case "payload_too_large":
      return "Paste is too large";
    case "rate_limited":
      return "Too many requests — try again later";
    case "invalid_request":
      return "Check the paste details and try again";
    default:
      return "Could not create paste — try again";
  }
}

function formatBytes(bytes: number): string {
  return bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KiB`;
}

function safeFilename(title: string, slug: string): string {
  const base = title
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 60);
  return `${base || slug}.txt`;
}

function LogoIcon() {
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true">
      <path d="M7 8h18M12 8V5.5h8V8M9.5 8l1.2 17.5h10.6L22.5 8M13.5 13l5 7M18.5 13l-5 7" />
    </svg>
  );
}
function SunIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="12" cy="12" r="3.5" />
      <path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M19.1 4.9l-1.4 1.4M6.3 17.7l-1.4 1.4" />
    </svg>
  );
}
function MoonIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M20 15.1A8.4 8.4 0 0 1 8.9 4a8.5 8.5 0 1 0 11.1 11.1Z" />
    </svg>
  );
}
function ChevronIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="m6 8 4 4 4-4" />
    </svg>
  );
}
function CheckIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="m4 10 4 4 8-8" />
    </svg>
  );
}
function CodeIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="m7 5-5 5 5 5M13 5l5 5-5 5" />
    </svg>
  );
}
function LockIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <rect x="4" y="8" width="12" height="9" rx="2" />
      <path d="M7 8V6a3 3 0 0 1 6 0v2" />
    </svg>
  );
}
function ArrowIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="M3 10h13M11 5l5 5-5 5" />
    </svg>
  );
}
function SearchIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <circle cx="8.5" cy="8.5" r="5.5" />
      <path d="m13 13 4 4" />
    </svg>
  );
}

function PreviousIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="m13 5-5 5 5 5" />
    </svg>
  );
}

function NextIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="m7 5 5 5-5 5" />
    </svg>
  );
}
function CopyIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <rect x="6" y="6" width="10" height="10" rx="1.5" />
      <path d="M14 6V4.5A1.5 1.5 0 0 0 12.5 3h-8A1.5 1.5 0 0 0 3 4.5v8A1.5 1.5 0 0 0 4.5 14H6" />
    </svg>
  );
}
function RawIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="m7 5-4 5 4 5M13 5l4 5-4 5" />
    </svg>
  );
}
function DownloadIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="M10 3v10M6 9l4 4 4-4M3 17h14" />
    </svg>
  );
}
function PlusIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="M10 3v14M3 10h14" />
    </svg>
  );
}
