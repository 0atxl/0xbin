# Contributing to 0xbin

Thanks for contributing. Please keep changes scoped to one implementation-plan
step where possible and read `AGENTS.md` before starting work.

## Local checks

Install Go 1.26 and Node.js 24 or newer, then run:

```text
npm --prefix web ci
make format
make lint
make test
make test-race
make test-e2e
make build
```

`test-e2e` is a documented placeholder until browser flows are introduced in
the frontend phase. Do not add product behaviour before its implementation-plan
step.

## Pull requests

- Explain the requirement and implementation-plan step addressed.
- Include tests for changed behaviour.
- Do not include paste bodies, keys, or other sensitive data in fixtures,
  screenshots, logs, or commit messages.
- Keep documentation aligned with observable behaviour.
