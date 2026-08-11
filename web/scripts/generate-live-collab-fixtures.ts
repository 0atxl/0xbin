import { mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { buildLiveCollabFixtures } from "../src/live-collab-fixtures.ts";

const repositoryRoot = resolve(
  dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const fixturePath = resolve(repositoryRoot, "tests/livecollab/fixtures.json");

await mkdir(dirname(fixturePath), { recursive: true });
await writeFile(
  fixturePath,
  `${JSON.stringify({ fixtures: buildLiveCollabFixtures() }, null, 2)}\n`,
);
