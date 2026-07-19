import { EditorState, StateField, Transaction } from "@codemirror/state";
import { history, isolateHistory } from "@codemirror/commands";

type EditKind = "typing" | "deletion" | null;
type PreviousEdit = {
  kind: EditKind;
  endedWithWhitespace: boolean;
};

function editKind(transaction: Transaction): EditKind {
  if (!transaction.docChanged) return null;
  const lengthChange =
    transaction.newDoc.length - transaction.startState.doc.length;
  if (lengthChange > 0) return "typing";
  if (lengthChange < 0) return "deletion";
  return null;
}

function endsWithWhitespace(transaction: Transaction): boolean {
  let finalInsertedCharacter = "";
  transaction.changes.iterChanges((_, __, ___, ____, inserted) => {
    if (inserted.length > 0) {
      finalInsertedCharacter = inserted.sliceString(inserted.length - 1);
    }
  });
  return /\s/.test(finalInsertedCharacter);
}

const previousEdit = StateField.define<PreviousEdit>({
  create: () => ({ kind: null, endedWithWhitespace: false }),
  update: (_, transaction) => ({
    kind: transaction.docChanged ? editKind(transaction) : null,
    endedWithWhitespace:
      transaction.docChanged && editKind(transaction) === "typing"
        ? endsWithWhitespace(transaction)
        : false,
  }),
});

const separateEditKinds = EditorState.transactionExtender.of((transaction) => {
  const current = editKind(transaction);
  const previous = transaction.startState.field(previousEdit);
  if (
    current &&
    previous.kind &&
    (current !== previous.kind ||
      (current === "typing" && previous.endedWithWhitespace))
  ) {
    return { annotations: isolateHistory.of("before") };
  }
  return null;
});

export const editorHistoryExtensions = [
  history({ newGroupDelay: 5000 }),
  previousEdit,
  separateEditKinds,
];
