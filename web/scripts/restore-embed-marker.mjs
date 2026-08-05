import { mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";

const marker = resolve(process.cwd(), "../internal/webassets/dist/.keep");
await mkdir(dirname(marker), { recursive: true });
await writeFile(
  marker,
  "The Vite production build writes embedded frontend assets into this directory.\n",
);
