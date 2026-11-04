package apitypes

// GitHubCredentialRequest is what git's credential protocol knows: the host (and
// protocol and path, which the daemon ignores) of the remote asking.
type GitHubCredentialRequest struct {
	Host     string `json:"host"`
	Protocol string `json:"protocol,omitempty"`
	Path     string `json:"path,omitempty"`
}

// GitHubCredentialResponse is a credential for one use. The caller must not
// persist it or put it in a long-lived environment.
type GitHubCredentialResponse struct {
	Username string `json:"username"`
	Token    string `json:"token"`
	// Source says where the token came from: account, project_env or daemon_env.
	Source string `json:"source" enum:"account,project_env,daemon_env"`
}
