package ghaccount

import (
	"context"
	"strings"
)

// Inspection statuses.
const (
	StatusOK           = "ok"             // token is the expected account and can push to the repo
	StatusNoAccount    = "no_account"     // no dedicated account: sessions keep the default credentials
	StatusNoToken      = "no_token"       // gh has no token for the account (not logged in)
	StatusMismatch     = "mismatch"       // the token authenticates as another account
	StatusNoRepoAccess = "no_repo_access" // the account cannot see or push to the repo
	StatusUnverified   = "unverified"     // GitHub could not be reached
)

// Inspection is what a project's GitHub account actually resolves to. It never
// carries a token.
type Inspection struct {
	Status     string
	Login      string // the account Tallpine picked ("" = none)
	TokenLogin string // the login `GET /user` reports for that account's token
}

// Inspect fetches login's token fresh (no cache, no refusal) and reports which
// account it authenticates as and whether it can push to repo.
func (r *Resolver) Inspect(ctx context.Context, login string, repo Repo) Inspection {
	out := Inspection{Login: login}
	if login == "" {
		out.Status = StatusNoAccount
		return out
	}
	tctx, cancel := context.WithTimeout(ctx, dur(r.StepTimeout, 3*stepTimeout))
	defer cancel()
	token, err := r.GH.Token(tctx, login)
	if err != nil || strings.TrimSpace(token) == "" {
		out.Status = StatusNoToken
		return out
	}
	if r.Identity != nil {
		actual, err := r.Identity.Login(tctx, token)
		if err != nil {
			out.Status = StatusUnverified
			return out
		}
		out.TokenLogin = actual
		if !strings.EqualFold(actual, login) {
			out.Status = StatusMismatch
			return out
		}
	}
	can, err := r.Push.CanPush(tctx, token, repo)
	switch {
	case err != nil:
		out.Status = StatusUnverified
	case !can:
		out.Status = StatusNoRepoAccess
	default:
		out.Status = StatusOK
	}
	return out
}
