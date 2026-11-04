package skillbridge

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/pathx"
)

// Offer IDs.
const (
	OfferClaudeMD     = "claude-md"
	OfferClaudeSkills = "claude-skills"
	OfferCodexMD      = "codex-md"
	OfferCodexSkills  = "codex-skills"
)

// Item is one thing an offer creates, relative to the project checkout.
type Item struct {
	Path string // slash-separated, relative to the checkout
	Kind string // "file" or "link"
	// Target is the relative link target for a link; for a file it is empty and
	// Content holds the text.
	Target  string
	Content string
}

// Offer is a suggested change that makes the project visible to a harness.
type Offer struct {
	ID      string
	Harness string
	Items   []Item
}

// Created reports what ApplyOffer made. Mode is "symlink" or "copy" for links.
type Created struct {
	Path string
	Mode string
}

// errUnsafe means an item would land outside the checkout.
var errUnsafe = errors.New("destination is outside the project checkout")

// symlink is os.Symlink; tests swap it to exercise the copy fallback.
var symlink = os.Symlink

// Offers lists the changes that would make the project's instruction file and
// repo skills visible to each harness in harnesses (claude-code, codex). It
// only reads, and only looks inside the checkout.
func Offers(workspace string, harnesses []string) []Offer {
	return OffersExcept(workspace, harnesses, nil)
}

// OffersExcept is Offers without the skills named in deselected: the user chose
// not to bridge them, so they are neither offered nor created.
func OffersExcept(workspace string, harnesses, deselected []string) []Offer {
	if workspace == "" {
		return nil
	}
	inv := Discover(Options{Workspace: workspace})
	var out []Offer
	for _, h := range harnesses {
		switch h {
		case HarnessClaude:
			if inv.Instructions.AgentsMD && !inv.Instructions.ClaudeMD {
				out = append(out, Offer{ID: OfferClaudeMD, Harness: h, Items: []Item{{Path: "CLAUDE.md", Kind: "file", Content: "@AGENTS.md\n"}}})
			}
			if items := skillLinks(workspace, inv, ConvClaude, deselected); len(items) > 0 {
				out = append(out, Offer{ID: OfferClaudeSkills, Harness: h, Items: items})
			}
		case HarnessCodex:
			if inv.Instructions.RootClaudeMD && !inv.Instructions.AgentsMD {
				out = append(out, Offer{ID: OfferCodexMD, Harness: h, Items: []Item{{Path: "AGENTS.md", Kind: "link", Target: "CLAUDE.md"}}})
			}
			if items := skillLinks(workspace, inv, ConvAgents, deselected); len(items) > 0 {
				out = append(out, Offer{ID: OfferCodexSkills, Harness: h, Items: items})
			}
		}
	}
	return out
}

// destSafe reports whether creating rel inside workspace stays inside the
// checkout: no existing ancestor between the workspace and the item may be a
// symlink (it could point anywhere, for example at ~/.claude/skills), and the
// destination may not sit under a user skill root (~/.claude, ~/.agents,
// ~/.codex), which happens when the project folder is the home directory.
func destSafe(workspace, rel string) bool {
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return false
	}
	cur := workspace
	parts := strings.Split(filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel))), "/")
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			break // everything below is created by us, inside the checkout
		}
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	dest := filepath.Join(root, filepath.FromSlash(rel))
	home, err := os.UserHomeDir()
	if err != nil {
		return true
	}
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	for _, r := range []string{".claude", ".agents", ".codex"} {
		if pathx.Within(filepath.Join(home, r), dest) {
			return false
		}
	}
	return true
}

