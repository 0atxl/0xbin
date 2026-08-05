import { useEffect, useState, type ReactNode } from "react";
import {
  createLiveAPI,
  createLiveRoom,
  getLiveRoom,
  LiveAPIError,
  unlockLiveRoom,
  type CreatedLiveRoom,
  type LiveRoomSnapshot,
} from "./live-api";
import { CodeEditor, LanguageMenu } from "./editor";
import {
  blankLiveDraft,
  maxLiveRoomBytes,
  type LiveCreateValidation,
  type LiveDraft,
  validateLiveDraft,
} from "./live";
import { beginLoading } from "./loading";
import { utf8Bytes } from "./create";

export type LiveRouteProps =
  | {
      mode: "create";
      initialDraft: LiveDraft;
      onStatus: (message: string) => void;
      onCreated: (created: CreatedLiveRoom) => Promise<void>;
    }
  | {
      mode: "room";
      slug: string;
      onSecurityGateChange: (open: boolean) => void;
      copyFailed: boolean;
      shareURL?: string;
      onRetryCopy: () => void;
    };

export default function LiveRoute(props: LiveRouteProps) {
  if (props.mode === "create") {
    return <LiveCreateState {...props} />;
  }
  return <LiveRoomPage {...props} />;
}

function LiveCreateState({
  initialDraft,
  onStatus,
  onCreated,
}: Extract<LiveRouteProps, { mode: "create" }>) {
  const [draft, setDraft] = useState(initialDraft);
  const [requirePassword, setRequirePassword] = useState(false);
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<LiveCreateValidation>({});
  const [submitting, setSubmitting] = useState(false);
  const contentBytes = utf8Bytes(draft.document.content);

  function updateDocument(update: Partial<LiveDraft["document"]>) {
    setDraft((current) => ({
      ...current,
      document: { ...current.document, ...update },
    }));
    setErrors((current) => {
      const next = { ...current };
      if ("name" in update) delete next.name;
      if ("content" in update) delete next.content;
      return next;
    });
  }

  async function submit() {
    const nextErrors = validateLiveDraft(draft, requirePassword, password);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    setSubmitting(true);
    const stopLoading = beginLoading();
    try {
      const created = await createLiveRoom(createLiveAPI(), {
        ...(requirePassword ? { password } : {}),
        documents: [draft.document],
      });
      await onCreated(created);
    } catch (error: unknown) {
      onStatus(liveCreateFailureMessage(error));
    } finally {
      stopLoading();
      setSubmitting(false);
    }
  }

  return (
    <form
      className="create-canvas live-create-canvas"
      aria-labelledby="live-create-heading"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <h1 className="sr-only" id="live-create-heading">
        Create LiveBin room
      </h1>
      <div className="metadata-bar live-metadata-bar">
        <label className="live-field">
          <span className="live-field-label">Tab name</span>
          <input
            value={draft.document.name}
            aria-invalid={Boolean(errors.name)}
            onChange={(event) => updateDocument({ name: event.target.value })}
          />
          {errors.name ? (
            <span className="live-field-error" role="alert">
              {errors.name}
            </span>
          ) : null}
        </label>
        <div className="live-language-field">
          <span className="live-field-label">Language</span>
          <LanguageMenu
            value={draft.document.language}
            onChange={(language) => updateDocument({ language })}
          />
        </div>
      </div>

      <div className="live-editor-frame">
        <div className="live-editor-heading">
          <span>Content</span>
          {errors.content ? (
            <span className="live-field-error" role="alert">
              {errors.content}
            </span>
          ) : null}
        </div>
        <CodeEditor
          value={draft.document.content}
          language={draft.document.language}
          ariaLabel="Live room content"
          onChange={(content) => updateDocument({ content })}
          onSubmit={() => void submit()}
        />
      </div>

      <footer className="creation-toolbar live-creation-toolbar">
        <span className="live-expiry" title="Room expires in 24 hours">
          24h
        </span>
        <span
          className={
            contentBytes > maxLiveRoomBytes
              ? "byte-count over-limit"
              : "byte-count"
          }
        >
          {formatBytes(contentBytes)} / 1 MiB
        </span>
        <div className="toolbar-spacer" />
        <label className="encrypt-toggle">
          <input
            type="checkbox"
            checked={requirePassword}
            onChange={(event) => {
              const required = event.target.checked;
              setRequirePassword(required);
              if (!required) {
                setPassword("");
                setErrors((current) => {
                  const next = { ...current };
                  delete next.password;
                  return next;
                });
              }
            }}
          />
          <LockIcon />
          <span>Require password</span>
        </label>
        {requirePassword ? (
          <label className="live-password-field">
            <span>Password</span>
            <input
              type="password"
              value={password}
              aria-invalid={Boolean(errors.password)}
              onChange={(event) => {
                setPassword(event.target.value);
                setErrors((current) => {
                  const next = { ...current };
                  delete next.password;
                  return next;
                });
              }}
            />
            {errors.password ? (
              <span className="live-field-error" role="alert">
                {errors.password}
              </span>
            ) : null}
          </label>
        ) : null}
        <button className="primary-action" type="submit" disabled={submitting}>
          {submitting ? "Creating…" : "Create LiveBin room"}
          <ArrowIcon />
        </button>
      </footer>
    </form>
  );
}

