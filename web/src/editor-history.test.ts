import { closeBrackets, insertBracket } from "@codemirror/autocomplete";
import { insertTab, undo } from "@codemirror/commands";
import { EditorSelection, EditorState, Transaction } from "@codemirror/state";
import { describe, expect, it } from "vitest";
import { editorHistoryExtensions } from "./editor-history";

describe("editor history", () => {
  it("groups a continuous run of typing into one undo step", () => {
    let state = EditorState.create({
      doc: "",
      extensions: editorHistoryExtensions,
    });
    for (const [index, letter] of [..."hello"].entries()) {
      state = state.update({
        changes: { from: index, insert: letter },
        annotations: [
          Transaction.userEvent.of("input.type"),
          Transaction.time.of(index * 900),
        ],
      }).state;
    }

    expect(
      undo({ state, dispatch: (transaction) => (state = transaction.state) }),
    ).toBe(true);
    expect(state.doc.toString()).toBe("");
  });

  it("starts a new undo step after a space", () => {
    let state = EditorState.create({
      doc: "",
      extensions: editorHistoryExtensions,
    });
    for (const [index, letter] of [..."hello world"].entries()) {
      state = state.update({
        changes: { from: index, insert: letter },
        annotations: Transaction.userEvent.of("input.type"),
      }).state;
    }

    expect(
      undo({ state, dispatch: (transaction) => (state = transaction.state) }),
    ).toBe(true);
    expect(state.doc.toString()).toBe("hello ");
    expect(
      undo({ state, dispatch: (transaction) => (state = transaction.state) }),
    ).toBe(true);
    expect(state.doc.toString()).toBe("");
  });

  it("undoes a backspace before the preceding typing", () => {
    let state = EditorState.create({
      doc: "",
      extensions: editorHistoryExtensions,
    });
    state = state.update({
      changes: { from: 0, insert: "hello" },
      annotations: Transaction.userEvent.of("input.type"),
    }).state;
    state = state.update({
      changes: { from: 4, to: 5 },
      annotations: Transaction.userEvent.of("delete.backward"),
    }).state;

    expect(
      undo({ state, dispatch: (transaction) => (state = transaction.state) }),
    ).toBe(true);
    expect(state.doc.toString()).toBe("hello");
    expect(
      undo({ state, dispatch: (transaction) => (state = transaction.state) }),
    ).toBe(true);
    expect(state.doc.toString()).toBe("");
  });

  it("undoes an automatically closed bracket as one edit", () => {
    let state = EditorState.create({
      doc: "",
      extensions: [editorHistoryExtensions, closeBrackets()],
    });
    const bracket = insertBracket(state, "{");
    expect(bracket).not.toBeNull();
    state = bracket!.state;
    expect(state.doc.toString()).toBe("{}");

    expect(
      undo({ state, dispatch: (transaction) => (state = transaction.state) }),
    ).toBe(true);
    expect(state.doc.toString()).toBe("");
  });

  it("inserts Tab at the cursor instead of indenting the line start", () => {
    let state = EditorState.create({
      doc: "hello",
      selection: EditorSelection.cursor(2),
    });
    expect(
      insertTab({
        state,
        dispatch: (transaction) => (state = transaction.state),
      }),
    ).toBe(true);
    expect(state.doc.toString()).toBe("he\tllo");
  });
});
