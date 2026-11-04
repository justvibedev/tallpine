package controllers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/bodylimit"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

func (c *SessionsController) listWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/files")
		return
	}
	files, err := c.Svc.ListWorkspaceFiles(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceFilesResponse(files))
}

func (c *SessionsController) getWorkspaceManifest(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/manifest")
		return
	}
	manifest, err := c.Svc.GetWorkspaceManifest(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceManifestResponse(manifest))
}

func (c *SessionsController) getWorkspaceHistory(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/history")
		return
	}
	view, err := workspaceViewQuery(r.URL.Query())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	history, err := c.Svc.GetWorkspaceHistory(r.Context(), sessionID(r), view)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceHistoryResponse(history))
}

func (c *SessionsController) getWorkspaceCommitSelection(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/workspace/history/selection")
		return
	}
	var in WorkspaceCommitSelectionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	view := workspaceView(in.Branch, in.PRNumber, in.SourceURL, in.ExpectedTipSHA, in.ExpectedBaseSHA)
	selection, err := c.Svc.GetWorkspaceCommitSelection(r.Context(), sessionID(r), view, in.CommitSHAs, strings.TrimSpace(in.WorkspaceVersion))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceCommitSelectionResponse(selection))
}

func (c *SessionsController) listWorkspaceBranches(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/branches")
		return
	}
	branches, err := c.Svc.ListWorkspaceBranches(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceBranchesResponse(branches))
}

// workspaceView builds the view a JSON body selects. The service validates it.
func workspaceView(branch string, prNumber int, sourceURL, expectedTipSHA, expectedBaseSHA string) sessionsvc.WorkspaceView {
	return sessionsvc.WorkspaceView{
		Branch:          strings.TrimSpace(branch),
		PRNumber:        prNumber,
		SourceURL:       strings.TrimSpace(sourceURL),
		ExpectedTipSHA:  strings.TrimSpace(expectedTipSHA),
		ExpectedBaseSHA: strings.TrimSpace(expectedBaseSHA),
	}
}

// workspaceViewQuery parses the branch or pull request a workspace read
// selects. It only parses; the service validates the selection.
func workspaceViewQuery(query url.Values) (sessionsvc.WorkspaceView, error) {
	view := workspaceView(query.Get("branch"), 0, query.Get("sourceUrl"), query.Get("expectedTipSha"), query.Get("expectedBaseSha"))
	if query.Has("branch") && view.Branch == "" {
		return sessionsvc.WorkspaceView{}, apierr.Invalid("INVALID_WORKSPACE_BRANCH", "branch must not be empty", nil)
	}
	if raw := strings.TrimSpace(query.Get("prNumber")); raw != "" {
		number, err := strconv.Atoi(raw)
		if err != nil || number <= 0 {
			return sessionsvc.WorkspaceView{}, apierr.Invalid("INVALID_WORKSPACE_VIEW", "prNumber must be a positive integer", nil)
		}
		view.PRNumber = number
	}
	return view, nil
}

func (c *SessionsController) getWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/file")
		return
	}
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if relPath == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "WORKSPACE_PATH_REQUIRED", "path is required", nil)
		return
	}
	section := sessionsvc.WorkspaceFileSection(strings.TrimSpace(r.URL.Query().Get("section")))
	commitSHA := strings.TrimSpace(r.URL.Query().Get("commitSha"))
	var file sessionsvc.WorkspaceFileDetail
	var err error
	if workspaceSnapshotRequested(r.URL.Query()) {
		input, parseErr := workspaceSnapshotQuery(r.URL.Query())
		if parseErr != nil {
			envelope.WriteError(w, r, parseErr)
			return
		}
		file, err = c.Svc.GetWorkspaceSnapshotFile(r.Context(), sessionID(r), input)
	} else if commitSHA != "" {
		file, err = c.Svc.GetWorkspaceFileAtCommit(r.Context(), sessionID(r), relPath, commitSHA)
	} else {
		file, err = c.Svc.GetWorkspaceFile(r.Context(), sessionID(r), relPath, section)
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceFileResponse(file))
}

func (c *SessionsController) updateWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/workspace/file")
		return
	}
	bodylimit.Raise(w, r, maxWorkspaceFileBody)
	var in UpdateWorkspaceFileRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.Path) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "WORKSPACE_PATH_REQUIRED", "path is required", nil)
		return
	}
	file, err := c.Svc.UpdateWorkspaceFile(r.Context(), sessionID(r), sessionsvc.UpdateWorkspaceFileInput{
		Path:                    in.Path,
		Content:                 in.Content,
		ExpectedFileFingerprint: strings.TrimSpace(in.ExpectedFileFingerprint),
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceFileResponse(file))
}

func (c *SessionsController) listPRFiles(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/pr/{prNumber}/files")
		return
	}
	number, err := strconv.Atoi(chi.URLParam(r, "prNumber"))
	if err != nil || number <= 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR_NUMBER", "prNumber must be a positive integer", nil)
		return
	}
	files, err := c.Svc.ListPRFiles(r.Context(), sessionID(r), number, strings.TrimSpace(r.URL.Query().Get("sourceUrl")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, prFilesResponse(files))
}

