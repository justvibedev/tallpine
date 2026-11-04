package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// Add registers a local git repository as a project.
//
// The whole method body is serialised by addMu because workspace registration
// mutates the filesystem (git init, .gitignore, commits) between the conflict
// check and the store write — two concurrent calls for the same path would both
// pass FindProjectByPath and then race on those mutations.
func (m *Service) Add(ctx context.Context, in AddInput) (Project, error) {
	path, err := normalizePath(in.Path)
	if err != nil {
		return Project{}, err
	}
	id := defaultProjectID(path)
	if in.ProjectID != nil {
		id = domain.ProjectID(strings.TrimSpace(*in.ProjectID))
	}
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}

	m.addMu.Lock()
	defer m.addMu.Unlock()

	activeProjects, err := m.store.ListProjects(ctx)
	if err != nil {
		return Project{}, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load project")
	}

	name := string(id)
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if utf8.RuneCountInString(name) > maxDisplayNameLen {
			return Project{}, apierr.Invalid("DISPLAY_NAME_TOO_LONG", fmt.Sprintf("Display name must be %d characters or fewer", maxDisplayNameLen), nil)
		}
	}
	if name == "" {
		name = string(id)
	}
	if utf8.RuneCountInString(name) > maxDisplayNameLen {
		runes := []rune(name)
		name = string(runes[:maxDisplayNameLen])
	}

	var creators creatorStore
	if in.CreatedBy != "" {
		var rejection error
		if creators, id, rejection = m.checkAgentCreate(ctx, in, path, id); rejection != nil {
			return Project{}, rejection
		}
	}

	existing, registered, err := m.store.FindProjectByPath(ctx, path)
	if err != nil {
		return Project{}, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !registered {
		// Existing records retain the user's chosen path. Compare directory
		// identity as well, so aliases (including macOS /tmp and /private/tmp)
		// cannot register the same repository under a second project ID.
		if selectedInfo, statErr := os.Stat(path); statErr == nil && selectedInfo.IsDir() {
			for _, candidate := range activeProjects {
				registeredInfo, statErr := os.Stat(candidate.Path)
				if statErr == nil && os.SameFile(selectedInfo, registeredInfo) {
					existing, registered = candidate, true
					break
				}
			}
		}
	}
	if registered {
		return Project{}, apierr.Conflict("PATH_ALREADY_REGISTERED", "A project at this path is already registered", map[string]any{
			"existingProjectId":  existing.ID,
			"suggestedProjectId": string(m.suggestID(ctx, id)),
		})
	}

	if in.ClonePreparationID != "" {
		markerID, exists, markerErr := readClonePreparationID(path)
		if markerErr != nil {
			return Project{}, apierr.Invalid("CLONE_PREPARATION_FAILED", "The prepared clone could not be inspected.", map[string]any{"path": path})
		}
		if !exists || !sameClonePreparationID(markerID, in.ClonePreparationID) {
			return Project{}, apierr.Conflict("CLONE_PREPARATION_MISMATCH", "This checkout belongs to a different clone preparation.", map[string]any{"path": path})
		}
	}

	if existing, ok, err := m.store.GetProject(ctx, string(id)); err != nil {
		return Project{}, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load project")
	} else if ok && existing.ArchivedAt.IsZero() && existing.Path != path {
		if in.ProjectID != nil {
			return Project{}, apierr.Conflict("ID_ALREADY_REGISTERED", "A project with this id is already registered for a different path", map[string]any{
				"existingProjectId":  existing.ID,
				"suggestedProjectId": string(m.suggestID(ctx, id)),
			})
		}
		// A caller that omitted projectId asked the service to derive one from
		// the basename. Resolve that derived collision here; explicit IDs retain
		// their conflict semantics so user intent is never silently rewritten.
		id = m.suggestID(ctx, id)
	}

	var projectConfig domain.ProjectConfig
	if in.Config != nil {
		if err := in.Config.Validate(); err != nil {
			return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
		}
		if err := in.Config.ValidateAgentRulesCap(""); err != nil {
			return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
		}
		projectConfig = *in.Config
	}

	registeredAt := time.Now()
	row := domain.ProjectRecord{
		ID:           string(id),
		Path:         path,
		DisplayName:  name,
		RegisteredAt: registeredAt,
		Kind:         domain.ProjectKindSingleRepo,
		Config:       projectConfig,
	}
	if in.AsWorkspace {
		repos, err := prepareWorkspaceProject(ctx, path, domain.ProjectID(row.ID), registeredAt)
		if err != nil {
			return Project{}, err
		}
		row.Kind = domain.ProjectKindWorkspace
		row.RepoOriginURL = resolveGitOriginURL(path)
		if err := row.Config.ValidateCanonicalRepository(row.RepoOriginURL); err != nil {
			return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
		}
		if err := m.store.UpsertWorkspaceProject(ctx, row, repos); err != nil {
			return Project{}, projectStoreError(err, "PROJECT_ADD_FAILED", "Failed to register workspace project")
		}
		if in.ClonePreparationID != "" {
			removeClonePreparationMarker(path)
		}
		p := m.projectFromRow(ctx, row)
		p.WorkspaceRepos = workspaceReposFromRecords(row.Path, repos)
		return p, nil
	}
	// The three repository probes are independent git subprocesses; run them
	// concurrently so registration pays one wave instead of three serial
	// spawns. Error precedence stays sequential below (not-a-repo wins over
	// unborn), preserving the existing error contract.
	var (
		isRepo    bool
		hasCommit bool
		originURL string
	)
	var probes sync.WaitGroup
	probes.Add(3)
	go func() {
		defer probes.Done()
		isRepo = isGitRepo(path)
	}()
	go func() {
		defer probes.Done()
		hasCommit = repoHasCommit(ctx, path)
	}()
	go func() {
		defer probes.Done()
		originURL = resolveGitOriginURL(path)
	}()
	probes.Wait()
	if !isRepo {
		return Project{}, apierr.Invalid("NOT_A_GIT_REPO", "Tallpine needs a Git repository with an initial commit before it can create agent workspaces.", nil)
	}
	if !hasCommit {
		return Project{}, apierr.Invalid("PROJECT_UNBORN", "Tallpine needs a Git repository with an initial commit before it can create agent workspaces.", map[string]any{
			"path":         path,
			"suggestedFix": "Run `git commit --allow-empty -m \"initial commit\"` in this folder, then try again.",
		})
	}
	row.RepoOriginURL = originURL
	if err := row.Config.ValidateCanonicalRepository(row.RepoOriginURL); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	if err := m.store.UpsertProject(ctx, row); err != nil {
		return Project{}, projectStoreError(err, "PROJECT_ADD_FAILED", "Failed to register project")
	}
	if creators != nil {
		if err := creators.SetProjectCreator(ctx, id, in.CreatedBy, time.Now()); err != nil {
			// A project without its creator row would be unmanageable and look
			// user-registered: take the registration back.
			if _, archiveErr := m.store.ArchiveProject(ctx, string(id), time.Now()); archiveErr != nil {
				m.logger.Error("project: undo registration after creator failure", "project", id, "error", archiveErr)
			}
			return Project{}, projectStoreError(err, "PROJECT_ADD_FAILED", "Failed to register project")
		}
	}
	if in.ClonePreparationID != "" {
		removeClonePreparationMarker(path)
	}
	return m.projectFromRow(ctx, row), nil
}

