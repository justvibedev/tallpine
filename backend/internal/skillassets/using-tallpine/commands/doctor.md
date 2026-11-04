# tallpine doctor

Run local Tallpine health checks. Use this to diagnose setup problems or verify the environment is correctly configured.

## Syntax

```
tallpine doctor [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output health checks as JSON | - |

## Examples

```bash
# Run health checks
tallpine doctor
```

```bash
# Get health check results as JSON
tallpine doctor --json
```
