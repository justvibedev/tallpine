# Contributing

Tallpine is a fork of [Agent Orchestrator](https://github.com/OrchestratorInc/agent-orchestrator) by OrchestratorInc (Apache-2.0). Contributions are welcome: bug fixes, tests, and documentation. For new features, open an issue first and wait for a maintainer to agree before writing code.

Contributions are accepted under the project's [Apache-2.0 license](LICENSE).

## Build and test

Prerequisites: macOS, Git, Go (version in `backend/go.mod`), Node.js 22 with npm. [AGENTS.md](AGENTS.md) is the authoritative reference for layout, conventions, and commands; [docs/development.md](docs/development.md) covers local setup. Full documentation is at <https://docs.tallpine.eu>.

```bash
# Backend (CLI + daemon)
cd backend && ../scripts/go build -o ./bin/tallpine ./cmd/tallpine

# Frontend (Electron app)
cd frontend && npm ci && npm run dev

# Checks, run from the repository root
make verify-fast    # only what your branch touches vs origin/main; run before every push
make verify         # the full suite; run once before opening a PR
make lint-changed   # lint only changed files
```

GitHub Actions is not used for this repository; `scripts/verify` runs the checks locally. Run the narrowest relevant tests first (one Go package or one vitest file), then `make verify-fast`. Include the verify summary in your PR description. If a step fails on `main` too, say so in the PR instead of fixing unrelated code.

If you change `backend/internal/httpd/apispec/openapi.yaml`, regenerate the TypeScript schema as described in AGENTS.md and commit both files.

## Issues

Use the [GitHub issue forms](https://github.com/justvibedev/tallpine/issues/new/choose) for bugs and feature requests. For a bug, a short description of what you did and what went wrong is enough; add the Tallpine version, OS, and steps to reproduce if you have them. Search existing issues first. Remove secrets and personal data from logs and screenshots.

Report security vulnerabilities privately; see [SECURITY.md](SECURITY.md).

## Pull requests

- Branch from `main`; one focused change per PR; link the issue (`Fixes #123`).
- Use conventional commit messages.
- Add or update tests for behavior you change, and explain intentional omissions.
- Follow the repository rules in [AGENTS.md](AGENTS.md), including the Tallpine side-by-side identity rules (never reintroduce `ao`, `~/.ao`, or `AO_*`).
- The PR template is prefilled; fill in each section.
- A maintainer reviews and merges once the local checks pass.

## Conduct

Be respectful and constructive, and assume good intent.
