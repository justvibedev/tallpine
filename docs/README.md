# tallpine rewrite docs

Tallpine runs as a long-running Go backend daemon
(`backend/`) plus an Electron + TypeScript frontend (`frontend/`). The backend
supervises coding-agent sessions and exposes daemon control, project/session
state, terminal streaming, and CDC/event infrastructure.

Start with [architecture.md](architecture.md) for the current backend model and
[cli/README.md](cli/README.md) for the CLI surface.

## Reference docs

| Doc                                                    | What it covers                                                                                                        |
| ------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| [documentation-map.md](documentation-map.md)           | Human-facing docs vs the machine-readable contract layer, source of truth per concern, and the CI gates that hold it. |
| [Public manual sources](../frontend/src/docs/content/index.mdx) | Existing user guides, configuration, capability catalog, CLI reference, and mobile access pages. |
| [architecture.md](architecture.md)                     | Current backend model, package layout, status derivation, persistence/CDC, and load-bearing rules.                    |
| [scm-observer.md](scm-observer.md)                     | SCM subsystem: polling pipeline, durable-state invariants, PR identity model, and the rename/transfer design.         |
| [backend-code-structure.md](backend-code-structure.md) | Package ownership rules for the Go backend: domain, services, ports, adapters, storage, HTTP, CLI, and daemon wiring. |
| [cli/README.md](cli/README.md)                         | CLI commands and daemon control surface.                                                                              |
| [self-hosted-remote.md](self-hosted-remote.md)         | Experimental multi-host setup, client workflow, security boundary, and current limits.                                  |
| [development.md](development.md)                       | Prerequisites, build steps, running tests, and troubleshooting for local development.                                 |
| [stack.md](stack.md)                                   | Accepted library/runtime choices, pending stack decisions, and dependencies explicitly avoided for V1.                |
| [daemon-environment.md](daemon-environment.md) | Proposal for the GUI-launch PATH/credentials problem when Electron starts the daemon. |
| [agent-policy.md](agent-policy.md) | Enterprise agent policy: permission cap, managed config, what it does and does not protect. |
| [planning.md](planning.md) | Planning before building: modes, flow, plan card, stale plans, MDM and CLI. |
| [switch-claude-subscription.md](switch-claude-subscription.md) | User guide: add a Claude account and move every session to it, what the first turn costs, and what Remove does. |
| [harness-version-refresh.md](harness-version-refresh.md) | Managed harness release switching, idle terminal refresh, and the native-resume support matrix. |
| [onboarding-contract-review.md](onboarding-contract-review.md) | Project onboarding readiness contract review and the remaining journey/acceptance gaps. |
| [onboarding-recovery-testing.md](onboarding-recovery-testing.md) | How to test clone-onboarding recovery: automated checks, browser flows, and manual desktop fixtures. |
| [awake-follow-up-controls.md](awake-follow-up-controls.md) | Stay Awake controls deliberately deferred from the first desktop-awake release (see ADR 0006). |
| [stay-awake-physical-tests.md](stay-awake-physical-tests.md) | Physical acceptance checklist for Stay Awake that automated tests cannot cover (see ADR 0006). |

See [startup-cues.md](startup-cues.md) for command cues that gate a new worktree's opening turn.

## Mental model

Persist durable facts, derive display status:

- session table: `activity_state`, `is_terminated`, identity, metadata
- PR tables: PR/CI/review facts
- derived read model: `service.Session` computes display status from session + PR facts
