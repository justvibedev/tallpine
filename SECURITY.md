# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub:

1. Open the repository's **Security** tab.
2. Choose **Report a vulnerability** (GitHub private security advisories).
3. Describe the issue, the affected version or commit, and steps to reproduce.

Do not open a public issue or pull request for a vulnerability, and do not include secrets or personal data in a report.

Ordinary bugs that are not security-sensitive belong in the [issue tracker](https://github.com/justvibedev/tallpine/issues).

## Scope

Tallpine runs locally: a daemon on loopback or a Unix socket, an Electron app, and a CLI. Reports about authentication of the daemon API, handling of agent credentials, command execution, and the update path are in scope. Vulnerabilities in the upstream project that Tallpine does not change should also be reported to [OrchestratorInc/agent-orchestrator](https://github.com/OrchestratorInc/agent-orchestrator).

## What Tallpine does not protect against

**Tallpine does not defend against another malicious program running as the same operating-system user.** The daemon's install token (`~/.tallpine/daemon.token`), the session key and the database are files that your user can read. The install token keeps other OS users and containers out of the daemon API; it does not keep out a program that runs as you, which can read those files and act with the daemon's full authority.

Security mode (off by default; its file fence is macOS only and not applied to every harness mode) is a different control: it keeps fenced agents away from Tallpine's own state. It is not a boundary against other software on the machine, and it does not restrict network access. See [Security mode](https://docs.tallpine.eu/configuration/security-mode).

Reports that depend on an attacker already running code as the same user are generally out of scope unless they bypass a boundary Tallpine documents as protecting against it.

## Supported versions

Only the latest release and the current `main` branch receive fixes.
