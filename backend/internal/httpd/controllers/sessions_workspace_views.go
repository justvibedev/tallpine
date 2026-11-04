package controllers

import (
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

func workspaceFilesResponse(files sessionsvc.WorkspaceFiles) ListWorkspaceFilesResponse {
	return ListWorkspaceFilesResponse{
		SessionID:        files.SessionID,
		WorkspaceVersion: files.WorkspaceVersion,
		CompareBaseSHA:   files.CompareBaseSHA,
		CompareBaseRef:   files.CompareBaseRef,
		CompareMode:      files.CompareMode,
		Files:            workspaceFileSummariesResponse(files.Files),
		Truncated:        files.Truncated,
		Sections:         workspaceFileSectionsResponse(files.Sections),
		Commits:          workspaceCommitsResponse(files.Commits),
		CommitsTruncated: files.CommitsTruncated,
		Summary:          WorkspaceSummary(files.Summary),
		Degraded:         files.Degraded,
		DegradedCode:     files.DegradedCode,
		Ahead:            files.Ahead,
		Behind:           files.Behind,
	}
}

func workspaceManifestResponse(manifest sessionsvc.WorkspaceManifest) WorkspaceManifestResponse {
	return WorkspaceManifestResponse{
		SessionID:        manifest.SessionID,
		WorkspaceVersion: manifest.WorkspaceVersion,
		CompareBaseSHA:   manifest.CompareBaseSHA,
		CompareBaseRef:   manifest.CompareBaseRef,
		CompareMode:      manifest.CompareMode,
		Files:            workspaceFileSummariesResponse(manifest.Files),
		Sections:         workspaceFileSectionsResponse(manifest.Sections),
		Summary:          WorkspaceSummary(manifest.Summary),
		Truncated:        manifest.Truncated,
		Stale:            manifest.Stale,
		Refreshing:       manifest.Refreshing,
		Degraded:         manifest.Degraded,
		DegradedCode:     manifest.DegradedCode,
	}
}

func workspaceHistoryResponse(history sessionsvc.WorkspaceHistory) WorkspaceHistoryResponse {
	var rangeSummary *WorkspaceSummary
	if history.RangeSummary != nil {
		summary := WorkspaceSummary(*history.RangeSummary)
		rangeSummary = &summary
	}
	return WorkspaceHistoryResponse{
		SessionID: history.SessionID, View: WorkspaceViewResponse(history.View), Commits: workspaceCommitsResponse(history.Commits),
		CommitsTruncated: history.CommitsTruncated, RangeSummary: rangeSummary, Ahead: history.Ahead, Behind: history.Behind, Landed: history.Landed,
	}
}

func workspaceBranchesResponse(branches sessionsvc.WorkspaceBranches) WorkspaceBranchesResponse {
	rows := make([]WorkspaceBranchResponse, 0, len(branches.Branches))
	for _, branch := range branches.Branches {
		rows = append(rows, WorkspaceBranchResponse{
			Kind: branch.Kind, Name: branch.Name, TipSHA: branch.TipSHA, UpdatedAt: optionalTime(branch.UpdatedAt),
			CheckedOut: branch.CheckedOut, Detached: branch.Detached, IsDefault: branch.IsDefault,
			PRNumber: branch.PRNumber, PRURL: branch.PRURL, PRTitle: branch.PRTitle,
		})
	}
	return WorkspaceBranchesResponse{
		SessionID: branches.SessionID, Supported: branches.Supported, DefaultBranch: branches.DefaultBranch, Branches: rows, Truncated: branches.Truncated,
	}
}

func prFilesResponse(files sessionsvc.PRFiles) ListPRFilesResponse {
	return ListPRFilesResponse{
		SessionID:        files.SessionID,
		Files:            workspaceFileSummariesResponse(files.Files),
		Commits:          workspaceCommitsResponse(files.Commits),
		CommitsTruncated: files.CommitsTruncated,
		Truncated:        files.Truncated,
		Summary:          WorkspaceSummary(files.Summary),
	}
}

func workspaceFileSummaryResponse(file sessionsvc.WorkspaceFileSummary) WorkspaceFileSummary {
	return WorkspaceFileSummary{
		Path:            file.Path,
		PreviousPath:    file.PreviousPath,
		Status:          file.Status,
		Additions:       file.Additions,
		Deletions:       file.Deletions,
		Size:            file.Size,
		Binary:          file.Binary,
		Editable:        file.Editable,
		FileFingerprint: file.FileFingerprint,
	}
}

func workspaceFileSummariesResponse(files []sessionsvc.WorkspaceFileSummary) []WorkspaceFileSummary {
	out := make([]WorkspaceFileSummary, 0, len(files))
	for _, file := range files {
		out = append(out, workspaceFileSummaryResponse(file))
	}
	return out
}

func workspaceFileSectionsResponse(sections sessionsvc.WorkspaceFileSections) WorkspaceFileSections {
	return WorkspaceFileSections{
		Uncommitted: workspaceFileSummariesResponse(sections.Uncommitted),
		Staged:      workspaceFileSummariesResponse(sections.Staged),
		Unstaged:    workspaceFileSummariesResponse(sections.Unstaged),
		Untracked:   workspaceFileSummariesResponse(sections.Untracked),
		Committed:   workspaceFileSummariesResponse(sections.Committed),
	}
}

func workspaceCommitsResponse(commits []sessionsvc.CommitSummary) []WorkspaceCommitSummary {
	out := make([]WorkspaceCommitSummary, 0, len(commits))
	for _, commit := range commits {
		out = append(out, WorkspaceCommitSummary{
			SHA:       commit.SHA,
			Subject:   commit.Subject,
			Author:    commit.Author,
			Timestamp: commit.Timestamp,
			Files:     workspaceFileSummariesResponse(commit.Files),
		})
	}
	return out
}

func workspaceFileResponse(file sessionsvc.WorkspaceFileDetail) WorkspaceFileResponse {
	return WorkspaceFileResponse{Historical: file.Historical,
		SessionID:        file.SessionID,
		Path:             file.Path,
		PreviousPath:     file.PreviousPath,
		Status:           file.Status,
		Additions:        file.Additions,
		Deletions:        file.Deletions,
		Size:             file.Size,
		Binary:           file.Binary,
		Deleted:          file.Deleted,
		Editable:         file.Editable,
		ImageMediaType:   file.ImageMediaType,
		Content:          file.Content,
		ContentTruncated: file.ContentTruncated,
		Diff:             file.Diff,
		DiffTruncated:    file.DiffTruncated,
		CompareBaseSHA:   file.CompareBaseSHA,
		CompareBaseRef:   file.CompareBaseRef,
		CompareMode:      file.CompareMode,
		WorkspaceVersion: file.WorkspaceVersion,
		FileFingerprint:  file.FileFingerprint,
	}
}

func workspaceCommitSelectionResponse(selection sessionsvc.WorkspaceCommitSelection) WorkspaceCommitSelectionResponse {
	response := WorkspaceCommitSelectionResponse{
		SessionID:  selection.SessionID,
		TipSHA:     selection.View.TipSHA,
		BaseSHA:    selection.View.BaseSHA,
		Contiguous: selection.Contiguous,
		Combinable: selection.Combinable,
		Files:      workspaceFileSummariesResponse(selection.Files),
		Summary:    WorkspaceSummary(selection.Summary),
		Truncated:  selection.Truncated,
	}
	if conflict := selection.Conflict; conflict != nil {
		paths := append([]string{}, conflict.Paths...)
		response.Conflict = &WorkspaceCommitConflictResponse{CommitSHA: conflict.CommitSHA, Paths: paths, Reason: conflict.Reason}
	}
	return response
}

func workspaceDiffsResponse(diffs sessionsvc.WorkspaceDiffs) WorkspaceDiffsResponse {
	groups := make([]WorkspaceDiffGroupResponse, 0, len(diffs.Groups))
	for _, group := range diffs.Groups {
		deferred := make([]WorkspaceDiffDeferredResponse, 0, len(group.Deferred))
		for _, item := range group.Deferred {
			deferred = append(deferred, WorkspaceDiffDeferredResponse{Path: item.Path, Reason: item.Reason})
		}
		groupErrors := make([]WorkspaceDiffErrorResponse, 0, len(group.Errors))
		for _, item := range group.Errors {
			groupErrors = append(groupErrors, WorkspaceDiffErrorResponse{Code: item.Code, Message: item.Message})
		}
		groups = append(groups, WorkspaceDiffGroupResponse{Repository: group.Repository, Patch: group.Patch, Truncated: group.Truncated, IncludedPaths: group.IncludedPaths, Deferred: deferred, Errors: groupErrors})
	}
	return WorkspaceDiffsResponse{SessionID: diffs.SessionID, WorkspaceVersion: diffs.WorkspaceVersion, Groups: groups}
}

func workspaceFileRevisionResponse(revision sessionsvc.WorkspaceFileRevision) WorkspaceFileRevisionResponse {
	return WorkspaceFileRevisionResponse{
		SessionID: revision.SessionID, Path: revision.Path, Side: revision.Side, Revision: revision.Revision,
		WorkspaceVersion: revision.WorkspaceVersion, MediaType: revision.MediaType, Encoding: revision.Encoding,
		Size: revision.Size, Exists: revision.Exists, Binary: revision.Binary, Truncated: revision.Truncated, Content: revision.Content,
	}
}

func workspaceFileSearchResponse(result sessionsvc.WorkspaceFileSearch) WorkspaceFileSearchResponse {
	items := make([]WorkspaceFileSearchResultResponse, 0, len(result.Results))
	for _, item := range result.Results {
		items = append(items, WorkspaceFileSearchResultResponse{Path: item.Path, Status: item.Status, Size: item.Size, Binary: item.Binary, FileFingerprint: item.FileFingerprint})
	}
	return WorkspaceFileSearchResponse{SessionID: result.SessionID, Query: result.Query, Results: items, NextCursor: result.NextCursor, Truncated: result.Truncated}
}

func workspaceTreeResponse(tree sessionsvc.WorkspaceTree) ListWorkspaceTreeResponse {
	out := make([]WorkspaceTreeEntry, 0, len(tree.Entries))
	for _, entry := range tree.Entries {
		out = append(out, WorkspaceTreeEntry{
			Name:       entry.Name,
			Path:       entry.Path,
			Type:       entry.Type,
			Status:     entry.Status,
			HasChanges: entry.HasChanges,
			Size:       entry.Size,
			Binary:     entry.Binary,
		})
	}
	return ListWorkspaceTreeResponse{Snapshot: tree.Snapshot,
		SessionID: tree.SessionID,
		Path:      tree.Path,
		Entries:   out,
		Truncated: tree.Truncated,
	}
}
