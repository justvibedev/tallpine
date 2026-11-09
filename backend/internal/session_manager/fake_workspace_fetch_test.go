package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/ports"
)

type fetchDefaultBranchCall struct {
	repoPath string
	remote   string
	branch   string
	baseRef  string
}

func (w *fakeWorkspace) FetchDefaultBranch(ctx context.Context, repoPath string, target ports.WorkspaceDefaultBranch, gitEnv []string) error {
	w.fetches = append(w.fetches, fetchDefaultBranchCall{repoPath: repoPath, remote: target.Remote, branch: target.Branch, baseRef: target.BaseRef})
	w.fetchEnvs = append(w.fetchEnvs, gitEnv)
	if w.sharedLog != nil {
		*w.sharedLog = append(*w.sharedLog, "FetchDefaultBranch:"+target.Remote+"/"+target.Branch)
	}
	if w.fetchFunc != nil {
		return w.fetchFunc(ctx, repoPath, target)
	}
	return w.fetchErr
}
