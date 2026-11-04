# tallpine automation

Manage durable recurring session automations through the daemon API.

## Syntax

```text
tallpine automation <subcommand> [args] [flags]
tallpine automations <subcommand> [args] [flags]
```

## Subcommands

---

### tallpine automation create

Create a recurring automation for a project.

**Syntax:**
```text
tallpine automation create --project <id> --name <name> --prompt <text> (--rrule <rule> | --cron <expr>) [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project string` | Project id that owns the automation | Required |
| `--name string` | Display name | Required |
| `--prompt string` | Session task prompt | Required |
| `--rrule string` | RFC 5545 recurrence rule | Required unless `--cron` is set |
| `--cron string` | Supported five-field cron expression | Required unless `--rrule` is set |
| `--timezone string` | IANA timezone | Local zone when resolvable |
| `--kind string` | Session kind | `worker` |
| `--harness string` | Agent harness; empty uses project default | - |
| `--disabled` | Create disabled | - |
| `--json` | Print JSON | - |

**Example:**

```bash
tallpine automation create --project my-app --name "Morning triage" --prompt "Review overnight issues" --cron "0 9 * * *" --timezone America/Los_Angeles
```

---

### tallpine automation list

List automations.

**Syntax:**
```text
tallpine automation list [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project string` | Filter by project id | - |
| `--enabled bool` | Filter by enabled state when present | - |
| `--json` | Print JSON | - |

---

### tallpine automation get

Show one automation.

**Syntax:**
```text
tallpine automation get <id> [--json]
```

---

### tallpine automation update

Update automation metadata, schedule, harness, or enabled state.

**Syntax:**
```text
tallpine automation update <id> [flags]
```

**Flags:**

| Flag | Meaning |
|---|---|
| `--name string` | New display name |
| `--prompt string` | New task prompt |
| `--rrule string` | Replace schedule with an RRule |
| `--cron string` | Replace schedule with a cron expression |
| `--timezone string` | IANA timezone |
| `--kind string` | Session kind |
| `--harness string` | Agent harness; empty uses project default |
| `--enabled bool` | Enable or disable future dispatch |
| `--json` | Print JSON |

At least one update flag is required. `--rrule` and `--cron` are mutually exclusive.

---

### tallpine automation delete

Delete an automation and its run history. Linked sessions remain available.

**Syntax:**
```text
tallpine automation delete <id> [--yes]
tallpine automation rm <id> [--yes]
```

Without `--yes`, type the automation id when prompted to confirm.

---

### tallpine automation runs

List run history for one automation.

**Syntax:**
```text
tallpine automation runs <id> [--limit <n>] [--cursor <cursor>] [--json]
```

`--limit` must be 1–100. When a non-JSON listing prints `next cursor: ...`, pass that value to `--cursor` for the next page.
