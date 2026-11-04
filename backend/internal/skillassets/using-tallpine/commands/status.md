# tallpine status

Show Tallpine daemon status. Use this to verify the daemon is up and check which port it is bound to.

## Syntax

```
tallpine status [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output status as JSON | - |

## Examples

```bash
# Check daemon status
tallpine status
```

```bash
# Get status as JSON (e.g. to check port programmatically)
tallpine status --json
```
