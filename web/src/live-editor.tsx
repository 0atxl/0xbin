import { useEffect, useRef } from "react";
import {
  Compartment,
  EditorState,
  StateEffect,
  StateField,
} from "@codemirror/state";
import { collab } from "@codemirror/collab";
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
  lineNumbers,
  placeholder,
  WidgetType,
  type DecorationSet,
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
  anchor: number;
  head: number;
  active: boolean;
};

const setRemoteCursors = StateEffect.define<RemoteCursor[]>();

class RemoteCursorWidget extends WidgetType {
  constructor(
    private readonly nickname: string,
    private readonly color: string,
    private readonly active: boolean,
  ) {
    super();
  }

  toDOM() {
    const element = document.createElement("span");
    element.className = `live-remote-caret${this.active ? " is-active" : ""}`;
    element.style.setProperty(
      "--remote-color",
      safeParticipantColor(this.color),
    );
    element.setAttribute("aria-hidden", "true");
    const label = document.createElement("span");
    label.className = "live-remote-label";
    label.textContent = this.nickname;
    element.append(label);
    return element;
  }

  ignoreEvent() {
    return true;
  }
}

const remoteCursorField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(decorations, transaction) {
    let next = decorations.map(transaction.changes);
    for (const effect of transaction.effects) {
      if (!effect.is(setRemoteCursors)) continue;
      const ranges = effect.value.flatMap((cursor) => {
        const from = clampPosition(
          Math.min(cursor.anchor, cursor.head),
          transaction.state.doc.length,
        );
        const to = clampPosition(
          Math.max(cursor.anchor, cursor.head),
          transaction.state.doc.length,
        );
        const result = [];
        if (from !== to) {
          result.push(
            Decoration.mark({
              class: "live-remote-selection",
              attributes: {
                style: `--remote-color: ${safeParticipantColor(cursor.color)}`,
              },
            }).range(from, to),
          );
        }
        result.push(
          Decoration.widget({
            widget: new RemoteCursorWidget(
              cursor.nickname,
              cursor.color,
              cursor.active,
            ),
            side: cursor.head <= cursor.anchor ? -1 : 1,
          }).range(clampPosition(cursor.head, transaction.state.doc.length)),
        );
        return result;
      });
      next = Decoration.set(ranges, true);
    }
    return next;
  },
  provide: (field) => EditorView.decorations.from(field),
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
