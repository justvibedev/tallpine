package envfilter

// baseEnvNames are the variables an agent's shell needs to run git, gh, ssh,
// node, go and the common toolchains. They are exact names; entries ending in *
// are prefixes. The list is curated, not exhaustive: anything else is opt-in
// through the policy's passthrough list or the project's own environment.
//
// GitHub tokens are deliberately absent (the githubCreds rule decides them), and so are
// NO_COLOR and TERM/COLORTERM, which the runtimes set themselves.
var baseEnvNames = []string{
	// Identity and shell.
	"PATH", "HOME", "USER", "LOGNAME", "SHELL",
	// Locale and time.
	"LANG", "LANGUAGE", "LC_*", "TZ",
	// Temp dirs (the daemon points these at the session's scratch dir).
	"TMPDIR", "TMP", "TEMP",
	// Display, XDG and the macOS/Linux user session.
	"DISPLAY", "WAYLAND_DISPLAY", "XDG_*", "DBUS_SESSION_BUS_ADDRESS",
	// SSH agent so git over ssh keeps working.
	"SSH_AUTH_SOCK", "SSH_AGENT_PID",
	// Proxies and corporate CAs.
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FTP_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy", "ftp_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO",
	// Git (not GIT_CONFIG_COUNT/KEY/VALUE, which can carry credentials).
	"GIT_SSH", "GIT_SSH_COMMAND", "GIT_EXEC_PATH", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM",
	"GIT_AUTHOR_*", "GIT_COMMITTER_*", "GIT_TERMINAL_PROMPT",
	// Toolchains and version managers.
	"GOPATH", "GOROOT", "GOFLAGS", "GOMODCACHE", "GOCACHE", "GOPROXY", "GOPRIVATE", "GONOSUMDB", "GONOSUMCHECK", "GOTOOLCHAIN",
	"NVM_DIR", "VOLTA_HOME", "PNPM_HOME", "BUN_INSTALL", "FNM_DIR", "ASDF_DIR", "ASDF_DATA_DIR", "MISE_*",
	"CARGO_HOME", "RUSTUP_HOME", "PYENV_ROOT", "VIRTUAL_ENV", "UV_*", "PIPX_*", "JAVA_HOME", "ANDROID_HOME", "ANDROID_SDK_ROOT",
	"HOMEBREW_PREFIX", "HOMEBREW_CELLAR", "HOMEBREW_REPOSITORY",
	// Windows.
	"SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "PATHEXT", "USERPROFILE", "USERNAME", "APPDATA", "LOCALAPPDATA",
	"PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432", "COMMONPROGRAMFILES", "HOMEDRIVE", "HOMEPATH", "OS",
	"PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS", "COMPUTERNAME", "USERDOMAIN",
}

// harnessEnvNames are the variables each supported agent CLI reads for its own
// login and routing, keyed by harness id. They are credentials by nature, so an
// allowlisted agent receives only its own harness's list, never another's (a
// Claude worker does not see OPENAI_*). This is not a sandbox: a harness still
// sees its own credentials. A harness that is empty or not listed here gets the
// union of every list, which is the pre-v2 behaviour.
var harnessEnvNames = map[string][]string{
	// Claude Code, including Bedrock and Vertex routing.
	"claude-code": {
		"ANTHROPIC_*", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_SKIP_*",
		"CLOUD_ML_REGION", "ANTHROPIC_VERTEX_PROJECT_ID",
		"AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK",
		"GOOGLE_APPLICATION_CREDENTIALS",
	},
	"codex":            {"OPENAI_*", "CODEX_*"},
	"kiro":             {"KIRO_*"},
	"deepseek-harness": {"DEEPSEEK_*", "DSH_*"},
	// Not a registered harness id: kept so the unknown-harness union still covers Gemini-style CLIs.
	"gemini": {"GEMINI_*", "GOOGLE_API_KEY"},
	// The fake test harness needs no credentials.
	"fake": nil,
}

// harnessNames returns the credential names harness may receive. strict (the
// hardened profile) gives an empty or unknown harness nothing, not the union.
func harnessNames(harness string, strict bool) []string {
	if names, ok := harnessEnvNames[harness]; ok {
		return names
	}
	if strict {
		return nil
	}
	var all []string
	for _, names := range harnessEnvNames {
		all = append(all, names...)
	}
	return all
}
