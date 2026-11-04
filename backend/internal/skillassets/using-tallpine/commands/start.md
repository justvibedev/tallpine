# tallpine start

Fetch (if needed) and open the Tallpine desktop app. The desktop app owns the daemon, state, and updates. `tallpine start` no longer runs a daemon: it resolves the installed app (or downloads the latest release), opens it, and exits.

## Syntax

```
tallpine start [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output start result as JSON | - |

## Examples

```bash
# Open the Tallpine desktop app
tallpine start
```

```bash
# Open the app and get the result as JSON
tallpine start --json
```