// skillLinks plans a link in the target convention's skills dir for each repo
// skill that only lives in another convention (the first source wins a name).
func skillLinks(workspace string, inv Inventory, target Convention, deselected []string) []Item {
	skip := map[string]bool{}
	for _, n := range deselected {
		skip[n] = true
	}
	dir := map[Convention]string{ConvClaude: ".claude", ConvAgents: ".agents", ConvCodex: ".codex"}
	have := map[string]bool{}
	for _, s := range inv.Skills {
		if s.Scope == ScopeRepo && s.Convention == target {
			have[s.Name] = true
		}
	}
	var items []Item
	for _, s := range inv.Skills {
		if s.Scope != ScopeRepo || s.Convention == target || have[s.Name] || skip[s.Name] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(workspace, dir[target], "skills", s.Name)); err == nil {
			continue
		}
		if !destSafe(workspace, dir[target]+"/skills/"+s.Name) {
			continue
		}
		have[s.Name] = true
		items = append(items, Item{
			Path:   dir[target] + "/skills/" + s.Name,
			Kind:   "link",
			Target: "../../" + dir[s.Convention] + "/skills/" + s.Name,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items
}

// ApplyOffer creates the items of the named offer in the checkout. It never
// overwrites: an item whose path already exists is skipped. A link falls back
// to a copy when the filesystem refuses symlinks. Nothing is staged or
// committed.
func ApplyOffer(workspace string, harnesses []string, id string) ([]Created, error) {
	return ApplyOfferExcept(workspace, harnesses, nil, id)
}

// ApplyOfferExcept is ApplyOffer for an offer computed without the deselected skills.
func ApplyOfferExcept(workspace string, harnesses, deselected []string, id string) ([]Created, error) {
	for _, o := range OffersExcept(workspace, harnesses, deselected) {
		if o.ID != id {
			continue
		}
		var made []Created
		for _, it := range o.Items {
			c, err := create(workspace, it)
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			if err != nil {
				return made, fmt.Errorf("create %s: %w", it.Path, err)
			}
			made = append(made, c)
		}
		return made, nil
	}
	return nil, fs.ErrNotExist
}

func create(workspace string, it Item) (Created, error) {
	if !destSafe(workspace, it.Path) {
		return Created{}, errUnsafe
	}
	dest := filepath.Join(workspace, filepath.FromSlash(it.Path))
	if _, err := os.Lstat(dest); err == nil {
		return Created{}, fs.ErrExist
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil { //nolint:gosec // project files get normal repo permissions
		return Created{}, err
	}
	if it.Kind == "file" {
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // project files get normal repo permissions
		if err != nil {
			return Created{}, err
		}
		_, werr := f.WriteString(it.Content)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return Created{Path: it.Path, Mode: "file"}, werr
	}
	if err := symlink(filepath.FromSlash(it.Target), dest); err == nil {
		return Created{Path: it.Path, Mode: "symlink"}, nil
	} else if errors.Is(err, fs.ErrExist) {
		return Created{}, err
	}
	src := filepath.Join(filepath.Dir(dest), filepath.FromSlash(it.Target))
	if r, err := filepath.EvalSymlinks(src); err == nil {
		src = r
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return Created{}, err
	}
	if !pathx.Within(root, src) {
		return Created{}, errUnsafe
	}
	if err := copyTree(root, src, dest); err != nil {
		_ = os.RemoveAll(dest)
		return Created{}, err
	}
	return Created{Path: it.Path, Mode: "copy"}, nil
}

// maxCopyDepth bounds symlink resolution so a link cycle cannot recurse forever.
const maxCopyDepth = 8

// copyTree copies src to dst, following symlinks only while their targets stay
// inside root (the resolved checkout). A repo can commit a link to a file
// outside the checkout, such as a private key; copying it in would turn it into
// ordinary tracked content. Such links are skipped.
func copyTree(root, src, dst string) error { return copyTreeDepth(root, src, dst, 0) }

func copyTreeDepth(root, src, dst string, depth int) error {
	if depth > maxCopyDepth {
		return errors.New("skill directory nests symlinks too deeply")
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 && p != src {
			// WalkDir does not descend into symlinks: copy what they point at.
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			if !pathx.Within(root, resolved) {
				return nil
			}
			if st, err := os.Stat(resolved); err == nil && st.IsDir() {
				return copyTreeDepth(root, resolved, out, depth+1)
			}
			return copyFile(resolved, out)
		}
		if d.IsDir() {
			return os.MkdirAll(out, 0o755) //nolint:gosec // project files get normal repo permissions
		}
		return copyFile(p, out)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // user-initiated copy inside the user's own checkout
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // project files get normal repo permissions
	if err != nil {
		return err
	}
	_, err = io.Copy(f, in)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
