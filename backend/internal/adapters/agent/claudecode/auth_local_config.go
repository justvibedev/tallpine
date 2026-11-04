package claudecode

// claudeConfigPathFor is the global config file a Claude launched with env
// reads: <CLAUDE_CONFIG_DIR>/.claude.json for an account or project folder,
// ~/.claude.json otherwise.
func claudeConfigPathFor(env map[string]string) (string, error) {
	_, path, err := claudeLaunchConfigPaths(env)
	return path, err
}
