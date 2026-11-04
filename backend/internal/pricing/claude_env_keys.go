package pricing

// ClaudeCredentialEnv are variables that make Claude Code use something other
// than a config folder's own login: API keys, tokens, a base URL and the
// route flags. The Claude account adapter neutralises them per launch.
var ClaudeCredentialEnv = append([]string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL",
}, ClaudeRouteEnvFlags...)

// ClaudeLaunchEnvKeys lists every variable the Claude account adapter's
// LaunchEnv can set, so a caller holding the env of all providers (the review
// launcher) can keep only Claude's without importing the adapter.
func ClaudeLaunchEnvKeys() []string {
	keys := make([]string, 0, 2+len(ClaudeCredentialEnv))
	keys = append(keys, "CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	return append(keys, ClaudeCredentialEnv...)
}
