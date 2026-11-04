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

## Supported versions

Only the latest release and the current `main` branch receive fixes.
