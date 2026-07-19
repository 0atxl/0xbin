import type { Extension } from "@codemirror/state";

export const editorIndentColumns = 8;

export const languages = [
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
] as const;

export async function loadEditorLanguage(language: string): Promise<Extension> {
  switch (language) {
    case "javascript": {
      const { javascript } = await import("@codemirror/lang-javascript");
      return javascript();
    }
    case "typescript": {
      const { javascript } = await import("@codemirror/lang-javascript");
      return javascript({ typescript: true });
    }
    case "html": {
      const { html } = await import("@codemirror/lang-html");
      return html();
    }
    case "c":
    case "cpp": {
      const { cpp } = await import("@codemirror/lang-cpp");
      return cpp();
    }
    case "java": {
      const { java } = await import("@codemirror/lang-java");
      return java();
    }
    case "rust": {
      const { rust } = await import("@codemirror/lang-rust");
      return rust();
    }
    case "python": {
      const { python } = await import("@codemirror/lang-python");
      return python();
    }
    case "go": {
      const { go } = await import("@codemirror/lang-go");
      return go();
    }
    default:
      return [];
  }
}
