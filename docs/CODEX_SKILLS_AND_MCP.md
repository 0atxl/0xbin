# Codex Skills and MCP Recommendations for 0xbin

**Recommendation:** Start with a small setup. 0xbin does not need an MCP-heavy or skill-heavy repository.

`AGENTS.md`, skills, and MCP solve different problems:

- `AGENTS.md` supplies durable repository rules and verification expectations.
- Skills package repeatable workflows that Codex can invoke when appropriate.
- MCP connects Codex to external tools or live/private data.

Official Codex references: [AGENTS.md guidance](https://developers.openai.com/codex/guides/agents-md), [Agent Skills](https://developers.openai.com/codex/skills), and [MCP](https://developers.openai.com/codex/mcp).

## 1. Start With These Capabilities

### 1.1 Browser/Playwright workflow — recommended for frontend phase

Use when:

- Testing creation, encrypted fragment handling, missing-key dialog, and burn confirmation
- Checking keyboard navigation and responsive behaviour
- Inspecting browser console/network traffic to prove the key never reaches the server
- Running stored-XSS and accessibility flows

Why: The highest-risk product behaviour crosses browser JavaScript, routing, cryptography, and the API. Browser automation gives stronger evidence than unit tests alone.

This may be provided as an installed browser/Playwright skill or MCP server depending on the Codex environment. Prefer the already-supported capability rather than installing duplicate browser systems.

### 1.2 Skill Creator — useful later, not a day-one requirement

Use the built-in skill creator only after a workflow repeats enough to deserve automation. Codex skills are directories centered on `SKILL.md`, optionally with scripts/references/assets; they can be invoked explicitly or selected from their description. [Official skill structure](https://developers.openai.com/codex/skills).

Good later candidates:

- `0xbin-release-check`: build, migrations, image, upgrade, restore, and changelog verification
- `0xbin-security-regression`: run crypto vectors, XSS corpus, fragment-leak inspection, race and fuzz checks
- `0xbin-migration-check`: create old-version fixtures, migrate, verify, and test rollback guidance

Do not create these before the real commands and failure modes exist. Until then, the implementation plan and `AGENTS.md` are sufficient.

### 1.3 Security scanning tools — commands before skills

Establish ordinary repository commands first:

- Go formatting, vet/static analysis, test, and race detector
- Frontend lint/typecheck/test/audit
- Dependency update scanning
- Container vulnerability scanning
- Fuzz targets for parsers and proxy headers

Wrap them in a custom skill only when the procedure stabilizes. A skill should teach a repeatable workflow; it should not replace versioned project scripts.

## 2. MCP Servers

### 2.1 GitHub MCP — recommended after the repository is on GitHub

Use for:

- Reading and updating issues
- Reviewing pull requests and CI status
- Connecting implementation-plan steps to tracked work
- Inspecting releases and repository metadata

Do not use it merely to edit local files; Codex already works directly in the repository. Grant the minimum permissions needed, especially before allowing issue/PR writes.

### 2.2 Browser/Playwright MCP — recommended if no native browser tool exists

Use for the same end-to-end scenarios listed above. Install only one browser-control route. Duplicate browser MCPs add confusion and inconsistent sessions.

### 2.3 Figma MCP — optional tomorrow or later

Use only if the frontend design actually lives in Figma and Codex needs to inspect frames, design tokens, components, or assets. It is unnecessary while frontend work is behavioural and code-first.

### 2.4 Error-monitoring MCP — optional after public beta

If the hosted service later uses Sentry or a similar platform, its MCP can help investigate live errors. Do not add monitoring MCP before selecting and deploying the monitoring system. Ensure URLs/fragments, paste content, and keys are redacted at the SDK level before any external telemetry.

## 3. MCPs Not Needed Initially

- **Supabase/PostgreSQL MCP:** The product uses SQLite.
- **Database write MCP:** Agents should use migrations and tests, not mutate production SQLite directly.
- **Filesystem MCP:** Codex already has repository filesystem access.
- **Redis MCP:** Redis is not part of the architecture.
- **Kubernetes MCP:** Kubernetes is outside the roadmap.
- **Slack/Linear MCP:** Add only if the project genuinely adopts those systems.
- **Generic web-scraping MCP:** Official documentation and normal web research are sufficient.
- **AI-model/OpenAI API MCP:** 0xbin has no AI feature or OpenAI API dependency.

## 4. Recommended Initial Setup

At repository start:

```text
Required
├── AGENTS.md
├── spec.md and docs/
├── local build/test scripts
└── browser testing capability by frontend Phase 2/3

Optional once useful
├── GitHub MCP
└── Figma MCP if a Figma design exists

Later, based on repeated workflows
├── release-check skill
├── security-regression skill
└── error-monitoring MCP
```

## 5. Codex Working Pattern

1. Open the repository root in VS Code/Codex.
2. Ask Codex to read `AGENTS.md`, `spec.md`, and the applicable implementation-plan step.
3. Work one step at a time.
4. Require the verification gate before moving on.
5. Review the diff and explain the design yourself before accepting it.
6. When Codex repeats a mistake, add a short durable rule to `AGENTS.md`.
7. Turn a workflow into a skill only after it becomes stable and repetitive.

Official guidance recommends keeping `AGENTS.md` practical and small, using skills for reusable workflows, and MCP for external systems rather than treating them as competing alternatives. [Codex customization overview](https://developers.openai.com/codex/concepts/customization).

## 6. Day-One Recommendation

For 0xbin, begin with:

1. The root `AGENTS.md` already created.
2. No custom project skill yet.
3. Browser/Playwright capability when frontend implementation begins.
4. GitHub MCP only after the repository and issue workflow exist.
5. Figma MCP only if tomorrow's design work uses Figma.

This keeps the setup understandable and prevents tool configuration from becoming a project of its own.

