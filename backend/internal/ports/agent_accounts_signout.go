package ports

// AgentAccountSignOutOnRemove is implemented by providers whose CLI sign-out
// is safe to run when an account is removed or a sign-in is discarded.
// Claude Code's sign-out also revokes the folder's tokens on the server, which
// cannot be shown to leave another login of the same user alone, so Claude does
// not implement it: removing an account only deletes its folder.
type AgentAccountSignOutOnRemove interface {
	SignsOutOnRemove() bool
}

// SignsOutOnRemove reports whether p wants Logout called on removal.
func SignsOutOnRemove(p AgentAccountProviderCLI) bool {
	s, ok := p.(AgentAccountSignOutOnRemove)
	return ok && s.SignsOutOnRemove()
}
