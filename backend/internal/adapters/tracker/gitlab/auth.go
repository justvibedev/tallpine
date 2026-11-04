package gitlab

import (
	scmgitlab "tallpine.eu/tallpine/backend/internal/adapters/scm/gitlab"
)

// ErrNoToken re-exports the SCM provider's canonical sentinel so the
// tracker and SCM adapter share one error identity. Callers that need to
// distinguish "no token" from other failures should use
// errors.Is(err, scmgitlab.ErrNoToken) regardless of whether the failure
// originated in the tracker or the SCM provider.
var ErrNoToken = scmgitlab.ErrNoToken

// DefaultTokenSource returns the standard GitLab token source chain used
// by the tracker: TALLPINE_GITLAB_TOKEN → GITLAB_TOKEN → glab auth status
// --show-token. This mirrors the SCM provider's chain so both adapters
// honor the same precedence.
func DefaultTokenSource() scmgitlab.TokenSource {
	return scmgitlab.FallbackTokenSource{
		&scmgitlab.EnvTokenSource{EnvVars: []string{"TALLPINE_GITLAB_TOKEN"}},
		&scmgitlab.GLabTokenSource{},
	}
}
