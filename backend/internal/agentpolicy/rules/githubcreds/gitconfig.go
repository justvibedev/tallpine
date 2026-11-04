package githubcreds

import (
	"net/url"
	"sort"
	"strings"
)

// EnvGitConfigParameters is git's own channel for command-line configuration
// (`git -c`): a space-separated list of 'key'='value' pairs, each shell-quoted.
// The credential config travels here, not as GIT_CONFIG_COUNT / GIT_CONFIG_KEY_n /
// GIT_CONFIG_VALUE_n, because agent harnesses that sanitize the environment for
// the commands they run (Codex, DeepSeek Harness) drop every variable whose name
// contains KEY, TOKEN, SECRET or PASSWORD: GIT_CONFIG_KEY_n vanished while
// GIT_CONFIG_COUNT stayed, and git failed on every call.
const EnvGitConfigParameters = "GIT_CONFIG_PARAMETERS"

// GitConfigEnv returns the GIT_CONFIG_PARAMETERS variable that makes git ask
// helperCommand for credentials on the given hosts. For each host, an empty
// `credential.<url>.helper` first resets the helpers configured elsewhere for
// that host (so the user's keychain token is not offered), then helperCommand is
// added. Scoping the reset to the host keeps other remotes' credentials working.
//
// existing is the session environment so far: a GIT_CONFIG_PARAMETERS already
// there is kept and ours is appended, and a project's own GIT_CONFIG_COUNT /
// KEY_n / VALUE_n are never touched (git reads those first, so ours still apply
// after them). An empty helperCommand adds only the resets (mode none: no helper
// answers for those hosts). helperCommand is run by git through a shell as
// `<helperCommand> get`, so it must be quoted for sh.
func GitConfigEnv(existing map[string]string, helperCommand string, hosts []string) map[string]string {
	var entries []string
	if prior := strings.TrimSpace(existing[EnvGitConfigParameters]); prior != "" {
		entries = append(entries, prior)
	}
	add := func(key, value string) {
		entries = append(entries, shellQuote(key)+"="+shellQuote(value))
	}
	for _, host := range uniqueSorted(hosts) {
		scope := "credential.https://" + host + ".helper"
		add(scope, "")
		if helperCommand != "" {
			add(scope, "!"+helperCommand)
		}
	}
	if len(entries) == 0 {
		return map[string]string{}
	}
	return map[string]string{EnvGitConfigParameters: strings.Join(entries, " ")}
}

// AppendGitConfig returns existing's GIT_CONFIG_PARAMETERS with each key/value
// pair appended (shell-quoted), keeping what is already there.
func AppendGitConfig(existing map[string]string, pairs ...[2]string) string {
	var entries []string
	if prior := strings.TrimSpace(existing[EnvGitConfigParameters]); prior != "" {
		entries = append(entries, prior)
	}
	for _, kv := range pairs {
		entries = append(entries, shellQuote(kv[0])+"="+shellQuote(kv[1]))
	}
	return strings.Join(entries, " ")
}

// shellQuote single-quotes s for git's GIT_CONFIG_PARAMETERS parser.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func uniqueSorted(hosts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] || strings.ContainsAny(h, "/ \t") {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// HostOf extracts the host of a git remote URL (https, ssh:// or scp-like
// git@host:owner/repo); "" when it has none.
func HostOf(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		return strings.ToLower(u.Hostname())
	}
	if at := strings.Index(remote, "@"); at >= 0 {
		rest := remote[at+1:]
		if colon := strings.Index(rest, ":"); colon > 0 {
			return strings.ToLower(rest[:colon])
		}
	}
	return ""
}
