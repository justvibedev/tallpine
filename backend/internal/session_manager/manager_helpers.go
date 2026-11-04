package sessionmanager

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func runtimeHandle(meta domain.SessionMetadata) ports.RuntimeHandle {
	return ports.RuntimeHandle{ID: meta.RuntimeHandleID}
}

func workspaceInfo(rec domain.SessionRecord) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      rec.Metadata.WorkspacePath,
		Branch:    rec.Metadata.Branch,
		BaseRef:   rec.Metadata.DiffBaseRef,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
		RepoPath:  rec.Metadata.WorkspaceRepoPath,
	}
}

func workspaceInfoFromRepoInfo(info ports.WorkspaceRepoInfo) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      info.Path,
		Branch:    info.Branch,
		BaseRef:   info.BaseRef,
		SessionID: info.SessionID,
		ProjectID: info.ProjectID,
		RepoPath:  info.RepoPath,
	}
}

// savedCatalogCovers reports whether a saved model catalog can answer a launch's
// model and effort checks without a live rediscovery.
func savedCatalogCovers(catalog ports.AgentModelCatalog, modelID, effort string) bool {
	if catalog.Stale || len(catalog.Models) == 0 || (catalog.RefreshState != "" && catalog.RefreshState != "idle") {
		return false
	}
	for _, item := range catalog.Models {
		if item.ID == modelID || (modelID == "" && item.IsDefault) {
			if effort == "" || containsString(item.Efforts, effort) {
				return true
			}
			if len(item.Efforts) == 0 {
				if concrete := newestClaudeFamilyModel(catalog.Models, item.ID, true); concrete != nil {
					return containsString(concrete.Efforts, effort)
				}
			}
			return false
		}
	}
	if concrete := newestClaudeFamilyModel(catalog.Models, modelID, false); concrete != nil {
		return effort == "" || containsString(concrete.Efforts, effort)
	}
	return false
}
