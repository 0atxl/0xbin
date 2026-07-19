import { describe, expect, it } from "vitest";
import {
  editorIndentColumns,
  languages,
  loadEditorLanguage,
} from "./languages";

describe("editor languages", () => {
  it("offers only the reviewed language set", () => {
    expect(languages).toEqual([
      ["plaintext", "Plain text"],
      ["javascript", "JavaScript"],
      ["typescript", "TypeScript"],
      ["html", "HTML"],
      ["python", "Python"],
      ["go", "Go"],
      ["c", "C"],
      ["cpp", "C++"],
      ["java", "Java"],
      ["rust", "Rust"],
    ]);
  });

  it("uses eight-column indentation and highlights every code option", async () => {
    expect(editorIndentColumns).toBe(8);
    for (const [language] of languages) {
      if (language !== "plaintext") {
        await expect(loadEditorLanguage(language)).resolves.not.toEqual([]);
      }
    }
    await expect(loadEditorLanguage("unsupported")).resolves.toEqual([]);
  });
});
