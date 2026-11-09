package statefence

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
)

// gitFence is what the fence opens and protects for workspace's git data.
//
// grant is the common git dir, opened for writing below it; private is the
// agent's own private git dir inside it. protect stays read-only: the files the
// owner's unfenced git trusts. siblings are the other worktrees' private dirs,
// which stay read-only too (see siblingGitDirs).
//
// grant comes only from repo, the project repository the daemon has on record
// (see permissioncap.RecordedLinkedGit). Any layout that does not fit gets no
// grant: git fails inside the fence and the fence stays as narrow as it was.
//
// Only what is below grant is opened (allowBelow), never grant itself: a grant
// on the directory entry would let a fenced process swap it for a symlink or
// carry it out, past the denies on config and hooks.
type gitFence struct {
	grant, private string
	protect        []string
	siblings       []string
}

func gitWriteSet(repo, workspace string) gitFence {
	if workspace == "" {
		return gitFence{}
	}
	dotGit := filepath.Join(workspace, ".git")
	st, err := os.Lstat(dotGit)
	if err != nil {
		return gitFence{}
	}
	if st.IsDir() {
		// A plain checkout: its .git is already under the workspace grant. Only
		// denies are derived from it, and a deny cannot widen anything.
		return gitFence{protect: trustedGitPaths(dotGit)}
	}
	common, private := permissioncap.RecordedLinkedGit(repo, workspace)
	if common == "" {
		slog.Warn("statefence: workspace .git does not match the project repository; no git access granted", "workspace", workspace, "repo", repo)
		return gitFence{}
	}
	siblings, ok := siblingGitDirs(private)
	if !ok {
		slog.Warn("statefence: cannot list the repository's other worktrees; no git access granted", "workspace", workspace, "repo", repo)
		return gitFence{}
	}
	// A linked worktree keeps its submodules in its own private dir, so the
	// common dir's modules and config.worktree belong to the main checkout.
	protect := append(trustedGitPaths(common), filepath.Join(common, "config.worktree"), filepath.Join(common, "modules"))
	return gitFence{grant: common, private: private, protect: protect, siblings: siblings}
}

// trustedGitPaths are the files in a git dir that make the unfenced git reading
// it run code. git follows a commondir file even in a main git dir, so planting
// one would point that git at a config and hooks the agent chose.
func trustedGitPaths(gitDir string) []string {
	return []string{filepath.Join(gitDir, "config"), filepath.Join(gitDir, "hooks"), filepath.Join(gitDir, "commondir")}
}

// writeRules emits the git rules. Seatbelt applies the last matching rule, so
// the agent's own private dir is reopened after the denies that also match it,
// and its commondir and directory entry are closed again after that.
//
// The agent's config.worktree stays writable: it is where the git shim sends
// `git config`, so denying it breaks normal git. What it sets applies to every
// git in this checkout, the daemon's included. The daemon turns off what it can
// (ports.GitHardenedArgs); a filter driver has no off switch, so git status and
// diff here can still run one, as TestHardenedGitStillRunsAnAgentsFilterDriver
// records. A plain checkout is not a layout Tallpine launches sessions in today
// (gitworktree hands out linked worktrees), so it gets no worktree denies.
func (g gitFence) writeRules(b *strings.Builder) {
	if g.grant != "" {
		allowBelow(b, g.grant)
		denySiblingWorktrees(b, g.grant, g.siblings)
		denyLaterSessionWorktrees(b, g.grant, filepath.Base(g.private))
		allowBelow(b, g.private)
		rule(b, "deny file-write*", []string{filepath.Join(g.private, "commondir")})
		emitRule(b, "deny file-write*", []string{g.private}, pathForms, literalFilter)
	}
	rule(b, "deny file-write*", g.protect)
}

// denyCheckoutEntries keeps the checkout's .git in place, which unfenced git in
// that checkout follows. Denies on what is inside .git would not be enough:
// renaming .git, or the whole checkout, away and back is a write to that entry
// only. git never moves either, and creating children is unaffected. A .git
// that does not exist yet stays free, so git init keeps working.
func denyCheckoutEntries(b *strings.Builder, workspace string) {
	if workspace == "" {
		return
	}
	emitRule(b, "deny file-write-unlink", []string{workspace}, grantForms, literalFilter)
	dotGit := filepath.Join(workspace, ".git")
	if _, err := os.Lstat(dotGit); err == nil {
		emitRule(b, "deny file-write*", []string{dotGit}, grantForms, literalFilter)
	}
}

// maxSiblingWorktrees bounds the denies one profile carries. The directory is
// under the git grant, so a fenced process can fill it.
const maxSiblingWorktrees = 512

// siblingGitDirs lists the private dirs of the repository's other worktrees.
// Each has a commondir and HEAD that the unfenced git in that worktree trusts,
// and unlike the agent's own checkout its .git file is out of reach, so the git
// grant would be the way in. Only real directories count: a planted link must
// not turn into a deny on its target. A worktree added after launch is not
// listed; denyLaterSessionWorktrees covers the ones Tallpine adds. Failing or
// overflowing reports !ok, so the caller grants nothing.
func siblingGitDirs(private string) ([]string, bool) {
	worktrees := filepath.Dir(private)
	dir, err := os.Open(worktrees)
	if err != nil {
		return nil, false
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(maxSiblingWorktrees + 1)
	if err != nil || len(entries) > maxSiblingWorktrees {
		return nil, false
	}
	own := filepath.Base(private)
	var siblings []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != own {
			siblings = append(siblings, filepath.Join(worktrees, entry.Name()))
		}
	}
	return siblings, true
}

// denySiblingWorktrees keeps other worktrees' private dirs read-only. A path
// deny inside one would not be enough: renaming the dir or worktrees/ away is a
// write to that entry only, and the contents can then be edited and moved back.
// So the entries themselves are denied. Names come from a directory the agent
// can write, so they are never read as globs.
func denySiblingWorktrees(b *strings.Builder, common string, siblings []string) {
	emitRule(b, "deny file-write*", []string{filepath.Join(common, "worktrees")}, pathForms, literalFilter)
	emitRule(b, "deny file-write*", siblings, pathForms, subpathFilter)
}

// sessionWorktreeName splits a Tallpine session's worktree name into the shared
// prefix: <prefix>-<number>, or <prefix>-orchestrator. A trailing digit is the
// suffix git adds when a name collides.
var sessionWorktreeName = regexp.MustCompile(`^(.+)-(?:\d+|orchestrator\d*)$`)

// denyLaterSessionWorktrees denies the private dirs of Tallpine sessions created
// after this launch, by the name shape their worktrees share with own. A profile
// cannot list them, and it cannot cover every later worktree either: git
// worktree add needs to create a new private dir and worktree remove to delete
// one, and to seatbelt deleting a commondir and writing a new one is the same
// writes as planting one. So a worktree the owner names by hand after launch
// stays writable until the next launch; only this one shape is denied, and an
// agent's own git worktree add under any other name keeps working.
func denyLaterSessionWorktrees(b *strings.Builder, common, own string) {
	m := sessionWorktreeName.FindStringSubmatch(own)
	if m == nil {
		return
	}
	name := regexp.QuoteMeta(m[1]) + "-(orchestrator[0-9]*|[0-9]+)(/|$)"
	emitRule(b, "deny file-write*", []string{filepath.Join(common, "worktrees")}, pathForms, func(p string) string {
		return "(regex " + quote("^"+regexp.QuoteMeta(p)+"/"+name) + ")"
	})
}

func literalFilter(p string) string { return "(literal " + quote(p) + ")" }

func subpathFilter(p string) string { return "(subpath " + quote(p) + ")" }
