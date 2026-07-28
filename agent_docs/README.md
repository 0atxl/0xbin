# Agent-Facing Documentation

This directory contains internal implementation guidance, design decisions,
delivery plans, and tooling notes intended for AI coding agents and
maintainers working on the project.

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
- [`PHASES.md`](PHASES.md) — delivery phases and scope boundaries.
- [`SEARCH_PERFORMANCE.md`](SEARCH_PERFORMANCE.md) — viewer-search performance
  implementation notes.

## Companion interfaces

The implemented command-line client lives in the separate
[`0xbin-cli`](https://github.com/0atxl/0xbin-cli) repository and consumes this
service's stable HTTP and encryption contracts. A separate `0xbin-mcp` product
interface is planned to reuse that CLI library. API or cryptographic changes
must preserve those contracts or be explicitly versioned.
