package domain

// CodexLoginCheck is the one rule for what a Codex account observation says
// about its login. A subscription sign-in is an authorized ChatGPT login; any
// authorized login (an API key included) can launch the CLI. The agent account
// list, failover, adoption and the Codex account service all read an account's
// sign-in through it.
func CodexLoginCheck(auth AgentAuthenticationState, method CodexAuthMethod, email *string) AgentAccountIdentityCheck {
	authorized := auth == AgentAuthenticationAuthorized
	check := AgentAccountIdentityCheck{
		SignedIn: authorized && method == CodexAuthMethodChatGPT,
		LoggedIn: authorized,
	}
	if email != nil {
		check.Email = *email
	}
	return check
}

// CodexSnapshotLoginCheck is CodexLoginCheck for a managed account snapshot. A
// signed-out or broken account has no login, whatever its last observation said.
func CodexSnapshotLoginCheck(s CodexAccountSnapshot) AgentAccountIdentityCheck {
	if s.Status != CodexAccountStatusValid {
		return AgentAccountIdentityCheck{}
	}
	return CodexLoginCheck(s.Authentication.State, s.AuthMethod, s.AccountEmail)
}

// CodexAccountMethod is how a Codex auth method signs a harness in.
func CodexAccountMethod(method CodexAuthMethod) AgentAccountMethod {
	switch method {
	case CodexAuthMethodChatGPT:
		return AgentAccountMethodAccount
	case CodexAuthMethodAPIKey:
		return AgentAccountMethodAPIKey
	}
	return ""
}

// CodexAccountViewOf is the Codex account view of a saved AgentAccount: the
// fields the AgentAccount row owns. The caller adds what is observed at read
// time (status, authentication, capacity). AgentAccount is the only stored
// model; CodexAccountSnapshot is only ever derived from it.
func CodexAccountViewOf(a AgentAccount, label string) CodexAccountSnapshot {
	view := CodexAccountSnapshot{
		ID: a.ID, Label: label, Source: CodexAccountSource(a.Source),
		AuthMethod: CodexAuthMethod(a.AuthMethod), CreatedAt: a.CreatedAt,
	}
	if a.Email != "" {
		email := a.Email
		view.AccountEmail = &email
	}
	return view
}
