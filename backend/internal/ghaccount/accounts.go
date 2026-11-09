package ghaccount

import (
	"context"
	"sync"
)

// Push answers for one account against one repo.
const (
	PushYes     = "yes"
	PushNo      = "no"
	PushUnknown = "unknown" // no token, GitHub unreachable, or the token is another account's
)

// Account is one signed-in gh login. Push is "" when no repo was asked about.
// It never carries a token.
type Account struct {
	Login string
	Push  string
}

// AccountList is the picker's data for one project. Tokens are never part of it.
type AccountList struct {
	Accounts []Account
	// Repo is owner/name of the project's GitHub repo; "" when it has none
	// (origin not on github.com, or a scratch project).
	Repo string
	// Override is the project's pinned account ("" = auto-pick).
	Override string
	// AutoPick is the account sessions get with no pin ("" = the machine's
	// default gh credentials, see the Resolver docs for when).
	AutoPick string
	// Unavailable is true when `gh` could not list accounts (not installed).
	Unavailable bool
}

// SignedIn lists the accounts signed in to github.com, bypassing the short
// login cache so a sign-in made a moment ago shows up. With repo set, each
// account's push access to it is checked concurrently.
func (r *Resolver) SignedIn(ctx context.Context, repo *Repo) ([]Account, error) {
	r.mu.Lock()
	r.logins = nil
	r.mu.Unlock()
	logins, err := r.listLogins(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Account, len(logins))
	var wg sync.WaitGroup
	for i, l := range logins {
		out[i] = Account{Login: l}
		if repo == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch r.Inspect(ctx, l, *repo).Status {
			case StatusOK:
				out[i].Push = PushYes
			case StatusNoRepoAccess:
				out[i].Push = PushNo
			default:
				out[i].Push = PushUnknown
			}
		}()
	}
	wg.Wait()
	return out, nil
}
