package domain

// SessionBranchState is the session branch's commit and push facts.
type SessionBranchState struct {
	// Commits is how many commits the branch has on top of its base.
	Commits int `json:"commits"`
	// RemoteBranch is the remote-tracking branch, such as "origin/feat/x".
	// Empty means the branch has never been pushed.
	RemoteBranch string `json:"remoteBranch,omitempty"`
	// Unpushed is how many branch commits are not on RemoteBranch. It equals
	// Commits while RemoteBranch is empty.
	Unpushed int `json:"unpushed"`
	// Head is the branch tip the other fields describe; a review verdict is
	// current only while its target matches it.
	Head string `json:"head,omitempty"`
	// Landed is true once commits the branch made are all on its base: the
	// tip moved off the commit the session was created from and the base now
	// holds it, so no commits are left to land.
	Landed bool `json:"landed,omitempty"`
}
