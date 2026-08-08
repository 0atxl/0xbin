import { useEffect, useId, useRef, useState } from "react";
import { Compartment, EditorState } from "@codemirror/state";
import {
  defaultKeymap,
  historyKeymap,
  indentLess,
  insertTab,
} from "@codemirror/commands";
import {
  EditorView,
  keymap,
  lineNumbers,
  placeholder,
  type ViewUpdate,
} from "@codemirror/view";
import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import {
  HighlightStyle,
  indentOnInput,
  indentUnit,
  syntaxHighlighting,
} from "@codemirror/language";
import { tags } from "@lezer/highlight";
import {
  editorIndentColumns,
  languages,
  loadEditorLanguage,
} from "./languages";
import { editorHistoryExtensions } from "./editor-history";

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

export function LanguageMenu({
  value,
  onChange,
  disabled = false,
}: {
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [closing, setClosing] = useState(false);
  const selectRef = useRef<HTMLDivElement>(null);
  const closeTimeout = useRef<number | undefined>(undefined);
  const menuID = useId();
  const selected =
    languages.find(([language]) => language === value)?.[1] ?? value;

  useEffect(() => {
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") closeMenu();
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [closing, open]);

  useEffect(() => {
    const closeOutside = (event: PointerEvent) => {
      if (
        event.target instanceof Node &&
        !selectRef.current?.contains(event.target)
      ) {
        closeMenu();
      }
    };
    document.addEventListener("pointerdown", closeOutside);
    return () => document.removeEventListener("pointerdown", closeOutside);
  }, [closing, open]);

  useEffect(
    () => () => {
      if (closeTimeout.current !== undefined) {
        window.clearTimeout(closeTimeout.current);
      }
    },
    [],
  );

  function closeMenu() {
    if (!open || closing) return;
    setClosing(true);
    closeTimeout.current = window.setTimeout(() => {
      setOpen(false);
      setClosing(false);
      closeTimeout.current = undefined;
    }, 140);
  }

  function toggleMenu() {
    if (closing) {
      if (closeTimeout.current !== undefined) {
        window.clearTimeout(closeTimeout.current);
        closeTimeout.current = undefined;
      }
      setClosing(false);
      setOpen(true);
      return;
    }
    if (open) {
      closeMenu();
      return;
    }
    setOpen(true);
  }

  return (
    <div className="custom-select" ref={selectRef}>
      <button
        type="button"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={menuID}
        onClick={toggleMenu}
      >
        <CodeIcon />
        <span>{selected}</span>
        <ChevronIcon />
      </button>
      {open ? (
        <ul
          id={menuID}
          className={closing ? "is-closing" : undefined}
          role="listbox"
          aria-label="Language"
        >
          {languages.map(([language, label]) => (
            <li key={language} role="option" aria-selected={language === value}>
              <button
                type="button"
                disabled={disabled}
                onClick={() => {
                  onChange(language);
                }}
              >
                <span>{label}</span>
                {language === value ? <CheckIcon /> : null}
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

export function CodeEditor({
  value,
  language,
  ariaLabel = "Paste content",
  onChange,
  onSubmit,
}: {
  value: string;
  language: string;
  ariaLabel?: string;
  onChange: (value: string) => void;
  onSubmit: () => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | undefined>(undefined);
  const languageConfig = useRef(new Compartment());
  const onChangeRef = useRef(onChange);
  const onSubmitRef = useRef(onSubmit);
  onChangeRef.current = onChange;
  onSubmitRef.current = onSubmit;

  useEffect(() => {
    if (!host.current) return;
    try {
      const editor = new EditorView({
        state: EditorState.create({
          doc: value,
          extensions: [
            lineNumbers(),
            EditorView.lineWrapping,
            placeholder("Write text or code here…"),
            closeBrackets(),
            indentOnInput(),
            editorHistoryExtensions,
            syntaxHighlighting(editorHighlightStyle, { fallback: true }),
            EditorState.tabSize.of(editorIndentColumns),
            indentUnit.of(" ".repeat(editorIndentColumns)),
            languageConfig.current.of([]),
            EditorView.contentAttributes.of({
              "aria-label": ariaLabel,
            }),
            keymap.of([
              {
                key: "Mod-Enter",
                run: () => {
                  onSubmitRef.current();
                  return true;
                },
              },
              { key: "Tab", run: insertTab },
              { key: "Shift-Tab", run: indentLess },
              ...historyKeymap,
              ...closeBracketsKeymap,
              ...defaultKeymap,
            ]),
            EditorView.updateListener.of((update: ViewUpdate) => {
              if (update.docChanged) {
                onChangeRef.current(update.state.doc.toString());
              }
            }),
          ],
        }),
        parent: host.current,
      });
      view.current = editor;
      return () => {
        view.current = undefined;
        editor.destroy();
      };
    } catch {
      return;
    }
  }, []);

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

  return (
    <div className="code-editor" ref={host}>
      <textarea
        className="editor-fallback"
        aria-label={ariaLabel}
        placeholder="Write text or code here…"
        wrap="soft"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
            event.preventDefault();
            onSubmit();
          }
        }}
      />
    </div>
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

export function CodeIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="m7 5-5 5 5 5M13 5l5 5-5 5" />
    </svg>
  );
}