type LiveRoomState =
  "loading" | "password" | "ready" | "unavailable" | "expired" | "error";

function LiveRoomPage({
  slug,
  onSecurityGateChange,
  copyFailed,
  shareURL,
  onRetryCopy,
}: Extract<LiveRouteProps, { mode: "room" }>) {
  const [room, setRoom] = useState<LiveRoomSnapshot>();
  const [state, setState] = useState<LiveRoomState>("loading");
  const [password, setPassword] = useState("");
  const [passwordError, setPasswordError] = useState(false);
  const [unlocking, setUnlocking] = useState(false);

  useEffect(() => {
    onSecurityGateChange(state === "password");
    return () => onSecurityGateChange(false);
  }, [onSecurityGateChange, state]);

  useEffect(() => {
    if (!slug) {
      setState("unavailable");
      return;
    }
    const controller = new AbortController();
    const stopLoading = beginLoading();
    setRoom(undefined);
    setPassword("");
    setPasswordError(false);
    setState("loading");
    getLiveRoom(createLiveAPI(), slug, controller.signal)
      .then((nextRoom) => {
        if (controller.signal.aborted) return;
        setRoom(nextRoom);
        setState("ready");
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (!(error instanceof LiveAPIError)) {
          setState("error");
          return;
        }
        switch (error.code) {
          case "password_required":
            setState("password");
            break;
          case "room_expired":
            setState("expired");
            break;
          case "not_found":
            setState("unavailable");
            break;
          default:
            setState("error");
        }
      })
      .finally(stopLoading);
    return () => {
      controller.abort();
      stopLoading();
    };
  }, [slug]);

  async function unlock() {
    setUnlocking(true);
    setPasswordError(false);
    const stopLoading = beginLoading();
    try {
      const nextRoom = await unlockLiveRoom(createLiveAPI(), slug, password);
      setRoom(nextRoom);
      setState("ready");
    } catch (error: unknown) {
      if (error instanceof LiveAPIError && error.code === "invalid_password") {
        setPasswordError(true);
        return;
      }
      if (error instanceof LiveAPIError && error.code === "room_expired") {
        setState("expired");
        return;
      }
      setState("error");
    } finally {
      stopLoading();
      setUnlocking(false);
    }
  }

  if (state === "loading") {
    return <LoadingAnnouncement label="Loading live room…" />;
  }
  if (state === "password") {
    return (
      <main className="key-gate live-password-gate">
        <form
          className="live-password-form"
          onSubmit={(event) => {
            event.preventDefault();
            void unlock();
          }}
        >
          <label htmlFor="live-room-password">Room password</label>
          <div className="key-entry-form">
            <input
              id="live-room-password"
              type="password"
              autoFocus
              aria-invalid={passwordError}
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
            <button type="submit" disabled={unlocking}>
              Unlock
            </button>
          </div>
          {passwordError ? <p role="alert">Password not accepted.</p> : null}
        </form>
      </main>
    );
  }
  if (state === "unavailable") {
    return (
      <CenteredState
        label="Live room unavailable"
        accentLabel
        detail="It may have expired, been deleted, or never existed."
      />
    );
  }
  if (state === "expired") {
    return (
      <CenteredState
        label="Live room expired"
        accentLabel
        detail="This room is no longer available."
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
  if (!room) return null;

  return (
    <>
      {copyFailed && shareURL ? (
        <button className="copy-link-retry" type="button" onClick={onRetryCopy}>
          LiveBin room created — copy link
        </button>
      ) : null}
      <CenteredState
        label="Live room"
        detail={`Expires ${new Date(room.expiresAt).toLocaleString()}.`}
      />
    </>
  );
}

function LoadingAnnouncement({ label }: { label: string }) {
  return (
    <main className="sr-only" role="status" aria-live="polite">
      {label}
    </main>
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

function liveCreateFailureMessage(error: unknown): string {
  if (!(error instanceof LiveAPIError)) {
    return "Could not create LiveBin room — try again";
  }
  switch (error.code) {
    case "message_too_large":
    case "room_limit_reached":
      return "LiveBin room content is too large";
    case "rate_limited":
      return "Too many requests — try again later";
    case "invalid_request":
      return "Check the LiveBin room details and try again";
    default:
      return "Could not create LiveBin room — try again";
  }
}

function formatBytes(bytes: number): string {
  return bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KiB`;
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
