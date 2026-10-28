import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type PointerEvent,
} from "react";
import { createPortal } from "react-dom";
import { motion, useReducedMotion } from "motion/react";
import { Group, Panel, Separator } from "motion-panels/react";
import type { LiveRoomDocument } from "./live-api";
import { languages } from "./languages";

export const livePanelLayouts = [
  { id: "columns", label: "Two columns", count: 2 },
  { id: "rows", label: "Two rows", count: 2 },
  { id: "three", label: "Three columns", count: 3 },
  { id: "grid", label: "Four-pane grid", count: 4 },
] as const;
export type LivePanelLayout = (typeof livePanelLayouts)[number]["id"];

export function LivePanels({
  layout,
  resetVersion,
  documents,
  paneIDs,
  activeDocumentID,
  structuralDisabled,
  canAdd,
  onSelect,
  onActivate,
  onSwap,
  onAdd,
  onLanguage,
  renderEditor,
}: {
  layout: LivePanelLayout | "single";
  resetVersion: number;
  documents: LiveRoomDocument[];
  paneIDs: string[];
  activeDocumentID: string;
  structuralDisabled: boolean;
  canAdd: boolean;
  onSelect: (pane: number, documentID: string) => void;
  onActivate: (documentID: string) => void;
  onSwap: (source: number, target: number) => void;
  onAdd: (pane: number) => void;
  onLanguage: (documentID: string, language: string) => void;
  renderEditor: (document: LiveRoomDocument) => ReactNode;
}) {
  // Keep each preset's dimensions when switching layouts or returning to one editor.
  const [sizes, setSizes] = useState<Record<string, number>>({});
  const [drag, setDrag] = useState<{
    source: number;
    target: number;
    x: number;
    y: number;
  }>();
  const reducedMotion = useReducedMotion();
  useLayoutEffect(() => {
    setSizes({});
  }, [resetVersion]);
  const pointer = useRef<
    { id: number; source: number; x: number; y: number } | undefined
  >(undefined);
  const root = useRef<HTMLDivElement>(null);
  const count = livePanelLayouts.find((item) => item.id === layout)?.count ?? 1;

  function swap(source: number, target: number) {
    onSwap(source, target);
    requestAnimationFrame(() => {
      root.current
        ?.querySelector<HTMLButtonElement>(
          `[data-pane-index="${target}"] .live-pane-move`,
        )
        ?.focus();
    });
  }

  function movePointer(event: PointerEvent<HTMLButtonElement>) {
    const start = pointer.current;
    if (!start || start.id !== event.pointerId) return;
    if (Math.hypot(event.clientX - start.x, event.clientY - start.y) < 5)
      return;
    const target = window.document
      .elementFromPoint(event.clientX, event.clientY)
      ?.closest<HTMLElement>("[data-pane-index]");
    const index =
      target && root.current?.contains(target)
        ? Number(target.dataset.paneIndex)
        : start.source;
    setDrag({
      source: start.source,
      target: index,
      x: event.clientX - start.x,
      y: event.clientY - start.y,
    });
  }

  function endPointer(event: PointerEvent<HTMLButtonElement>, commit: boolean) {
    if (pointer.current?.id !== event.pointerId) return;
    if (commit && drag && drag.source !== drag.target)
      swap(drag.source, drag.target);
    pointer.current = undefined;
    setDrag(undefined);
    if (event.currentTarget.hasPointerCapture(event.pointerId))
      event.currentTarget.releasePointerCapture(event.pointerId);
  }

  function pane(index: number) {
    const document = documents.find((item) => item.id === paneIDs[index]);
    return (
      <motion.section
        initial={false}
        animate={{
          x:
            !reducedMotion && drag?.source === index
              ? Math.max(-36, Math.min(36, drag.x * 0.16))
              : 0,
          y:
            !reducedMotion && drag?.source === index
              ? Math.max(-24, Math.min(24, drag.y * 0.16))
              : 0,
          scale: !reducedMotion && drag?.source === index ? 0.985 : 1,
        }}
        transition={
          reducedMotion
            ? { duration: 0 }
            : { type: "spring", stiffness: 360, damping: 28, mass: 0.9 }
        }
        className={`live-editor-pane${document?.id === activeDocumentID ? " is-focused" : ""}${drag?.target === index && drag.source !== index ? " is-drop-target" : ""}${drag?.source === index ? " is-moving" : ""}`}
        aria-label={
          document ? `${document.name} editor` : `Empty pane ${index + 1}`
        }
        data-pane-index={index}
        data-document-id={document?.id}
        onFocusCapture={() => {
          if (document) onActivate(document.id);
        }}
      >
        <div className="live-pane-heading">
          <button
            type="button"
            className="live-pane-move"
            aria-label={`Move pane ${index + 1}`}
            title="Drag to swap panes. Arrow keys move the pane."
            onPointerDown={(event) => {
              if (event.button !== 0) return;
              pointer.current = {
                id: event.pointerId,
                source: index,
                x: event.clientX,
                y: event.clientY,
              };
              event.currentTarget.setPointerCapture(event.pointerId);
            }}
            onPointerMove={movePointer}
            onPointerUp={(event) => endPointer(event, true)}
            onPointerCancel={(event) => endPointer(event, false)}
            onLostPointerCapture={() => {
              pointer.current = undefined;
              setDrag(undefined);
            }}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                pointer.current = undefined;
                setDrag(undefined);
                return;
              }
              const offset =
                event.key === "ArrowLeft"
                  ? -1
                  : event.key === "ArrowRight"
                    ? 1
                    : event.key === "ArrowUp"
                      ? layout === "grid"
                        ? -2
                        : -1
                      : event.key === "ArrowDown"
                        ? layout === "grid"
                          ? 2
                          : 1
                        : 0;
              if (!offset) return;
              event.preventDefault();
              const target = index + offset;
              if (target >= 0 && target < count) swap(index, target);
            }}
          >
            <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
              {[4, 8, 12].map((y) => (
                <g key={y}>
                  <circle cx="5" cy={y} r="1" />
                  <circle cx="10" cy={y} r="1" />
                </g>
              ))}
            </svg>
          </button>
          <PaneMenu
            kind="document"
            label={`Document in pane ${index + 1}`}
            value={document?.id ?? ""}
            placeholder="Choose a document"
            options={documents.map((item) => [item.id, item.name])}
            onChange={(value) => onSelect(index, value)}
          />
          {document ? (
            <PaneMenu
              kind="language"
              label={`Language for ${document.name}`}
              value={document.language}
              options={languages}
              disabled={structuralDisabled}
              onChange={(value) => onLanguage(document.id, value)}
            />
          ) : null}
        </div>
        {document ? (
          renderEditor(document)
        ) : (
          <div className="live-empty-pane">
            <span>Choose a document above or create a new tab.</span>
            <button
              type="button"
              className="live-text-button"
              disabled={!canAdd}
              onClick={() => onAdd(index)}
            >
              New tab
            </button>
          </div>
        )}
      </motion.section>
    );
  }

  function divide(
    key: string,
    orientation: "horizontal" | "vertical",
    first: ReactNode,
    second: ReactNode,
    initial = 50,
  ) {
    const sizeKey = `${layout}-${key}`;
    return (
      <SplitGroup
        key={sizeKey}
        name={key}
        orientation={orientation}
        percent={sizes[sizeKey] ?? initial}
        snapPercent={initial}
        onPercentChange={(percent) =>
          setSizes((previous) => ({
            ...previous,
            [sizeKey]: percent,
          }))
        }
      >
        {[first, second]}
      </SplitGroup>
    );
  }

  const active =
    documents.find((item) => item.id === activeDocumentID) ?? documents[0];
  let content: ReactNode;
  switch (layout) {
    case "columns":
      content = divide("columns", "horizontal", pane(0), pane(1));
      break;
    case "rows":
      content = divide("rows", "vertical", pane(0), pane(1));
      break;
    case "three":
      content = divide(
        "left column",
        "horizontal",
        pane(0),
        divide("right columns", "horizontal", pane(1), pane(2)),
        33.333,
      );
      break;
    case "grid":
      content = divide(
        "grid rows",
        "vertical",
        divide("top columns", "horizontal", pane(0), pane(1)),
        divide("bottom columns", "horizontal", pane(2), pane(3)),
      );
      break;
    default:
      content = active ? (
        <section
          className="live-editor-pane"
          aria-label={`${active.name} editor`}
          data-document-id={active.id}
        >
          {renderEditor(active)}
        </section>
      ) : null;
  }
  return (
    <div
      ref={root}
      className={`live-panel-workspace${drag ? " is-moving-pane" : ""}`}
    >
      {content}
    </div>
  );
}