func (c *SessionsController) getPRFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/pr/{prNumber}/file")
		return
	}
	number, err := strconv.Atoi(chi.URLParam(r, "prNumber"))
	if err != nil || number <= 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR_NUMBER", "prNumber must be a positive integer", nil)
		return
	}
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if relPath == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "WORKSPACE_PATH_REQUIRED", "path is required", nil)
		return
	}
	query := r.URL.Query()
	var previousPath *string
	if query.Has("previousPath") {
		value := strings.TrimSpace(query.Get("previousPath"))
		previousPath = &value
	}
	var file sessionsvc.WorkspaceFileDetail
	if commitSHA := strings.TrimSpace(query.Get("commitSha")); commitSHA != "" {
		file, err = c.Svc.GetPRFileAtCommit(r.Context(), sessionID(r), number, strings.TrimSpace(query.Get("sourceUrl")), relPath, commitSHA)
	} else {
		file, err = c.Svc.GetPRFile(r.Context(), sessionID(r), number, strings.TrimSpace(query.Get("sourceUrl")), relPath, previousPath)
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceFileResponse(file))
}

func (c *SessionsController) getPRFileRevision(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/pr/{prNumber}/file/revision")
		return
	}
	number, err := strconv.Atoi(chi.URLParam(r, "prNumber"))
	if err != nil || number <= 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR_NUMBER", "prNumber must be a positive integer", nil)
		return
	}
	query := r.URL.Query()
	relPath := strings.TrimSpace(query.Get("path"))
	if relPath == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "WORKSPACE_PATH_REQUIRED", "path is required", nil)
		return
	}
	side := sessionsvc.WorkspaceFileBlobSide(strings.TrimSpace(query.Get("side")))
	if side == "" {
		side = sessionsvc.WorkspaceBlobAfter
	}
	var revision sessionsvc.WorkspaceFileRevision
	if commitSHA := strings.TrimSpace(query.Get("commitSha")); commitSHA != "" {
		revision, err = c.Svc.GetPRFileRevisionAtCommit(r.Context(), sessionID(r), number, strings.TrimSpace(query.Get("sourceUrl")), relPath, side, commitSHA)
	} else {
		revision, err = c.Svc.GetPRFileRevision(r.Context(), sessionID(r), number, strings.TrimSpace(query.Get("sourceUrl")), relPath, side)
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	envelope.WriteJSON(w, http.StatusOK, workspaceFileRevisionResponse(revision))
}

func (c *SessionsController) getWorkspaceDiffs(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/workspace/diffs")
		return
	}
	var in WorkspaceDiffRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	diffs, err := c.Svc.GetWorkspaceDiffs(r.Context(), sessionID(r), sessionsvc.WorkspaceDiffInput{
		Scope:            sessionsvc.WorkspaceDiffScope(in.Scope),
		Paths:            in.Paths,
		ContextLines:     in.ContextLines,
		IgnoreWhitespace: in.IgnoreWhitespace,
		WorkspaceVersion: in.WorkspaceVersion,
		CommitSHA:        strings.TrimSpace(in.CommitSHA),
		CommitSHAs:       in.CommitSHAs,
		View:             workspaceView(in.Branch, in.PRNumber, in.SourceURL, in.ExpectedTipSHA, in.ExpectedBaseSHA),
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceDiffsResponse(diffs))
}

func (c *SessionsController) getWorkspaceFileRevision(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/file/revision")
		return
	}
	query := r.URL.Query()
	relPath := strings.TrimSpace(query.Get("path"))
	if relPath == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "WORKSPACE_PATH_REQUIRED", "path is required", nil)
		return
	}
	side := sessionsvc.WorkspaceFileBlobSide(strings.TrimSpace(query.Get("side")))
	if side == "" {
		side = sessionsvc.WorkspaceBlobAfter
	}
	view, err := workspaceViewQuery(query)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	commitSHA := strings.TrimSpace(query.Get("commitSha"))
	var commitSHAs []string
	for _, sha := range strings.Split(query.Get("commitShas"), ",") {
		if sha = strings.TrimSpace(sha); sha != "" {
			commitSHAs = append(commitSHAs, sha)
		}
	}
	scope := sessionsvc.WorkspaceDiffScope(strings.TrimSpace(query.Get("scope")))
	workspaceVersion := strings.TrimSpace(query.Get("workspaceVersion"))
	expectedRevision := strings.TrimSpace(query.Get("expectedRevision"))
	var revision sessionsvc.WorkspaceFileRevision
	// A branch or pull request view, or ticked commits, read a commit
	// selection; anything else reads the checked-out comparison as before.
	if query.Get("snapshot") == "true" || view.Branch != "" || view.PRNumber != 0 || view.SourceURL != "" || commitSHA != "" || len(commitSHAs) > 0 {
		revision, err = c.Svc.GetWorkspaceSelectionFileRevision(r.Context(), sessionID(r), sessionsvc.WorkspaceSelectionRevisionInput{
			Snapshot: query.Get("snapshot") == "true", Path: relPath, Side: side, Scope: scope, View: view, CommitSHA: commitSHA, CommitSHAs: commitSHAs, WorkspaceVersion: workspaceVersion, ExpectedRevision: expectedRevision,
		})
	} else {
		revision, err = c.Svc.GetWorkspaceFileRevision(r.Context(), sessionID(r), relPath, scope, side, workspaceVersion, expectedRevision)
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	envelope.WriteJSON(w, http.StatusOK, workspaceFileRevisionResponse(revision))
}

