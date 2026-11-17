package skillbridge

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/process"
)

// LSPMarketplace is the marketplace the official code-intelligence plugins ship in.
const LSPMarketplace = "claude-plugins-official"

// LSPLanguage maps a language to its official Claude Code LSP plugin and the
// language server that plugin needs on PATH. BinaryInstall is shown for the
// user to copy; BinaryCommands are the only commands the daemon runs to install
// Binary (LSPInstaller), so a client can only pick an entry, never a command.
type LSPLanguage struct {
	Language   string
	Extensions []string
	Plugin     string // plugin name, without the marketplace
	Binary     string
	// BinaryInstall is the command (or note) that installs Binary.
	BinaryInstall string
	// BinaryCommands are alternatives, tried in order: the first whose tool is
	// installed runs. Empty means the daemon cannot install Binary.
	BinaryCommands []InstallCommand
}

// InstallCommand is one fixed argv: Tool resolved on PATH, then Args.
type InstallCommand struct {
	Tool string
	Args []string
}

// GoToolchainInstall is the command (or note) that installs Go for the host OS
// and, where Tallpine can run it unattended, the fixed argv to do so.
func GoToolchainInstall(goos string) (string, []InstallCommand) {
	if goos == "darwin" {
		return "brew install go", []InstallCommand{{"brew", []string{"install", "go"}}}
	}
	return "See https://go.dev/doc/install", nil
}

// LSPMarketplaceSource is the GitHub repo of LSPMarketplace, added when the
// account does not know the marketplace yet.
const LSPMarketplaceSource = "anthropics/claude-plugins-official"

// PluginID is the enabledPlugins key of the official plugin.
func (l LSPLanguage) PluginID() string { return l.Plugin + "@" + LSPMarketplace }

// PluginInstall is the command that installs the plugin.
func (l LSPLanguage) PluginInstall() string {
	return "claude plugin install " + l.PluginID()
}

// LSPCatalog lists the supported languages in display order.
var LSPCatalog = []LSPLanguage{
	{"typescript", []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}, "typescript-lsp", "typescript-language-server", "npm install -g typescript-language-server typescript@5",
		[]InstallCommand{{"npm", []string{"install", "-g", "typescript-language-server", "typescript@5"}}}},
	{"go", []string{".go"}, "gopls-lsp", "gopls", "go install golang.org/x/tools/gopls@latest",
		[]InstallCommand{{"go", []string{"install", "golang.org/x/tools/gopls@latest"}}, {"brew", []string{"install", "gopls"}}}},
	// sourcekit-lsp ships with Xcode or the Swift toolchain; there is nothing to run unattended.
	{"swift", []string{".swift"}, "swift-lsp", "sourcekit-lsp", "xcode-select --install   # sourcekit-lsp ships with the Swift toolchain", nil},
	{"python", []string{".py", ".pyi"}, "pyright-lsp", "pyright-langserver", "npm install -g pyright",
		[]InstallCommand{{"npm", []string{"install", "-g", "pyright"}}, {"brew", []string{"install", "pyright"}}}},
	{"kotlin", []string{".kt", ".kts"}, "kotlin-lsp", "kotlin-lsp", "brew install JetBrains/utils/kotlin-lsp",
		[]InstallCommand{{"brew", []string{"install", "JetBrains/utils/kotlin-lsp"}}}},
}

// LSPByPluginName finds the catalog entry of a plugin name (any marketplace).
func LSPByPluginName(name string) (LSPLanguage, bool) {
	for _, l := range LSPCatalog {
		if l.Plugin == name {
			return l, true
		}
	}
	return LSPLanguage{}, false
}

// LSPByLanguage finds the catalog entry of a language.
func LSPByLanguage(language string) (LSPLanguage, bool) {
	for _, l := range LSPCatalog {
		if l.Language == language {
			return l, true
		}
	}
	return LSPLanguage{}, false
}

// LangCount is how many tracked source files a language has.
type LangCount struct {
	Language string
	Files    int
}

// Thresholds for a language to count as one of the repo's main languages: at
// least lspMinFiles files and lspMinShare of the files of all catalog languages.
const (
	lspMinFiles = 5
	lspMinShare = 0.10
)

var lspSkipDirs = map[string]bool{"node_modules": true, "vendor": true, "third_party": true, "Pods": true, ".build": true, "dist": true, "build": true}

