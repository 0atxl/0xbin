import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";

document.documentElement.dataset.theme = window.matchMedia(
  "(prefers-color-scheme: dark)",
).matches
  ? "dark"
  : "light";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
