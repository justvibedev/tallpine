# tallpine project

Manage projects: register repos, inspect, configure per-project settings, and remove.

## Syntax

```
tallpine project <subcommand> [args] [flags]
```

## Subcommands

---

### tallpine project add

Register a local git repo as a project so sessions can be spawned in it. The path must be an existing git repository on disk. With `--as-workspace`, the path may be a parent folder containing direct child git repositories; Tallpine initializes/adopts the parent as the root repo and gitignores children.

**Syntax:**
```
tallpine project add [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--as-workspace` | Register a parent folder as a workspace project (root-as-repo plus direct child repos) | - |
| `--id string` | Project id | Derived by the daemon from the path |
| `--name string` | Display name | - |
| `--orchestrator-agent string` | Default orchestrator session agent | - |
| `--path string` | Absolute path to the local git repo | Required |
| `--worker-agent string` | Default worker session agent | - |

**Examples:**

```bash
# Register a repo as a project
tallpine project add --path /path/to/my-app --name "my-app"
```

```bash
# Register a workspace (parent folder containing multiple repos)
tallpine project add --path /path/to/side-quests --as-workspace --name "side-quests"
```

---

### tallpine project ls

List registered projects. Aliases: `ls`, `list`.

**Syntax:**
```
tallpine project ls [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output projects as JSON | - |

**Examples:**

```bash
# List all registered projects
tallpine project ls
```

---

### tallpine project get

Fetch one registered project.

**Syntax:**
```
tallpine project get <id> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output project as JSON | - |

**Examples:**

```bash
# Get details for the my-app project
tallpine project get my-app
```

---

### tallpine project rm

Remove a registered project. Aliases: `rm`, `remove`, `delete`.

**Syntax:**
```
tallpine project rm <id> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output removal result as JSON | - |
| `-y, --yes` | Skip confirmation prompt | - |

**Examples:**

```bash
# Remove a project (with confirmation)
tallpine project rm my-app
```

```bash
# Remove without prompt
tallpine project rm my-app -y
```

---

### tallpine project set-config

Update a project's saved configuration. Tallpine reads these settings when it spawns a session. Field flags **merge** into the existing config (only the flags you pass change; an empty scalar value removes that field, `--env` merges by key and `--unset-env KEY` removes one, `--symlink`/`--post-create`/`--reviewer` replace their list, `--tracker-intake=false` disables intake). `--config-json` **replaces** the whole config with the given object, and `--clear` removes all configuration.

**Note:** the merge reads the config and writes it back in two requests with no revision check, so a concurrent config change made in between is overwritten.

**Warning:** `--config-json` replaces the whole config. The CLI silently ignores unknown keys in it, so a typo can leave the replacement empty. Set or clear reasoning effort per role with `--worker-effort`, `--orchestrator-effort` and `--reviewer-effort`; `tallpine project get --json` shows it and `--config-json` carries it, but a replacement without `effort` still drops saved values. See the [full-config warning](https://docs.orchestrator.inc/configuration/projects/#update-from-the-cli) before updating project settings from the CLI.

**Syntax:**
```
tallpine project set-config <id> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--agent-rules string` | Project-specific standing instructions appended to worker session prompts | - |
| `--agent-rules-file string` | Repo-relative file containing project-specific worker standing instructions | - |
| `--canonical-repo-url string` | Explicit upstream HTTPS repository URL for PR claims; may be on another host when the repo moved, then it replaces origin for PR tracking | - |
| `--clear` | Clear all config | - |
| `--config-json string` | Full config as a JSON object; replaces the entire config (overrides field flags) | - |
| `--default-branch string` | Base branch new session worktrees are created from | - |
| `--env stringArray` | Env var `KEY=VALUE` forwarded into sessions (repeatable) | - |
| `--unset-env stringArray` | Env var `KEY` to remove from the project's env (repeatable) | - |
| `--json` | Output the updated project as JSON | - |
| `--model string` | Agent model override (e.g. `claude-opus-4-5`) | - |
| `--no-ci` | Declare that the project has no CI: when the provider reports no checks at all for a PR head, CI counts as passing only once a Tallpine review (`tallpine review trigger`) approved that exact head; provider review approvals do not count. Failing or pending checks still block. `--no-ci=false` clears it | Disabled |
| `--orchestrator-agent string` | Harness override for orchestrator sessions | - |
| `--orchestrator-effort string` | Reasoning effort for orchestrator sessions (`orchestrator.agentConfig.effort`); empty clears | - |
| `--orchestrator-rules string` | Project-specific standing instructions appended to orchestrator session prompts | - |
| `--permission string` | Permission mode: `default`, `accept-edits`, `auto`, `bypass-permissions` | - |
| `--post-create stringArray` | Command to run after workspace creation (repeatable) | - |
| `--session-prefix string` | Displayed session-id prefix | - |
| `--symlink stringArray` | Repo-relative path to symlink into workspaces (repeatable) | - |
| `--tracker-assignee string` | Issue assignee required for tracker intake eligibility | - |
| `--tracker-intake` | Opt this project into GitHub or GitLab issue intake; inert unless the daemon also has `TALLPINE_TRACKER_INTAKE=on` | Disabled |
| `--tracker-repo string` | Provider-native issue repository (`owner/repo` or `group/subgroup/repo`); defaults to the Git origin | - |
| `--reviewer stringArray` | Reviewer harness for worker PRs (repeatable) | - |
| `--reviewer-effort string` | Reasoning effort for every configured reviewer; needs `--reviewer` first; empty clears | - |
| `--worker-agent string` | Harness override for worker sessions | - |
| `--worker-effort string` | Reasoning effort for worker sessions (`worker.agentConfig.effort`); empty clears | - |

Use `--config-json` for fields without a dedicated flag, including
`trackerIntake.provider`, `autoReview`, `containerReap.disabled`,
`agentConfig.mode`, `worker.agentConfig.mode`, and
`orchestrator.agentConfig.mode`. The full-config replacement warning above
applies to `--config-json`.

**Examples:**

```bash
# Set default branch and model for a project
tallpine project set-config my-app --default-branch main --model claude-opus-4-5
```

```bash
# Replace the complete config; include every setting you want to keep
tallpine project set-config my-app --config-json \
  '{"defaultBranch":"main","agentConfig":{"model":"claude-opus-4-5"}}'
```

```bash
# Set an env var and a post-create command
tallpine project set-config my-app --env "NODE_ENV=development" --post-create "npm install"
```

```bash
# Set worker and orchestrator standing rules
tallpine project set-config my-app --agent-rules "Run focused tests before reporting done." --orchestrator-rules "Delegate implementation work to worker sessions."
```

Passing only `--agent-rules` and/or `--orchestrator-rules` writes just those two
fields (`PUT /api/v1/projects/<id>/rules`). An orchestrator session may do this
on its own project only; every other config change, and workers, get
`403 AGENT_SCOPE_FORBIDDEN`. Each change is logged with the actor and lengths.

```bash
# Load worker rules from a repo-relative file
tallpine project set-config my-app --agent-rules-file docs/worker-rules.md
```

```bash
# Enable assignee-scoped issue intake (requires TALLPINE_TRACKER_INTAKE=on)
tallpine project set-config my-app --tracker-intake \
  --tracker-repo owner/repo --tracker-assignee alice
```
