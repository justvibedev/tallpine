# tallpine stop

Stop the Tallpine daemon.

## Syntax

```
tallpine stop [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output stop result as JSON | - |
| `--timeout duration` | How long to wait for daemon shutdown | `10s` |

## Examples

```bash
# Stop the daemon
tallpine stop
```

```bash
# Stop with a longer timeout
tallpine stop --timeout 30s
```