// CountLanguages counts catalog-language files among tracked paths, skipping
// vendored directories.
func CountLanguages(paths []string) map[string]int {
	byExt := map[string]string{}
	for _, l := range LSPCatalog {
		for _, e := range l.Extensions {
			byExt[e] = l.Language
		}
	}
	counts := map[string]int{}
	for _, p := range paths {
		lang, ok := byExt[strings.ToLower(path.Ext(p))]
		if !ok || vendored(p) {
			continue
		}
		counts[lang]++
	}
	return counts
}

func vendored(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if lspSkipDirs[seg] {
			return true
		}
	}
	return false
}

// MainLanguages picks the languages that make up the repo, most files first.
func MainLanguages(counts map[string]int) []LangCount {
	total := 0
	for _, n := range counts {
		total += n
	}
	var out []LangCount
	for _, l := range LSPCatalog {
		n := counts[l.Language]
		if n >= lspMinFiles && float64(n) >= lspMinShare*float64(total) {
			out = append(out, LangCount{l.Language, n})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Files > out[b].Files })
	return out
}

// DetectLanguages counts the tracked files of workspace's checkout and returns
// its main languages. Outside a git repository it returns nil.
func DetectLanguages(workspace string) []LangCount {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := process.CommandContext(ctx, "git", ports.GitInDir(workspace, "ls-files", "-z")...) //nolint:gosec // fixed git subcommand on the user's own checkout
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return nil
	}
	var paths []string
	for _, p := range strings.Split(out.String(), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return MainLanguages(CountLanguages(paths))
}

// BinaryInstalled reports whether a language server binary is on PATH or in
// the usual tool directories a GUI-launched daemon's PATH misses.
func BinaryInstalled(home, name string) bool {
	_, ok := LookTool(home, name)
	return ok
}

// lookTool is LookTool; tests replace it.
var lookTool = LookTool

// Codes BinaryProblem returns; the UI translates them.
const (
	LSPProblemNoGo       = "no-go"
	LSPProblemNoTSServer = "no-tsserver"
)

// LSPProblemText is the English sentence for a BinaryProblem code (job errors).
func LSPProblemText(code string) string {
	switch code {
	case LSPProblemNoGo:
		return "gopls needs a Go toolchain: no go was found on PATH, so it fails with \"no views\". Install Go (brew install go)."
	case LSPProblemNoTSServer:
		return "The installed TypeScript has no tsserver.js (TypeScript 7 or newer), so typescript-language-server cannot start. Run: npm install -g typescript@5"
	}
	return code
}

// BinaryProblem returns a Problem code for an installed language server that cannot work, or "" when
// nothing is known to be wrong. It does not report a missing binary
// (BinaryInstalled does). Two known cases: gopls cannot load packages without
// a go on PATH ("no views"), and a typescript package without lib/tsserver.js
// (TypeScript 7+) leaves typescript-language-server unable to start.
func BinaryProblem(home string, l LSPLanguage) string {
	bin, ok := lookTool(home, l.Binary)
	if !ok {
		return ""
	}
	switch l.Binary {
	case "gopls":
		if _, found := lookTool(home, "go"); !found {
			return LSPProblemNoGo
		}
	case "typescript-language-server":
		if tsWithoutServer(bin) {
			return LSPProblemNoTSServer
		}
	}
	return ""
}

// tsWithoutServer reports whether a typescript package sits next to (or inside)
// the language server's node_modules but lacks lib/tsserver.js.
func tsWithoutServer(bin string) bool {
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return false
	}
	for dir := filepath.Dir(resolved); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) != "node_modules" {
			continue
		}
		ts := filepath.Join(dir, "typescript")
		if _, err := os.Stat(filepath.Join(ts, "package.json")); err != nil {
			continue
		}
		_, err := os.Stat(filepath.Join(ts, "lib", "tsserver.js"))
		return err != nil
	}
	return false
}

// LookTool finds an executable on PATH or in the usual tool directories
// (toolDirs) a GUI-launched daemon's PATH misses.
func LookTool(home, name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, d := range toolDirs(home) {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// toolDirs are where Homebrew, go install, npm and friends put executables.
func toolDirs(home string) []string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, "go", "bin"), filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".cargo", "bin"))
	}
	return dirs
}