func (c *SessionsController) searchWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/search")
		return
	}
	query := r.URL.Query()
	limit := 0
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_WORKSPACE_SEARCH_LIMIT", "limit is invalid", nil)
			return
		}
		limit = parsed
	}
	result, err := c.Svc.SearchWorkspaceFiles(r.Context(), sessionID(r), query.Get("query"), query.Get("cursor"), limit)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceFileSearchResponse(result))
}

// resolveWorkspacePath classifies a path from chat as a workspace file, a
// session artifact, or neither, so the renderer can open it without knowing
// where the session's worktree lives.
func (c *SessionsController) resolveWorkspacePath(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/resolve")
		return
	}
	result, err := c.Svc.ResolveWorkspacePath(r.Context(), sessionID(r), r.URL.Query().Get("path"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkspacePathResolutionResponse{
		SessionID: result.SessionID,
		Kind:      result.Kind,
		Path:      result.Path,
	})
}

// listWorkspaceTree returns one directory level of the session workspace's
// full file tree, git-status decorated. Unlike listWorkspaceFiles (changed
// files only), path is optional: an empty or missing path lists the
// workspace root.
func (c *SessionsController) listWorkspaceTree(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/tree")
		return
	}
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	var tree sessionsvc.WorkspaceTree
	var err error
	if workspaceSnapshotRequested(r.URL.Query()) || r.URL.Query().Has("commitSha") {
		input, parseErr := workspaceSnapshotQuery(r.URL.Query())
		if parseErr != nil {
			envelope.WriteError(w, r, parseErr)
			return
		}
		tree, err = c.Svc.ListWorkspaceSnapshotTree(r.Context(), sessionID(r), input, r.URL.Query().Get("filter"))
	} else {
		tree, err = c.Svc.ListWorkspaceTree(r.Context(), sessionID(r), relPath)
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceTreeResponse(tree))
}

// getWorkspaceFileBlob streams one side of an image file's diff. The renderer
// points an <img> straight at this route, so the response is the raw bytes with
// their media type rather than a JSON envelope.
func (c *SessionsController) getWorkspaceFileBlob(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/file/blob")
		return
	}
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if relPath == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "WORKSPACE_PATH_REQUIRED", "path is required", nil)
		return
	}
	side := sessionsvc.WorkspaceFileBlobSide(strings.TrimSpace(r.URL.Query().Get("side")))
	if side == "" {
		side = sessionsvc.WorkspaceBlobAfter
	}
	var blob sessionsvc.WorkspaceFileBlob
	var err error
	if workspaceSnapshotRequested(r.URL.Query()) || r.URL.Query().Has("commitSha") {
		input, parseErr := workspaceSnapshotQuery(r.URL.Query())
		if parseErr != nil {
			envelope.WriteError(w, r, parseErr)
			return
		}
		input.Side = side
		blob, err = c.Svc.GetWorkspaceSnapshotBlob(r.Context(), sessionID(r), input)
	} else {
		blob, err = c.Svc.GetWorkspaceFileBlob(r.Context(), sessionID(r), relPath, side)
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", blob.MediaType)
	h.Set("Content-Length", strconv.Itoa(len(blob.Data)))
	// The worktree changes under the viewer, and the URL carries no content
	// hash, so a cached response would show a stale image after every edit.
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob.Data)
}

// workspaceSnapshotQuery shares the review's source and selection validation.
func workspaceSnapshotQuery(query url.Values) (sessionsvc.WorkspaceSelectionRevisionInput, error) {
	view, err := workspaceViewQuery(query)
	if err != nil {
		return sessionsvc.WorkspaceSelectionRevisionInput{}, err
	}
	return sessionsvc.WorkspaceSelectionRevisionInput{Path: query.Get("path"), View: view, CommitSHA: query.Get("commitSha"), CommitSHAs: strings.FieldsFunc(query.Get("commitShas"), func(r rune) bool { return r == ',' })}, nil
}

// Source selectors must never be silently answered with mutable worktree data.
func workspaceSnapshotRequested(query url.Values) bool {
	return query.Get("snapshot") == "true" || query.Has("branch") || query.Has("prNumber") || query.Has("sourceUrl") || query.Has("expectedTipSha") || query.Has("expectedBaseSha") || query.Has("commitShas")
}

// maxWorkspaceFileBody bounds a workspace file edit: the 256 KiB content cap
// with generous room for JSON escaping.
const maxWorkspaceFileBody = 2 << 20
