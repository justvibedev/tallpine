package controllers

import (
	"tallpine.eu/tallpine/backend/internal/devimport"
	"tallpine.eu/tallpine/backend/internal/officialimport"
)

// ListDirsQuery is the query string accepted by GET /api/v1/fs/dirs.
type ListDirsQuery struct {
	Path string `query:"path,omitempty" description:"Absolute directory on the daemon host to list. When omitted, the daemon user's home directory."`
}

// FSEntry is one directory in a /api/v1/fs/dirs listing.
type FSEntry struct {
	Name    string `json:"name" description:"Directory name."`
	Path    string `json:"path" description:"Absolute path of the directory on the daemon host."`
	GitRepo bool   `json:"gitRepo" description:"True when the directory carries a .git entry (clone or worktree checkout)."`
}

// ListDirsResponse is the body of GET /api/v1/fs/dirs.
type ListDirsResponse struct {
	Path      string    `json:"path" description:"Absolute path that was listed."`
	Parent    string    `json:"parent" description:"Absolute path of the listed directory's parent; equals path at the filesystem root."`
	Entries   []FSEntry `json:"entries" description:"Subdirectories, excluding dotted names."`
	Truncated bool      `json:"truncated,omitempty" description:"True when the listing hit the entry cap and more subdirectories exist."`
}

// DevImportProjectsResponse is the body of POST /api/v1/dev/import-projects.
type DevImportProjectsResponse struct {
	Report devimport.Report `json:"report"`
}

// AOImportResponse is the body of POST /api/v1/imports/ao.
type AOImportResponse struct {
	Report officialimport.Report `json:"report"`
}

// PRIDParam is the {id} path parameter shared by the /prs/{id} routes.
type PRIDParam struct {
	ID string `path:"id" description:"PR number."`
}

// ResolveCommentsRequest is the optional body of POST /api/v1/prs/{id}/resolve-comments.
type ResolveCommentsRequest struct {
	// CommentIDs accepts provider comment ids and review thread ids. Comment
	// ids are mapped to their owning thread before resolving.
	CommentIDs []string `json:"commentIds,omitempty"`
}