// Motion Panels needs a nonzero initial size before mounting in reduced-motion
// browsers. Measuring the group first also makes nested presets resize correctly.
function SplitGroup({
  name,
  orientation,
  percent,
  snapPercent,
  onPercentChange,
  children,
}: {
  name: string;
  orientation: "horizontal" | "vertical";
  percent: number;
  snapPercent: number;
  onPercentChange: (percent: number) => void;
  children: [ReactNode, ReactNode];
}) {
  const container = useRef<HTMLDivElement>(null);
  const pointerResize = useRef(false);
  const [extent, setExtent] = useState(0);
  useLayoutEffect(() => {
    const element = container.current;
    if (!element) return;
    const measure = () => {
      const rect = element.getBoundingClientRect();
      setExtent(
        Math.round(orientation === "horizontal" ? rect.width : rect.height),
      );
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [orientation]);
  return (
    <div ref={container} className="live-split-group">
      {extent > 0 ? (
        <Group
          orientation={orientation}
          className="live-editor-panels"
          reorder={false}
        >
          <Panel
            size={Math.round((extent * percent) / 100)}
            minSize="20%"
            maxSize="80%"
            overshoot={18}
            onSizeChange={(value) => {
              const next = (value / extent) * 100;
              // Snap only a released pointer resize. Small keyboard steps must
              // remain free to move away from a preset's resting position.
              const snapped =
                pointerResize.current && Math.abs(next - snapPercent) <= 2;
              pointerResize.current = false;
              onPercentChange(snapped ? snapPercent : next);
            }}
          >
            {children[0]}
          </Panel>
          <Separator
            className="live-editor-separator"
            aria-label={`Resize ${name}`}
            onPointerDownCapture={() => {
              pointerResize.current = true;
            }}
            onPointerCancel={() => {
              pointerResize.current = false;
            }}
            onKeyDownCapture={() => {
              pointerResize.current = false;
            }}
          />
          <Panel>{children[1]}</Panel>
        </Group>
      ) : null}
    </div>
  );
}

// Portals keep compact menus clear of the editor panes' clipping boundaries.
function PaneMenu({
  kind,
  label,
  value,
  placeholder,
  options,
  disabled,
  onChange,
}: {
  kind: "document" | "language";
  label: string;
  value: string;
  placeholder?: string;
  options: readonly (readonly [string, string])[];
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({
    left: 0,
    top: 0,
    width: 140,
    maxHeight: 244,
  });
  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const button = trigger.current;
      if (!button) return;
      const rect = button.getBoundingClientRect();
      const pane = button.closest(".live-editor-pane")?.getBoundingClientRect();
      const left = Math.max(8, (pane?.left ?? 0) + 6);
      const right = Math.min(
        window.innerWidth - 8,
        (pane?.right ?? window.innerWidth) - 6,
      );
      const top = Math.max(8, (pane?.top ?? 0) + 4);
      const bottom = Math.min(
        window.innerHeight - 8,
        (pane?.bottom ?? window.innerHeight) - 4,
      );
      const width = Math.min(kind === "language" ? 140 : 160, right - left);
      const below = Math.max(0, bottom - rect.bottom - 4);
      const above = Math.max(0, rect.top - top - 4);
      const height = Math.min(menu.current?.scrollHeight ?? 244, 244);
      const opensBelow = below >= height || below >= above;
      const maxHeight = Math.min(244, opensBelow ? below : above);
      setPosition({
        width,
        maxHeight,
        left: Math.max(
          left,
          Math.min(
            kind === "language" ? rect.right - width : rect.left,
            right - width,
          ),
        ),
        top: opensBelow
          ? rect.bottom + 4
          : rect.top - Math.min(height, maxHeight) - 4,
      });
    };
    place();
    (
      menu.current?.querySelector<HTMLElement>('[aria-selected="true"]') ??
      menu.current?.querySelector<HTMLElement>('[role="option"]')
    )?.focus();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    const observer = new ResizeObserver(place);
    const pane = trigger.current?.closest(".live-editor-pane");
    if (pane) observer.observe(pane);
    const outside = (event: globalThis.PointerEvent) => {
      if (
        event.target instanceof Node &&
        !menu.current?.contains(event.target) &&
        !trigger.current?.contains(event.target)
      )
        setOpen(false);
    };
    window.document.addEventListener("pointerdown", outside, true);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
      window.document.removeEventListener("pointerdown", outside, true);
    };
  }, [open, kind]);
  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className={`live-pane-select live-pane-${kind}`}
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        data-value={value}
        disabled={disabled}
        onClick={() => setOpen(!open)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            setOpen(true);
          }
        }}
      >
        <span>
          {options.find(([key]) => key === value)?.[1] ?? placeholder}
        </span>
        <svg
          width="12"
          height="12"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          aria-hidden="true"
        >
          <path d="m4 6 4 4 4-4" />
        </svg>
      </button>
      {open
        ? createPortal(
            <div
              ref={menu}
              id={id}
              role="listbox"
              aria-label={label}
              className="live-pane-menu"
              style={position}
              onBlur={(event) => {
                if (!event.currentTarget.contains(event.relatedTarget))
                  setOpen(false);
              }}
              onKeyDown={(event) => {
                if (event.key === "Escape" || event.key === "Tab") {
                  if (event.key === "Escape") event.preventDefault();
                  setOpen(false);
                  trigger.current?.focus();
                  return;
                }
                const items = Array.from(
                  event.currentTarget.querySelectorAll<HTMLButtonElement>(
                    '[role="option"]',
                  ),
                );
                const current = items.indexOf(
                  window.document.activeElement as HTMLButtonElement,
                );
                let next = current;
                if (event.key === "ArrowDown")
                  next = (current + 1) % items.length;
                else if (event.key === "ArrowUp")
                  next = (current - 1 + items.length) % items.length;
                else if (event.key === "Home") next = 0;
                else if (event.key === "End") next = items.length - 1;
                else if (
                  event.key.length === 1 &&
                  !event.ctrlKey &&
                  !event.metaKey &&
                  event.key !== " "
                ) {
                  const match = [
                    ...items.slice(current + 1),
                    ...items.slice(0, current + 1),
                  ].find((item) =>
                    item.textContent
                      ?.toLowerCase()
                      .startsWith(event.key.toLowerCase()),
                  );
                  if (match) {
                    event.preventDefault();
                    match.focus();
                  }
                  return;
                } else return;
                event.preventDefault();
                items[next]?.focus();
              }}
            >
              {options.map(([key, text]) => (
                <button
                  key={key}
                  type="button"
                  role="option"
                  aria-selected={key === value}
                  tabIndex={-1}
                  onClick={() => {
                    setOpen(false);
                    trigger.current?.focus();
                    onChange(key);
                  }}
                >
                  <span>{text}</span>
                  {key === value ? <span aria-hidden="true">✓</span> : null}
                </button>
              ))}
            </div>,
            window.document.body,
          )
        : null}
    </>
  );
}
