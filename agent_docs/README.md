# Agent-Facing Documentation

This directory contains internal implementation guidance, design decisions,
delivery plans, and tooling notes intended for AI coding agents and
maintainers working on the project.

The live-sharing extension is implemented through Steps 0A–13 of its
implementation plan, and release-hardening Phases 0–10 are complete. Phases
0–5 of the bounded browser-identity and creator-authority evolution are
complete; Phase 6 is next. Its final workspace design pass follows the Phase 6
behavioral gate and precedes code-quality closure and the final release audit.

The repository-level instructions remain in [`AGENTS.md`](../AGENTS.md) at
the repository root because agent tools discover that filename there.

## Contents

- [`GOALS.md`](GOALS.md) — product, engineering, and learning goals that guide
  agent prioritization.
- [`CODEX_SKILLS_AND_MCP.md`](CODEX_SKILLS_AND_MCP.md) — recommendations for
  configuring Codex skills and MCP integrations for this project.
- [`TECHNICAL_DESIGN.md`](TECHNICAL_DESIGN.md) — internal component, data, and
  security-boundary design.
- [`FRONTEND.md`](FRONTEND.md) — frontend design and implementation baseline.
- [`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md) — ordered implementation
  work and verification gates.
- [`LIVE_SHARING_IMPLEMENTATION_PLAN.md`](LIVE_SHARING_IMPLEMENTATION_PLAN.md)
  — comprehensive plan for the optional multi-tab live-sharing extension.
- [`LIVE_SHARING_IDENTITY_AUTHORITY_PLAN.md`](LIVE_SHARING_IDENTITY_AUTHORITY_PLAN.md)
  — planned browser-profile identity, durable creator capability, and
  reversible room-lock foundation.
- [`LIVE_SHARING_REMEDIATION_PLAN.md`](LIVE_SHARING_REMEDIATION_PLAN.md) —
  phased, usage-conscious execution plan for findings from the final
  live-sharing audit.
- [`PHASES.md`](PHASES.md) — delivery phases and scope boundaries.
- [`SEARCH_PERFORMANCE.md`](SEARCH_PERFORMANCE.md) — viewer-search performance
  implementation notes.

## Companion interfaces

The implemented command-line client lives in the separate
[`0xbin-cli`](https://github.com/0atxl/0xbin-cli) repository and consumes this
service's stable HTTP and encryption contracts. A separate `0xbin-mcp` product
interface is planned to reuse that CLI library. API or cryptographic changes
must preserve those contracts or be explicitly versioned.