func (m *Service) suggestID(ctx context.Context, base domain.ProjectID) domain.ProjectID {
	for i := 1; ; i++ {
		candidate := domain.ProjectID(string(base) + strconv.Itoa(i))
		if _, ok, _ := m.store.GetProject(ctx, string(candidate)); !ok {
			return candidate
		}
	}
}

// defaultProjectID derives a valid project id from the folder name, so any
// folder can be added without typing an id: accents fold to plain letters
// ("café" -> "cafe"), every other run of characters the id alphabet rejects
// becomes one "-" ("app (v2)" -> "app-v2"), and a name with nothing usable
// left falls back to "project".
func defaultProjectID(path string) domain.ProjectID {
	name := norm.NFD.String(strings.ToLower(strings.TrimSpace(filepath.Base(path))))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.Is(unicode.Mn, r):
			// Combining accent split off by NFD.
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		case !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	id := b.String()
	for strings.Contains(id, "..") {
		id = strings.ReplaceAll(id, "..", ".")
	}
	id = strings.TrimLeftFunc(id, func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') })
	id = strings.TrimRight(id, "-")
	if id == "" {
		id = "project"
	}
	return domain.ProjectID(id)
}

var projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateProjectID(id domain.ProjectID) error {
	raw := string(id)
	// Reject any "." run: a "." prefix fails the pattern, but an embedded ".."
	// (e.g. "a..b") passes it yet yields a branch like "tp/a..b-1" that git's
	// check-ref-format rejects — surfacing as an opaque 500 at spawn time.
	if raw == "" || raw == "." || strings.Contains(raw, "..") || strings.ContainsAny(raw, `/\`) || !projectIDPattern.MatchString(raw) {
		return apierr.Invalid("INVALID_PROJECT_ID", "Project id failed storage-path validation", nil)
	}
	return nil
}

// resolveSessionPrefix prefers an explicit per-project SessionPrefix and falls
// back to the id-derived prefix. (Display only; session-id generation is
// unchanged.)
func resolveSessionPrefix(row domain.ProjectRecord) string {
	if p := strings.TrimSpace(row.Config.SessionPrefix); p != "" {
		return p
	}
	return sessionPrefix(row.ID)
}

func sessionPrefix(id string) string {
	if id == "" {
		return "tp"
	}
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
