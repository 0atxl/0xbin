import {
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type PointerEvent,
} from "react";
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
  const [drag, setDrag] = useState<{ source: number; target: number }>();
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
    setDrag({ source: start.source, target: index });
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
      <section
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
          <select
            className="live-pane-document"
            aria-label={`Document in pane ${index + 1}`}
            value={document?.id ?? ""}
            onChange={(event) => onSelect(index, event.target.value)}
          >
            {!document ? (
              <option value="" disabled>
                Choose a document
              </option>
            ) : null}
            {documents.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </select>
          {document ? (
            <select
              className="live-pane-language"
              aria-label={`Language for ${document.name}`}
              value={document.language}
              disabled={structuralDisabled}
              onChange={(event) => onLanguage(document.id, event.target.value)}
            >
              {languages.map(([id, label]) => (
                <option key={id} value={id}>
                  {label}
                </option>
              ))}
            </select>
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
      </section>
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
        onPercentChange={(percent) =>
          setSizes((previous) => ({ ...previous, [sizeKey]: percent }))
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
  onPercentChange,
  children,
}: {
  name: string;
  orientation: "horizontal" | "vertical";
  percent: number;
  onPercentChange: (percent: number) => void;
  children: [ReactNode, ReactNode];
}) {
  const container = useRef<HTMLDivElement>(null);
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
            overshoot={false}
            onSizeChange={(value) => onPercentChange((value / extent) * 100)}
          >
            {children[0]}
          </Panel>
          <Separator
            className="live-editor-separator"
            aria-label={`Resize ${name}`}
          />
          <Panel>{children[1]}</Panel>
        </Group>
      ) : null}
    </div>
  );
}
