import { useEffect, useRef } from "react";
import {
  Compartment,
  EditorSelection,
  EditorState,
  StateEffect,
  StateField,
  type ChangeDesc,
} from "@codemirror/state";
import { collab, getSyncedVersion, sendableUpdates } from "@codemirror/collab";
import {
  defaultKeymap,
  historyKeymap,
  indentLess,
  insertTab,
} from "@codemirror/commands";
import {
  Decoration,
  EditorView,
  keymap,
  layer,
  lineNumbers,
  placeholder,
  RectangleMarker,
  type DecorationSet,
  type LayerMarker,
  type ViewUpdate,
} from "@codemirror/view";
import {
  HighlightStyle,
  indentOnInput,
  indentUnit,
  syntaxHighlighting,
} from "@codemirror/language";
import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { tags } from "@lezer/highlight";
import type { LiveRoomDocument } from "./live-api";
import { editorHistoryExtensions } from "./editor-history";
import { loadEditorLanguage, editorIndentColumns } from "./languages";

const liveHighlightStyle = HighlightStyle.define([
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

export type RemoteCursor = {
  id: string;
  nickname: string;
  color: string;
  revision: number;
  anchor: number;
  head: number;
  active: boolean;
};

const setRemoteCursors = StateEffect.define<RemoteCursor[]>();

type RemoteCursorState = {
  sourceCursors: RemoteCursor[];
  cursors: RemoteCursor[];
  selections: DecorationSet;
};

class RemoteCursorMarker implements LayerMarker {
  constructor(
    readonly left: number,
    readonly top: number,
    readonly height: number,
    readonly cursors: RemoteCursor[],
  ) {}

  eq(other: RemoteCursorMarker) {
    return (
      this.left === other.left &&
      this.top === other.top &&
      this.height === other.height &&
      sameRemoteCursorPresentation(this.cursors, other.cursors)
    );
  }

  draw() {
    const element = document.createElement("div");
    element.className = "live-remote-caret";
    element.setAttribute("aria-hidden", "true");
    this.position(element);

    const labels = document.createElement("div");
    labels.className = "live-remote-labels";
    this.cursors.forEach((cursor, index) => {
      const color = safeParticipantColor(cursor.color);
      const caret = document.createElement("span");
      caret.className = "live-remote-caret-line";
      caret.style.setProperty("--remote-color", color);
      caret.style.setProperty("--remote-caret-index", String(index));
      element.append(caret);

      const label = document.createElement("span");
      label.className = `live-remote-label${cursor.active ? " is-active" : ""}`;
      label.style.setProperty("--remote-color", color);
      label.textContent = cursor.nickname;
      labels.append(label);
    });
    element.append(labels);
    return element;
  }

  update(element: HTMLElement, previous: RemoteCursorMarker) {
    if (!sameRemoteCursorPresentation(this.cursors, previous.cursors)) {
      return false;
    }
    this.position(element);
    return true;
  }

  private position(element: HTMLElement) {
    element.style.left = `${this.left}px`;
    element.style.top = `${this.top}px`;
    element.style.height = `${this.height}px`;
  }
}

function sameRemoteCursorPresentation(
  left: RemoteCursor[],
  right: RemoteCursor[],
): boolean {
  return (
    left.length === right.length &&
    left.every((cursor, index) => {
      const candidate = right[index];
      return (
        cursor.id === candidate.id &&
        cursor.nickname === candidate.nickname &&
        cursor.color === candidate.color &&
        cursor.active === candidate.active
      );
    })
  );
}

function remoteSelectionDecorations(cursors: RemoteCursor[]): DecorationSet {
  return Decoration.set(
    cursors.flatMap((cursor) => {
      const from = Math.min(cursor.anchor, cursor.head);
      const to = Math.max(cursor.anchor, cursor.head);
      return from === to
        ? []
        : [
            Decoration.mark({
              class: "live-remote-selection",
              attributes: {
                style: `--remote-color: ${safeParticipantColor(cursor.color)}`,
              },
            }).range(from, to),
          ];
    }),
    true,
  );
}

export function mapRemoteCursors(
  cursors: RemoteCursor[],
  changes: ChangeDesc,
  documentLength: number,
): RemoteCursor[] {
  return cursors.map((cursor) => ({
    ...cursor,
    anchor: clampPosition(
      changes.mapPos(cursor.anchor, cursor.anchor < cursor.head ? -1 : 1),
      documentLength,
    ),
    head: clampPosition(
      changes.mapPos(cursor.head, cursor.head < cursor.anchor ? -1 : 1),
      documentLength,
    ),
  }));
}

export function livePresenceSelection(state: EditorState): {
  revision: number;
  anchor: number;
  head: number;
} {
  const selection = state.selection.main;
  const pendingChanges = pendingChangeDescription(state);
  if (!pendingChanges) {
    return {
      revision: getSyncedVersion(state),
      anchor: selection.anchor,
      head: selection.head,
    };
  }
  const inverse = pendingChanges.invertedDesc;
  return {
    revision: getSyncedVersion(state),
    anchor: inverse.mapPos(
      selection.anchor,
      selection.anchor < selection.head ? -1 : 1,
    ),
    head: inverse.mapPos(
      selection.head,
      selection.head < selection.anchor ? -1 : 1,
    ),
  };
}

function pendingChangeDescription(state: EditorState): ChangeDesc | undefined {
  let pendingChanges: ChangeDesc | undefined;
  for (const update of sendableUpdates(state)) {
    pendingChanges = pendingChanges
      ? pendingChanges.composeDesc(update.changes)
      : update.changes;
  }
  return pendingChanges;
}

export function reconcileRemoteCursors(
  sourceCursors: RemoteCursor[],
  existingCursors: RemoteCursor[],
  state: EditorState,
): RemoteCursor[] {
  const syncedRevision = getSyncedVersion(state);
  const pendingChanges = pendingChangeDescription(state);
  const existingByID = new Map(
    existingCursors.map((cursor) => [cursor.id, cursor]),
  );
  return sourceCursors.flatMap((source) => {
    if (source.revision !== syncedRevision) {
      const existing = existingByID.get(source.id);
      return existing
        ? [
            {
              ...existing,
              nickname: source.nickname,
              color: source.color,
              active: source.active,
            },
          ]
        : [];
    }
    return pendingChanges
      ? mapRemoteCursors([source], pendingChanges, state.doc.length)
      : [
          {
            ...source,
            anchor: clampPosition(source.anchor, state.doc.length),
            head: clampPosition(source.head, state.doc.length),
          },
        ];
  });
}

const remoteCursorField = StateField.define<RemoteCursorState>({
  create: () => ({
    sourceCursors: [],
    cursors: [],
    selections: Decoration.none,
  }),
  update(value, transaction) {
    let sourceCursors = value.sourceCursors;
    let sourceChanged = false;
    for (const effect of transaction.effects) {
      if (!effect.is(setRemoteCursors)) continue;
      sourceCursors = effect.value;
      sourceChanged = true;
    }
    const startRevision = getSyncedVersion(transaction.startState);
    const syncedRevision = getSyncedVersion(transaction.state);
    if (
      !transaction.docChanged &&
      !sourceChanged &&
      startRevision === syncedRevision
    ) {
      return value;
    }
    let existingCursors = transaction.docChanged
      ? mapRemoteCursors(
          value.cursors,
          transaction.changes,
          transaction.state.doc.length,
        )
      : value.cursors;
    if (startRevision !== syncedRevision) {
      existingCursors = existingCursors.map((cursor) => ({
        ...cursor,
        revision: syncedRevision,
      }));
    }
    const cursors = reconcileRemoteCursors(
      sourceCursors,
      existingCursors,
      transaction.state,
    );
    return {
      sourceCursors,
      cursors,
      selections: remoteSelectionDecorations(cursors),
    };
  },
  provide: (field) =>
    EditorView.decorations.from(field, (value) => value.selections),
});

const remoteCursorLayer = layer({
  above: true,
  class: "live-remote-layer",
  update(update: ViewUpdate) {
    return (
      update.startState.field(remoteCursorField) !==
      update.state.field(remoteCursorField)
    );
  },
  markers(view) {
    const groups = new Map<number, RemoteCursor[]>();
    for (const cursor of view.state.field(remoteCursorField).cursors) {
      const group = groups.get(cursor.head) ?? [];
      group.push(cursor);
      groups.set(cursor.head, group);
    }
    return [...groups.entries()].flatMap(([head, cursors]) => {
      const orderedCursors = [...cursors].sort((left, right) =>
        left.id.localeCompare(right.id),
      );
      const side = orderedCursors.every((cursor) => cursor.head < cursor.anchor)
        ? -1
        : 1;
      const rectangle = RectangleMarker.forRange(
        view,
        "live-remote-caret-position",
        EditorSelection.cursor(head, side),
      )[0];
      return rectangle
        ? [
            new RemoteCursorMarker(
              rectangle.left,
              rectangle.top,
              rectangle.height,
              orderedCursors,
            ),
          ]
        : [];
    });
  },
});

export function safeParticipantColor(color: string): string {
  return /^#[0-9a-f]{6}$/i.test(color) ? color : "var(--accent)";
}

function clampPosition(position: number, length: number): number {
  return Math.max(
    0,
    Math.min(Number.isFinite(position) ? position : 0, length),
  );
}

export function makeLiveEditorState(
  document: LiveRoomDocument,
  clientID: string,
): EditorState {
  return EditorState.create({
    doc: document.content,
    extensions: [
      collab({ startVersion: document.revision, clientID }),
      remoteCursorField,
      remoteCursorLayer,
      lineNumbers(),
      EditorView.lineWrapping,
      placeholder("Write text or code here…"),
      closeBrackets(),
      indentOnInput(),
      editorHistoryExtensions,
      syntaxHighlighting(liveHighlightStyle, { fallback: true }),
      EditorState.tabSize.of(editorIndentColumns),
      indentUnit.of(" ".repeat(editorIndentColumns)),
      EditorView.contentAttributes.of({ "aria-label": "Live room content" }),
      keymap.of([
        { key: "Tab", run: insertTab },
        { key: "Shift-Tab", run: indentLess },
        ...historyKeymap,
        ...closeBracketsKeymap,
        ...defaultKeymap,
      ]),
    ],
  });
}

export function LiveCollaborativeEditor({
  state,
  language,
  readOnly,
  remoteCursors,
  onStateChange,
  onSelectionChange,
  onViewReady,
}: {
  state: EditorState;
  language: string;
  readOnly: boolean;
  remoteCursors: RemoteCursor[];
  onStateChange: (state: EditorState) => void;
  onSelectionChange: (state: EditorState) => void;
  onViewReady: (view: EditorView | undefined) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | undefined>(undefined);
  const languageCompartment = useRef(new Compartment());
  const readOnlyCompartment = useRef(new Compartment());
  const stateChangeRef = useRef(onStateChange);
  const selectionChangeRef = useRef(onSelectionChange);
  const viewReadyRef = useRef(onViewReady);
  stateChangeRef.current = onStateChange;
  selectionChangeRef.current = onSelectionChange;
  viewReadyRef.current = onViewReady;

  useEffect(() => {
    if (!host.current) return;
    const view = new EditorView({
      state: state.update({
        effects: StateEffect.appendConfig.of([
          languageCompartment.current.of([]),
          readOnlyCompartment.current.of(EditorState.readOnly.of(readOnly)),
        ]),
      }).state,
      parent: host.current,
      dispatchTransactions: (transactions, currentView) => {
        currentView.update(transactions);
        stateChangeRef.current(currentView.state);
        if (
          transactions.some(
            (transaction) => transaction.selection || transaction.docChanged,
          )
        ) {
          selectionChangeRef.current(currentView.state);
        }
      },
    });
    viewRef.current = view;
    stateChangeRef.current(view.state);
    viewReadyRef.current(view);
    return () => {
      stateChangeRef.current(view.state);
      viewReadyRef.current(undefined);
      view.destroy();
      viewRef.current = undefined;
    };
    // The document state is deliberately only consumed when this tab mounts.
    // Subsequent local and remote updates are dispatched to the mounted view.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    view.dispatch({
      effects: readOnlyCompartment.current.reconfigure(
        EditorState.readOnly.of(readOnly),
      ),
    });
  }, [readOnly]);

  useEffect(() => {
    let active = true;
    void loadEditorLanguage(language).then((extension) => {
      if (!active || !viewRef.current) return;
      viewRef.current.dispatch({
        effects: languageCompartment.current.reconfigure(extension),
      });
    });
    return () => {
      active = false;
    };
  }, [language]);

  useEffect(() => {
    const view = viewRef.current;
    if (view) view.dispatch({ effects: setRemoteCursors.of(remoteCursors) });
  }, [remoteCursors]);

  return <div className="code-editor live-code-editor" ref={host} />;
}
