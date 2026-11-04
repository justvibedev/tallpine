package agentauth

import (
	"strings"
)

// plans is the code-reviewed authentication allowlist in stable Harness
// settings order. Commands must be added here, never supplied by clients.
var plans = []Plan{
	loginMenuPlan("claude-code", "claude-login", nil, "Log in to Claude Code", []string{"claude", "auth", "login"}, "Choose Claude subscription, Anthropic Console, or SSO", "https://code.claude.com/docs/en/installation"),
	loginMenuPlan("codex", "codex-login", []string{"--use-default-credential-store"}, "Log in to Codex", []string{"codex", "login"}, "Choose ChatGPT, device code, API key, or access token", "https://github.com/openai/codex"),
	plan("kiro", ActionLogin, "Log in to Kiro", []string{"kiro-cli", "login"}, "Native browser flow; device flow remains a CLI option", "https://kiro.dev/docs/getting-started/installation/"),
	// dsh has no login subcommand: credentials are records in its own store
	// (~/.dsh/.credentials.yaml), written by the web profile's Models page.
	plan("deepseek-harness", ActionSetup, "Set up DeepSeek", []string{"dsh", "--profile", "web"}, "Opens DeepSeek's Models page to store an API key and pick a model route; leave it running until the key is saved", "https://github.com/deepseek-ai/deepseek-harness"),
}

func loginMenuPlan(agentID, launcher string, launcherArgs []string, title string, command []string, guidance, docs string) Plan {
	p := plan(agentID, ActionLogin, title, command, guidance, docs)
	p.launcher = launcher
	p.launcherArgs = launcherArgs
	return p
}

var planByAgentID = func() map[string]Plan {
	out := make(map[string]Plan, len(plans))
	for _, plan := range plans {
		out[plan.AgentID] = plan
	}
	return out
}()

func plan(agentID string, action Action, title string, command []string, guidance, docs string) Plan {
	logout := logoutCommand(agentID)
	return Plan{
		LogoutCommand:    strings.Join(logout, " "),
		logoutCommand:    logout,
		AgentID:          agentID,
		Action:           action,
		LaunchMode:       LaunchTerminal,
		DisplayCommand:   strings.Join(command, " "),
		Guidance:         guidance,
		DocumentationURL: docs,
		command:          command,
		title:            title,
	}
}

// Only documented CLI subcommands belong here. An absent entry explicitly
// means unsupported; do not infer logout from login or delete credential files.
func logoutCommand(agentID string) []string {
	switch agentID {
	case "claude-code":
		return []string{"claude", "auth", "logout"}
	case "codex":
		return []string{"codex", "logout"}
	default:
		return nil
	}
}
