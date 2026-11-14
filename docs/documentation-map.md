# Documentation map

This repository is edited by humans and by coding agents. Both need to know two
things quickly: which document describes a given concern, and which artifact is
the **source of truth** for it when documents disagree.

Tallpine's documentation has two layers. They grew organically; this page names them
so the split is intentional rather than incidental.

| Layer                     | Audience                      | Examples                                                        | How it stays correct                                    |
| ------------------------- | ----------------------------- | --------------------------------------------------------------- | ------------------------------------------------------- |
| Human-facing docs         | Contributors, users           | `README.md`, `CONTRIBUTING.md`, `docs/`, https://docs.tallpine.eu | Review. Prose describes code; it can lag behind it.     |
| Machine-readable contract | Coding agents, CI, generators | `openapi.yaml`, `AGENTS.md`, `skills/`, sqlc `gen/`             | Generated from source and/or checked by CI drift gates. |

The rule of thumb: **if an artifact in the contract layer disagrees with prose,
generated contracts and their source win** for implemented behavior. Handwritten
operating rules and skills still need comparison with current code and tests;
being listed in the contract layer does not make their prose drift-proof.

## Human-facing layer

| Document                                                    | Covers                                                                       |
| ----------------------------------------------------------- | ---------------------------------------------------------------------------- |
| [README.md](../README.md)                                   | Product overview, install, quickstart, top-level doc index.                  |
| [CONTRIBUTING.md](../CONTRIBUTING.md)                       | How to pick up work, claim issues, and open PRs.                             |
| [docs/README.md](README.md)                                 | Index of the repository's architecture and reference docs.                   |
| [docs/architecture.md](architecture.md)                     | Backend mental model, lifecycle, persistence/CDC, status derivation.         |
| [docs/backend-code-structure.md](backend-code-structure.md) | Package ownership rules for the Go backend.                                  |
| [docs/development.md](development.md)                       | Prerequisites, build, test, and troubleshooting for local development.       |
| [docs/adr/](adr/)                                           | Architecture decision records: why a boundary exists, not just what it is.   |
| https://docs.tallpine.eu                                          | Published product documentation for end users.                               |
| [frontend/src/docs/content/](../frontend/src/docs/content/)  | MDX sources for the public manual; `meta.json` files preserve navigation and order. |

These documents explain intent and rationale. They are reviewed by people and
are not machine-checked, so treat them as the _why_ and confirm the _what_
against the contract layer below.

For capability claims, compare the worker, Chat-driver, and reviewer registries
separately. For user flows, check the renderer and daemon wiring as well as
DTOs. The public manual uses the latest stable desktop release as its user
baseline: **v0.13.16**.
Contributor docs describe the checked-out revision of `main`, which can be ahead
of that release. Gated Cloud behavior needs a
separate deployed-environment check; source and a healthy public endpoint do
not prove an authenticated user journey.

## Machine-readable contract layer

| Artifact                                                               | Source of truth for                                                     | Generated from / defined in                                                                                            | Drift gate                                                                                                                                                               |
| ---------------------------------------------------------------------- | ----------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `backend/internal/httpd/apispec/openapi.yaml`                          | Daemon HTTP API: routes, request/response shapes, errors                | `backend/internal/httpd/controllers/dto_*.go` + `backend/internal/httpd/apispec/specgen/build.go` via `npm run api:spec` | `TestBuild_MatchesEmbedded` (embedded spec matches generator output) and `TestRouteSpecParity` (every registered route is in the spec) in `go test ./internal/httpd/...` |
| `packages/shared/api/schema.ts`                                           | Typed client the frontend uses to talk to the daemon                    | `openapi.yaml` via `npm run api:ts`                                                                                    | `scripts/verify` `api-drift` step regenerates and fails on `git diff`                                                                                                       |
| `backend/internal/storage/sqlite/gen/`                                 | Query/DTO code for SQLite storage                                       | `backend/internal/storage/sqlite/queries/*` + migrations via `npm run sqlc`                                            | `scripts/verify` `sqlc-drift` step                                                                                                                                            |
| [AGENTS.md](../AGENTS.md)                                              | Agent operating contract: repo layout, commands, hard rules, PR hygiene | Hand-written; `CLAUDE.md` is a pointer to it                                                                           | Not machine-checked. The hard rules it names are enforced by tests (for example loopback-only routing, status derivation) rather than by a doc gate.                     |
| `backend/internal/skillassets/using-tallpine/`                               | The `tallpine` CLI catalog installed into agent workspaces                    | Hand-written Markdown with YAML frontmatter, embedded in the binary                                                    | `TestEmbeddedSkillFrontmatterIsValidYAML` and sibling tests in `backend/internal/skillassets`                                                                            |
| `MobileAPIVersion` in `backend/internal/httpd/controllers/identity.go` | Contract version negotiated by the mobile client                        | Go constant                                                                                                            | Bump rule documented in the constant's comment and `docs/adr/0003-unauthenticated-identity-probe.md`                                                                     |
| `tallpine <command> --help`                                                  | Authoritative flag list for every CLI command                           | Cobra command definitions in `backend/internal/cli/`                                                                   | Table tests in `backend/internal/cli/*_test.go`                                                                                                                          |

The CLI's hand-mirrored DTOs (`backend/internal/cli/`) are a deliberate manual
boundary: they are not generated from `openapi.yaml`, and wire compatibility is
covered by tests rather than a generator. See "API contract changes" in
[AGENTS.md](../AGENTS.md).

## How to keep the layers in sync

- **Changing the daemon API**: edit the `dto_*.go` files and `build.go`, run `npm run api`,
  commit `openapi.yaml` and `schema.ts` with the Go change. CI fails otherwise.
- **Changing storage**: edit queries or add a migration, run `npm run sqlc`,
  commit `gen/`. Never hand-edit `gen/` or already-merged migrations.
- **Changing a hard rule or boundary**: update `AGENTS.md` in the same PR, and
  add an ADR under `docs/adr/` when the rule is a decision that needs rationale.
- **Changing `tallpine` CLI behavior**: update the command, its table test, and the
  matching page under `backend/internal/skillassets/using-tallpine/commands/`.
- **Changing prose only**: fine, but if the prose describes a contract artifact,
  re-read that artifact first. Prose follows the contract, not the reverse.

## Where to add new documentation

- A user needs it to use the product: https://docs.tallpine.eu (source under
  `frontend/src/docs/content/`).
- A contributor needs it to change the code: `docs/`, and add a row to
  [docs/README.md](README.md).
- An agent needs it to operate safely in the repo: `AGENTS.md`.
- An agent needs it to use the `tallpine` CLI at runtime: `backend/internal/skillassets/using-tallpine/`.
- It is a decision with trade-offs: `docs/adr/`.
